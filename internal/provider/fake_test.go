package provider

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

type obj = map[string]any

// fakeAPI is an in-memory account that behaves like the API in the ways the
// provider depends on: one-element arrays in and out of a write, a pending
// state that one read later has turned into READY, a queued deletion that one
// read later has completed, and 404 for an object that is not there.
type fakeAPI struct {
	mu      sync.Mutex
	objects map[string]obj // by id; "_kind" holds the kind
	created int
	writes  []string // "kind/verb", in order
}

// defaults are the fields the API fills in on create.
var defaults = map[string]obj{
	"domain": {"delegated_dns_zone": false, "is_valid_hostname": true, "dkim_record": nil, "dkim_pubkey": nil, "dkim_privkey": "PRIVATE"},
}

// seed adds an object as if it had always been there.
func (f *fakeAPI) seed(kind string, o obj) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored := obj{"_kind": kind, "state": "READY", "ready": true}
	maps.Copy(stored, defaults[kind])
	maps.Copy(stored, o)
	f.objects[o["id"].(string)] = stored
}

// remove deletes an object behind the provider's back.
func (f *fakeAPI) remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, id)
}

// named returns the ids of the objects of a kind whose name has the prefix.
func (f *fakeAPI) named(kind, prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for id, o := range f.objects {
		if name, _ := o["name"].(string); o["_kind"] == kind && strings.HasPrefix(name, prefix) {
			ids = append(ids, id)
		}
	}
	return ids
}

func (f *fakeAPI) wrote(kindVerb string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, w := range f.writes {
		if w == kindVerb {
			n++
		}
	}
	return n
}

func public(o obj) obj {
	out := maps.Clone(o)
	delete(out, "_kind")
	return out
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1"), "/"), "/")
	reply := func(status int, v any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	notFound := func() { reply(http.StatusNotFound, obj{"id": []string{"Not found."}}) }
	if len(parts) < 2 {
		notFound()
		return
	}
	kind, verb := parts[0], parts[1]

	if r.Method == http.MethodGet {
		switch {
		case verb == "list":
			list := []obj{}
			for _, id := range slices.Sorted(maps.Keys(f.objects)) {
				if f.objects[id]["_kind"] == kind {
					list = append(list, public(f.objects[id]))
				}
			}
			if kind == "server" {
				reply(http.StatusOK, obj{"web_servers": list, "imap_servers": []obj{}, "smtp_servers": []obj{}})
				return
			}
			reply(http.StatusOK, list)
		case verb == "read" && len(parts) == 3:
			o, ok := f.objects[parts[2]]
			if !ok || o["_kind"] != kind {
				notFound()
				return
			}
			reply(http.StatusOK, public(o))
			// The queued change completes after this read has reported it pending.
			if o["state"] == "PENDING_DELETE" {
				delete(f.objects, parts[2])
			} else {
				o["state"], o["ready"] = "READY", true
			}
		default:
			notFound()
		}
		return
	}

	body, _ := io.ReadAll(r.Body)
	var reqs []obj
	if err := json.Unmarshal(body, &reqs); err != nil || len(reqs) != 1 {
		reply(http.StatusBadRequest, obj{"errors": []string{"expected an array of one object"}})
		return
	}
	req := reqs[0]
	f.writes = append(f.writes, kind+"/"+verb)

	if verb == "create" {
		f.created++
		o := obj{"_kind": kind, "id": fmt.Sprintf("60000000-0000-0000-0000-%012d", f.created)}
		maps.Copy(o, defaults[kind])
		maps.Copy(o, req)
		o["state"], o["ready"] = "PENDING_CREATE", false
		f.objects[o["id"].(string)] = o
		reply(http.StatusOK, []obj{public(o)})
		return
	}
	id, _ := req["id"].(string)
	o, ok := f.objects[id]
	if !ok || o["_kind"] != kind {
		notFound()
		return
	}
	switch verb {
	case "update":
		maps.Copy(o, req)
		o["state"], o["ready"] = "PENDING_UPDATE", false
		reply(http.StatusOK, []obj{public(o)})
	case "delete":
		o["state"], o["ready"] = "PENDING_DELETE", false
		reply(http.StatusOK, obj{"ok": "Queued for deletion."})
	default:
		notFound()
	}
}

// fakeProvider serves a fakeAPI over TLS and returns it with the provider
// factories and a provider block that points at it. The test is skipped
// without a terraform binary, so the offline tests never try to download one.
func fakeProvider(t *testing.T) (*fakeAPI, map[string]func() (tfprotov6.ProviderServer, error), string) {
	t.Helper()
	// A version-manager shim is on PATH but answers only inside a directory that
	// pins a version, and the test framework runs terraform in a temporary one.
	bin := cmp.Or(os.Getenv("TF_ACC_TERRAFORM_PATH"), "terraform")
	probe := exec.Command(bin, "version")
	probe.Dir = t.TempDir()
	if err := probe.Run(); err != nil {
		t.Skipf("no usable terraform binary (%s): set TF_ACC_TERRAFORM_PATH, as make test does", bin)
	}

	f := &fakeAPI{objects: map[string]obj{}}
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)

	factories := map[string]func() (tfprotov6.ProviderServer, error){
		"opalstack": providerserver.NewProtocol6WithError(provider.Provider(&opalstackProvider{version: "test", httpClient: srv.Client()})),
	}
	block := fmt.Sprintf(`
provider "opalstack" {
  token    = "1111111111111111111111111111111111111111"
  base_url = %q
}
`, srv.URL+"/api/v1")
	return f, factories, block
}
