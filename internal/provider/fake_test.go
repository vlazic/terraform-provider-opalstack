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
	"time"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
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
	bodies  []string // the request body of each write
	// tune points a provider's client at this fake. fakeProvider sets it.
	tune func(*client.Config)
}

// get returns a copy of an object as a read would report it once it is ready.
func (f *fakeAPI) get(id string) obj {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := public(f.objects[id])
	delete(o, "state")
	delete(o, "ready")
	return o
}

// defaults are the fields the API fills in on create.
var defaults = map[string]obj{
	"app":       {"port": nil, "installer_url": nil},
	"dnsrecord": {"priority": 10, "ttl": 3600},
	"site":      {"ip4": nil, "ip6": nil, "cert": nil, "primary_domain": nil, "redirect": false, "generate_le": false, "disabled": false},
	"cert":      {"intermediates": nil, "exp_date": "2030-01-01T00:00:00Z"},
	"mariauser": {"external": false},
	"psqluser":  {"external": false},
	"mariadb":   {"charset": "utf8", "dbusers_readwrite": []any{}, "dbusers_readonly": []any{}},
	"psqldb":    {"charset": "utf8", "dbusers_readwrite": []any{}, "dbusers_readonly": []any{}},
	"domain":    {"delegated_dns_zone": false, "is_valid_hostname": true, "dkim_record": nil, "dkim_pubkey": nil, "dkim_privkey": "PRIVATE"},
	"mailuser": {"procmailrc": "", "autoresponder_enable": false, "autoresponder_subject": "", "autoresponder_message": "",
		"autoresponder_noreply": []any{}},
	"osvar": {"content": "", "osusers": []any{}, "global": false},
}

// createOnly are the fields that only the answer to a create carries.
var createOnly = map[string]obj{
	"osuser":    {"default_password": "fake-default-password"},
	"mariauser": {"default_password": "fake-default-password"},
	"psqluser":  {"default_password": "fake-default-password"},
	"mailuser":  {"default_password": "fake-default-password"},
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

// ids returns the objects of a kind whose name has one of the prefixes, or all
// of them when none is given.
func (f *fakeAPI) ids(kind string, prefixes ...string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for id, o := range f.objects {
		name, _ := o["name"].(string)
		if o["_kind"] == kind && (len(prefixes) == 0 ||
			slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(name, p) })) {
			ids = append(ids, id)
		}
	}
	return ids
}

// edit changes an object behind the provider's back; a nil change deletes it.
func (f *fakeAPI) edit(id string, change func(obj)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if change == nil {
		delete(f.objects, id)
		return
	}
	change(f.objects[id])
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

// lastBody is the body of the most recent write of that kind and verb.
func (f *fakeAPI) lastBody(kindVerb string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.writes) - 1; i >= 0; i-- {
		if f.writes[i] == kindVerb {
			return f.bodies[i]
		}
	}
	return ""
}

// sent checks the body of the most recent write of a kind and verb: the parts
// it must hold, and the ones it must not.
func (f *fakeAPI) sent(kindVerb string, has, lacks []string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		body := f.lastBody(kindVerb)
		for _, part := range has {
			if !strings.Contains(body, part) {
				return fmt.Errorf("%s lacks %s: %s", kindVerb, part, body)
			}
		}
		for _, part := range lacks {
			if strings.Contains(body, part) {
				return fmt.Errorf("%s must not send %s: %s", kindVerb, part, body)
			}
		}
		return nil
	}
}

// fill adds what the API derives from a write: an app's server and shell user
// name, and a setting of its own next to the ones the caller sent.
func (f *fakeAPI) fill(kind string, o obj, creating bool) {
	if kind == "site" {
		// Each route gets an id, and the site lives where its first app does.
		routes, _ := o["routes"].([]any)
		for i, r := range routes {
			route := r.(obj)
			route["id"] = fmt.Sprintf("70000000-0000-0000-0000-%012d", i)
			if app, ok := f.objects[fmt.Sprint(route["app"])]; ok && i == 0 {
				o["server"] = app["server"]
			}
		}
		return
	}
	if kind == "cert" {
		o["sha256_fingerprint"] = fmt.Sprintf("fake:%d", len(fmt.Sprint(o["cert"])))
	}
	if rules, ok := o["procmailrc"].(string); ok && kind == "mailuser" {
		o["procmailrc"] = strings.TrimRight(rules, "\n") // stored without its trailing newline, as the API does
	}
	if kind != "app" {
		return
	}
	if owner, ok := f.objects[fmt.Sprint(o["osuser"])]; ok {
		o["osuser_name"], o["server"] = owner["name"], owner["server"]
	}
	if creating && o["type"] == portType {
		o["port"] = 20000 + f.created // assigned on create only, as the API does
	}
	settings := obj{"auto_site_url": false}
	if sent, ok := o["json"].(obj); ok {
		maps.Copy(settings, sent)
	}
	o["json"] = settings
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
	f.bodies = append(f.bodies, string(body))

	if verb == "create" {
		f.created++
		o := obj{"_kind": kind, "id": fmt.Sprintf("60000000-0000-0000-0000-%012d", f.created)}
		maps.Copy(o, defaults[kind])
		maps.Copy(o, req)
		f.fill(kind, o, true)
		o["state"], o["ready"] = "PENDING_CREATE", false
		f.objects[o["id"].(string)] = o
		answer := public(o)
		maps.Copy(answer, createOnly[kind])
		reply(http.StatusOK, []obj{answer})
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
		f.fill(kind, o, false)
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
	f.tune = func(cfg *client.Config) {
		cfg.HTTPClient = srv.Client()
		cfg.RequestInterval, cfg.PollInterval = time.Nanosecond, time.Millisecond // a fake API needs no pacing
	}

	factories := map[string]func() (tfprotov6.ProviderServer, error){
		"opalstack": providerserver.NewProtocol6WithError(provider.Provider(&opalstackProvider{version: "test", tune: f.tune})),
	}
	block := fmt.Sprintf(`
provider "opalstack" {
  token    = "1111111111111111111111111111111111111111"
  base_url = %q
}
`, srv.URL+"/api/v1")
	return f, factories, block
}

// expectEmptyPlan is for a step that must plan nothing at all.
func expectEmptyPlan() resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}
}
