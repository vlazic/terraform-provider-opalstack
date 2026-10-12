package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every fixture in this file is written by hand from spec/openapi.yaml with
// placeholder values. None was captured from a real account.
const (
	testToken = "1111111111111111111111111111111111111111"
	idA       = "01010101-0202-0303-0404-050505050505"
	idB       = "11111111-1212-1313-1414-151515151515"
	idC       = "21212121-2222-2323-2424-252525252525"
)

type recorded struct {
	method, path, auth, body string
}

type fakeAPI struct {
	t      *testing.T
	mu     sync.Mutex
	calls  []recorded
	routes map[string][]reply // "METHOD path" -> replies, the last one repeating
}

type reply struct {
	status int
	body   string
	header map[string]string
}

func newFake(t *testing.T) (*fakeAPI, *Client, *[]string) {
	t.Helper()
	f := &fakeAPI{t: t, routes: map[string][]reply{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)

	var logs []string
	c, err := New(Config{
		Token:   testToken,
		BaseURL: srv.URL,
		Log: func(_ context.Context, msg string, fields map[string]any) {
			raw, _ := json.Marshal(fields)
			logs = append(logs, msg+" "+string(raw))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.minInterval, c.retryBase, c.pollMin, c.pollMax = 0, time.Millisecond, time.Millisecond, 2*time.Millisecond
	return f, c, &logs
}

func (f *fakeAPI) on(method, path string, replies ...reply) {
	f.routes[method+" "+path] = replies
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, recorded{r.Method, r.URL.Path, r.Header.Get("Authorization"), string(body)})
	key := r.Method + " " + r.URL.Path
	queue := f.routes[key]
	var rep reply
	switch {
	case len(queue) == 0:
		rep = reply{status: http.StatusTeapot, body: `{"detail":"no fixture for ` + key + `"}`}
	case len(queue) == 1:
		rep = queue[0]
	default:
		rep, f.routes[key] = queue[0], queue[1:]
	}
	f.mu.Unlock()

	for k, v := range rep.header {
		w.Header().Set(k, v)
	}
	w.WriteHeader(rep.status)
	_, _ = io.WriteString(w, rep.body)
}

// count returns how many requests matched. An empty method or path matches any.
func (f *fakeAPI) count(method, path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if (method == "" || c.method == method) && (path == "" || c.path == path) {
			n++
		}
	}
	return n
}

func (f *fakeAPI) last() recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func ok(body string) reply { return reply{status: 200, body: body} }

func TestNewRequiresToken(t *testing.T) {
	if _, err := New(Config{Token: "  \n"}); err == nil {
		t.Fatal("expected an error for a blank token")
	}
}

func TestCreateSendsOneElementArray(t *testing.T) {
	f, c, _ := newFake(t)
	f.on("POST", "/osuser/create/", ok(`[{"id":"`+idA+`","state":"PENDING_CREATE","ready":false,
		"name":"tfacc-abc123","server":"`+idB+`","default_password":"placeholder-pw"}]`))

	got, err := c.OSUsers().Create(context.Background(), OSUserCreate{Server: idB, Name: "tfacc-abc123"})
	if err != nil {
		t.Fatal(err)
	}
	call := f.last()
	if call.auth != "Token "+testToken {
		t.Errorf("Authorization = %q", call.auth)
	}
	if want := `[{"server":"` + idB + `","name":"tfacc-abc123"}]`; call.body != want {
		t.Errorf("body = %s, want %s", call.body, want)
	}
	if got.ID != idA || got.Ready || got.State != "PENDING_CREATE" || got.DefaultPassword != "placeholder-pw" {
		t.Errorf("unexpected object: %+v", got)
	}
}

func TestCreateRejectsWrongElementCount(t *testing.T) {
	f, c, _ := newFake(t)
	f.on("POST", "/domain/create/", ok(`[]`))
	if _, err := c.Domains().Create(context.Background(), DomainCreate{Name: "tfacc-a.example.com"}); err == nil {
		t.Fatal("expected an error for an empty response array")
	}
}

func TestUpdateIsPartial(t *testing.T) {
	f, c, _ := newFake(t)
	f.on("POST", "/site/update/", ok(`[{"id":"`+idA+`","state":"PENDING_UPDATE","ready":false,"name":"tfacc-site","disabled":true}]`))

	disabled := true
	if _, err := c.Sites().Update(context.Background(), SiteUpdate{ID: idA, Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if want := `[{"id":"` + idA + `","disabled":true}]`; f.last().body != want {
		t.Errorf("body = %s, want %s", f.last().body, want)
	}

	// An empty list and an explicit null are both sendable, and distinct from "unchanged".
	clear := SiteUpdate{ID: idA, Routes: []Route{}, Cert: Null[string](), IP4: Set(idB)}
	if _, err := c.Sites().Update(context.Background(), clear); err != nil {
		t.Fatal(err)
	}
	if want := `[{"id":"` + idA + `","ip4":"` + idB + `","routes":[],"cert":null}]`; f.last().body != want {
		t.Errorf("clearing: body = %s, want %s", f.last().body, want)
	}
}

func TestSiteCreateSendsArraysNeverNull(t *testing.T) {
	f, c, _ := newFake(t)
	f.on("POST", "/site/create/", ok(`[{"id":"`+idA+`","name":"tfacc-site"}]`))
	if _, err := c.Sites().Create(context.Background(), SiteCreate{Name: "tfacc-site", Domains: []string{idB}}); err != nil {
		t.Fatal(err)
	}
	if want := `[{"name":"tfacc-site","domains":["` + idB + `"],"routes":[]}]`; f.last().body != want {
		t.Errorf("body = %s, want %s", f.last().body, want)
	}
}

func TestNewRejectsUnusableBaseURL(t *testing.T) {
	for _, bad := range []string{"my.opalstack.com/api/v1", "ftp://example.com", "https://"} {
		if _, err := New(Config{Token: testToken, BaseURL: bad}); err == nil {
			t.Errorf("base URL %q: expected an error", bad)
		}
	}
}

func TestDeleteAcceptsQueuedObjectReply(t *testing.T) {
	f, c, _ := newFake(t)
	f.on("POST", "/app/delete/", ok(`{"ok":"Queued for deletion."}`))
	if err := c.Apps().Delete(context.Background(), idA); err != nil {
		t.Fatal(err)
	}
	if want := `[{"id":"` + idA + `"}]`; f.last().body != want {
		t.Errorf("body = %s, want %s", f.last().body, want)
	}
}

// The API can lose a delete that arrives while a create is pending: the
// object comes back as READY. The wait notices and sends the delete again.
func TestWaitDeletedResendsALostDelete(t *testing.T) {
	f, c, _ := newFake(t)
	f.on("GET", "/app/read/"+idA,
		ok(`{"id":"`+idA+`","state":"PENDING_DELETE","ready":false}`),
		ok(`{"id":"`+idA+`","state":"READY","ready":true}`),
		ok(`{"id":"`+idA+`","state":"PENDING_DELETE","ready":false}`),
		reply{status: 404, body: `{"id":["Not found."]}`})
	f.on("POST", "/app/delete/", ok(`{"ok":"Queued for deletion."}`))
	if err := c.Apps().WaitDeleted(context.Background(), idA); err != nil {
		t.Fatal(err)
	}
	if n := f.count("POST", "/app/delete/"); n != 1 {
		t.Errorf("%d deletes were sent, want exactly one resend", n)
	}
}

func TestServerListIsAnObject(t *testing.T) {
	f, c, _ := newFake(t)
	f.on("GET", "/server/list/", ok(`{"web_servers":[{"id":"`+idB+`","hostname":"opal1.opalstack.com","type":"web"}],
		"imap_servers":[],"smtp_servers":[]}`))
	got, err := c.ListServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Web) != 1 || got.Web[0].Hostname != "opal1.opalstack.com" {
		t.Errorf("unexpected servers: %+v", got)
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name    string
		rep     reply
		is      error
		isNot   []error
		message string
	}{
		{"not found, field shape", reply{status: 404, body: `{"id":["Not found."]}`}, ErrNotFound, []error{ErrForbidden, ErrUnauthorized}, "id: Not found."},
		{"not found, errors shape", reply{status: 404, body: `{"errors":["Not found."]}`}, ErrNotFound, nil, "Not found."},
		{"not found, html", reply{status: 404, body: "<!doctype html><h1>Not Found</h1>"}, ErrNotFound, nil, "<h1>Not Found</h1>"},
		{"forbidden", reply{status: 403, body: `{"detail":"No permission."}`}, ErrForbidden, []error{ErrNotFound}, "No permission."},
		{"unauthorized", reply{status: 401, body: `{"detail":"Invalid token."}`}, ErrUnauthorized, []error{ErrNotFound}, "Invalid token."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, c, _ := newFake(t)
			f.on("GET", "/app/read/"+idA, tc.rep)
			_, err := c.Apps().Read(context.Background(), idA)
			if !errors.Is(err, tc.is) {
				t.Fatalf("err = %v, want it to match %v", err, tc.is)
			}
			for _, not := range tc.isNot {
				if errors.Is(err, not) {
					t.Errorf("err = %v must not match %v", err, not)
				}
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Errorf("err = %q, want it to contain %q", err, tc.message)
			}
		})
	}
}

func TestValidationErrorListsFields(t *testing.T) {
	f, c, _ := newFake(t)
	f.on("POST", "/app/create/", reply{status: 400, body: `[{"name":["Invalid name."],"type":["Not a valid choice."]}]`})
	_, err := c.Apps().Create(context.Background(), AppCreate{Name: "Bad Name", OSUser: idB, Type: "XXX"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Fatalf("err = %v, want an APIError with status 400", err)
	}
	if got := strings.Join(apiErr.Messages, "|"); got != "name: Invalid name.|type: Not a valid choice." {
		t.Errorf("messages = %q", got)
	}
}

func TestReadRefusesMalformedID(t *testing.T) {
	f, c, _ := newFake(t)
	for _, id := range []string{"", "../token/list", "a?b", "..", "tfacc-abc123", "%2e%2e%2ftoken%2flist", idA + "x"} {
		if _, err := c.Apps().Read(context.Background(), id); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("id %q: err = %v, want a refusal that is not ErrNotFound", id, err)
		}
		if err := c.Apps().WaitDeleted(context.Background(), id); err == nil {
			t.Errorf("id %q: WaitDeleted reported it gone", id)
		}
	}
	if n := f.count("", ""); n != 0 {
		t.Errorf("%d requests were sent for malformed ids", n)
	}
}

func TestRetries(t *testing.T) {
	object := ok(`{"id":"` + idA + `","name":"tfacc-x"}`)

	t.Run("GET retries a server error", func(t *testing.T) {
		f, c, _ := newFake(t)
		f.on("GET", "/app/read/"+idA, reply{status: 500}, reply{status: 503}, object)
		if _, err := c.Apps().Read(context.Background(), idA); err != nil {
			t.Fatal(err)
		}
		if n := f.count("GET", "/app/read/"+idA); n != 3 {
			t.Errorf("%d requests, want 3", n)
		}
	})

	t.Run("POST does not retry a server error", func(t *testing.T) {
		for _, status := range []int{500, 502} {
			f, c, _ := newFake(t)
			f.on("POST", "/app/create/", reply{status: status}, ok(`[{"id":"`+idA+`"}]`))
			if _, err := c.Apps().Create(context.Background(), AppCreate{Name: "tfacc-x", OSUser: idB, Type: "STA"}); err == nil {
				t.Fatalf("expected the %d to be returned", status)
			}
			if n := f.count("POST", "/app/create/"); n != 1 {
				t.Errorf("status %d: %d requests, want 1: a repeated create could make a second object", status, n)
			}
		}
	})

	t.Run("Retry-After is capped", func(t *testing.T) {
		f, c, _ := newFake(t)
		f.on("GET", "/app/read/"+idA, reply{status: 429, header: map[string]string{"Retry-After": "86400"}}, object)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := c.Apps().Read(ctx, idA)
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 429 {
			t.Fatalf("err = %v, want the 429 once the deadline passes", err)
		}
		c.mu.Lock()
		held := time.Until(c.next)
		c.mu.Unlock()
		if held > maxRetryAfter || held < maxRetryAfter-5*time.Second {
			t.Errorf("other requests are held for %v, want about %v", held, maxRetryAfter)
		}
	})

	t.Run("POST retries 429", func(t *testing.T) {
		f, c, _ := newFake(t)
		f.on("POST", "/app/create/", reply{status: 429}, ok(`[{"id":"`+idA+`"}]`))
		if _, err := c.Apps().Create(context.Background(), AppCreate{Name: "tfacc-x", OSUser: idB, Type: "STA"}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("gives up after maxAttempts", func(t *testing.T) {
		f, c, _ := newFake(t)
		f.on("GET", "/app/read/"+idA, reply{status: 503})
		if _, err := c.Apps().Read(context.Background(), idA); err == nil {
			t.Fatal("expected an error")
		}
		if n := f.count("GET", "/app/read/"+idA); n != maxAttempts {
			t.Errorf("%d requests, want %d", n, maxAttempts)
		}
	})

	t.Run("404 and 401 are not retried", func(t *testing.T) {
		for _, status := range []int{401, 403, 404} {
			f, c, _ := newFake(t)
			f.on("GET", "/app/read/"+idA, reply{status: status, body: `{"detail":"x"}`})
			_, _ = c.Apps().Read(context.Background(), idA)
			if n := f.count("GET", "/app/read/"+idA); n != 1 {
				t.Errorf("status %d: %d requests, want 1", status, n)
			}
		}
	})
}

func TestWaitReady(t *testing.T) {
	f, c, _ := newFake(t)
	f.on("GET", "/osuser/read/"+idA,
		ok(`{"id":"`+idA+`","state":"PENDING_CREATE","ready":false}`),
		ok(`{"id":"`+idA+`","state":"PENDING_CREATE","ready":false}`),
		ok(`{"id":"`+idA+`","state":"READY","ready":true,"name":"tfacc-abc123"}`))

	got, err := c.OSUsers().WaitReady(context.Background(), idA)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Ready || got.Name != "tfacc-abc123" {
		t.Errorf("unexpected object: %+v", got)
	}
	if n := f.count("GET", "/osuser/read/"+idA); n != 3 {
		t.Errorf("%d reads, want 3: the object that reported ready is the one returned", n)
	}
}

func TestWaitReadyTimeoutNamesLastState(t *testing.T) {
	f, c, _ := newFake(t)
	f.on("GET", "/osuser/read/"+idA, ok(`{"id":"`+idA+`","state":"PENDING_CREATE","ready":false}`))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := c.OSUsers().WaitReady(ctx, idA)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline error", err)
	}
	if !strings.Contains(err.Error(), "PENDING_CREATE") {
		t.Errorf("err = %q, want it to name the last state", err)
	}
}

func TestWaitReadyStopsOnAFailedState(t *testing.T) {
	f, c, _ := newFake(t)
	f.on("GET", "/app/read/"+idA,
		ok(`{"id":"`+idA+`","state":"PENDING_INSTALL","ready":false}`),
		ok(`{"id":"`+idA+`","state":"INSTALL_FAILED","ready":false}`))
	_, err := c.Apps().WaitReady(context.Background(), idA)
	if !errors.Is(err, ErrFailed) || !strings.Contains(err.Error(), "INSTALL_FAILED") {
		t.Fatalf("err = %v, want ErrFailed naming the state", err)
	}
	if n := f.count("GET", ""); n != 2 {
		t.Errorf("%d reads, want the wait to stop at the failed state", n)
	}
}

func TestWaitReadyStopsOnNotFound(t *testing.T) {
	f, c, _ := newFake(t)
	f.on("GET", "/osuser/read/"+idA, reply{status: 404, body: `{"id":["Not found."]}`})
	if _, err := c.OSUsers().WaitReady(context.Background(), idA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestWaitDeleted(t *testing.T) {
	f, c, _ := newFake(t)
	f.on("GET", "/domain/read/"+idA,
		ok(`{"id":"`+idA+`","state":"PENDING_DELETE","ready":false}`),
		reply{status: 404, body: `{"id":["Not found."]}`})
	if err := c.Domains().WaitDeleted(context.Background(), idA); err != nil {
		t.Fatal(err)
	}
}

func TestWaitDeletedDoesNotTreatForbiddenAsGone(t *testing.T) {
	f, c, _ := newFake(t)
	f.on("GET", "/domain/read/"+idA, reply{status: 403, body: `{"detail":"No permission."}`})
	if err := c.Domains().WaitDeleted(context.Background(), idA); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestSafetyPrefix(t *testing.T) {
	setup := func(t *testing.T) (*fakeAPI, *Client) {
		f, c, _ := newFake(t)
		c.cfg.SafetyPrefix = "tfacc-"
		return f, c
	}
	posts := func(f *fakeAPI) int { return f.count("POST", "") }

	t.Run("create with the prefix passes", func(t *testing.T) {
		f, c := setup(t)
		f.on("POST", "/domain/create/", ok(`[{"id":"`+idA+`","name":"tfacc-a.example.com"}]`))
		if _, err := c.Domains().Create(context.Background(), DomainCreate{Name: "tfacc-a.example.com"}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("create without the prefix sends nothing", func(t *testing.T) {
		f, c := setup(t)
		_, err := c.Domains().Create(context.Background(), DomainCreate{Name: "www.example.com"})
		if !errors.Is(err, ErrSafetyPrefix) {
			t.Fatalf("err = %v, want ErrSafetyPrefix", err)
		}
		if n := f.count("", ""); n != 0 {
			t.Errorf("%d requests were sent", n)
		}
	})

	t.Run("an update without an id is refused", func(t *testing.T) {
		f, c := setup(t)
		if _, err := c.Sites().Update(context.Background(), SiteUpdate{Name: "tfacc-site"}); !errors.Is(err, ErrSafetyPrefix) {
			t.Errorf("err = %v, want ErrSafetyPrefix", err)
		}
		if n := f.count("", ""); n != 0 {
			t.Errorf("%d requests were sent", n)
		}
	})

	t.Run("a rename out of the prefix is refused", func(t *testing.T) {
		f, c := setup(t)
		f.on("GET", "/site/read/"+idA, ok(`{"id":"`+idA+`","name":"tfacc-site"}`))
		_, err := c.Sites().Update(context.Background(), SiteUpdate{ID: idA, Name: "production"})
		if !errors.Is(err, ErrSafetyPrefix) {
			t.Errorf("err = %v, want ErrSafetyPrefix", err)
		}
		if n := posts(f); n != 0 {
			t.Errorf("%d POST requests were sent", n)
		}
	})

	t.Run("update and delete look the name up first", func(t *testing.T) {
		f, c := setup(t)
		f.on("GET", "/app/read/"+idA, ok(`{"id":"`+idA+`","name":"production_app"}`))
		if _, err := c.Apps().Update(context.Background(), AppUpdate{ID: idA, Type: "STA"}); !errors.Is(err, ErrSafetyPrefix) {
			t.Errorf("update: err = %v, want ErrSafetyPrefix", err)
		}
		if err := c.Apps().Delete(context.Background(), idA); !errors.Is(err, ErrSafetyPrefix) {
			t.Errorf("delete: err = %v, want ErrSafetyPrefix", err)
		}
		if n := posts(f); n != 0 {
			t.Errorf("%d POST requests were sent", n)
		}
	})

	t.Run("a write is refused when the name cannot be read", func(t *testing.T) {
		f, c := setup(t)
		f.on("GET", "/app/read/"+idA, reply{status: 404, body: `{"id":["Not found."]}`})
		err := c.Apps().Delete(context.Background(), idA)
		if !errors.Is(err, ErrSafetyPrefix) || errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrSafetyPrefix that does not also match ErrNotFound", err)
		}
		if n := posts(f); n != 0 {
			t.Errorf("%d POST requests were sent", n)
		}
	})

	t.Run("a DNS record is judged by its domain", func(t *testing.T) {
		f, c := setup(t)
		f.on("GET", "/domain/read/"+idB, ok(`{"id":"`+idB+`","name":"www.example.com"}`))
		f.on("GET", "/dnsrecord/read/"+idA, ok(`{"id":"`+idA+`","domain":"`+idB+`","type":"A"}`))

		_, err := c.DNSRecords().Create(context.Background(), DNSRecordCreate{Domain: idB, Type: "A", Content: "192.0.2.1"})
		if !errors.Is(err, ErrSafetyPrefix) {
			t.Errorf("create: err = %v, want ErrSafetyPrefix", err)
		}
		if err := c.DNSRecords().Delete(context.Background(), idA); !errors.Is(err, ErrSafetyPrefix) {
			t.Errorf("delete: err = %v, want ErrSafetyPrefix", err)
		}
		if n := posts(f); n != 0 {
			t.Errorf("%d POST requests were sent", n)
		}

		f.on("GET", "/domain/read/"+idB, ok(`{"id":"`+idB+`","name":"tfacc-a.example.com"}`))
		f.on("POST", "/dnsrecord/delete/", ok(`{"ok":"Queued for deletion."}`))
		if err := c.DNSRecords().Delete(context.Background(), idA); err != nil {
			t.Errorf("delete under a test domain: %v", err)
		}
	})

	t.Run("an app is refused under a shell user that is neither prefixed nor the sandbox user", func(t *testing.T) {
		f, c := setup(t)
		f.on("GET", "/osuser/read/"+idB, ok(`{"id":"`+idB+`","name":"production"}`))
		_, err := c.Apps().Create(context.Background(), AppCreate{Name: "tfacc-app", OSUser: idB, Type: "STA"})
		if !errors.Is(err, ErrSafetyPrefix) {
			t.Errorf("err = %v, want ErrSafetyPrefix", err)
		}
		if n := posts(f); n != 0 {
			t.Errorf("%d POST requests were sent", n)
		}
	})

	t.Run("an app passes under the sandbox user and under a prefixed one", func(t *testing.T) {
		f, c := setup(t)
		c.cfg.SafetyOSUserID = idB
		f.on("GET", "/osuser/read/"+idC, ok(`{"id":"`+idC+`","name":"tfacc-user"}`))
		f.on("POST", "/app/create/", ok(`[{"id":"`+idA+`","name":"tfacc-app"}]`))
		for _, osuser := range []string{idB, idC} {
			if _, err := c.Apps().Create(context.Background(), AppCreate{Name: "tfacc-app", OSUser: osuser, Type: "STA"}); err != nil {
				t.Errorf("under %s: %v", osuser, err)
			}
		}
		if n := f.count("GET", "/osuser/read/"+idB); n != 0 {
			t.Errorf("the sandbox user was read %d times", n)
		}
	})

	t.Run("a create that leaves the name to the API is refused", func(t *testing.T) {
		f, c := setup(t)
		f.on("GET", "/domain/read/"+idB, ok(`{"id":"`+idB+`","name":"tfacc-a.example.com"}`))
		_, err := c.Sites().Create(context.Background(), SiteCreate{Domains: []string{idB}})
		if !errors.Is(err, ErrSafetyPrefix) {
			t.Errorf("err = %v, want ErrSafetyPrefix", err)
		}
		if n := f.count("", ""); n != 0 {
			t.Errorf("%d requests were sent", n)
		}
	})

	t.Run("a site's primary domain is a reference too, and each object is read once", func(t *testing.T) {
		f, c := setup(t)
		f.on("GET", "/domain/read/"+idB, ok(`{"id":"`+idB+`","name":"tfacc-a.example.com"}`))
		f.on("GET", "/domain/read/"+idC, ok(`{"id":"`+idC+`","name":"www.example.com"}`))
		f.on("GET", "/app/read/"+idB, ok(`{"id":"`+idB+`","name":"tfacc-app"}`))
		primary := idC
		_, err := c.Sites().Create(context.Background(), SiteCreate{
			Name: "tfacc-site", Domains: []string{idB}, PrimaryDomain: &primary,
			Routes: []Route{{App: idB, URI: "/"}, {App: idB, URI: "/x/"}},
		})
		if !errors.Is(err, ErrSafetyPrefix) {
			t.Errorf("err = %v, want ErrSafetyPrefix", err)
		}
		if n := posts(f); n != 0 {
			t.Errorf("%d POST requests were sent", n)
		}

		f, c = setup(t)
		f.on("GET", "/domain/read/"+idB, ok(`{"id":"`+idB+`","name":"tfacc-a.example.com"}`))
		f.on("GET", "/app/read/"+idB, ok(`{"id":"`+idB+`","name":"tfacc-app"}`))
		f.on("POST", "/site/create/", ok(`[{"id":"`+idA+`","name":"tfacc-site"}]`))
		primary = idB
		if _, err := c.Sites().Create(context.Background(), SiteCreate{
			Name: "tfacc-site", Domains: []string{idB}, PrimaryDomain: &primary,
			Routes: []Route{{App: idB, URI: "/"}, {App: idB, URI: "/x/"}},
		}); err != nil {
			t.Fatal(err)
		}
		if n := f.count("GET", "/app/read/"+idB); n != 1 {
			t.Errorf("the app was read %d times, want 1", n)
		}
		if n := f.count("GET", "/domain/read/"+idB); n != 1 {
			t.Errorf("the domain was read %d times, want 1", n)
		}
	})

	t.Run("the sandbox user itself is never written", func(t *testing.T) {
		f, c := setup(t)
		c.cfg.SafetyOSUserID = idB
		f.on("GET", "/osuser/read/"+idB, ok(`{"id":"`+idB+`","name":"sandbox"}`))
		if err := c.OSUsers().Delete(context.Background(), idB); !errors.Is(err, ErrSafetyPrefix) {
			t.Errorf("err = %v, want ErrSafetyPrefix", err)
		}
		if n := posts(f); n != 0 {
			t.Errorf("%d POST requests were sent", n)
		}
	})

	t.Run("a site is refused when a domain or a routed app lacks the prefix", func(t *testing.T) {
		f, c := setup(t)
		f.on("GET", "/site/read/"+idA, ok(`{"id":"`+idA+`","name":"tfacc-site"}`))
		f.on("GET", "/domain/read/"+idB, ok(`{"id":"`+idB+`","name":"tfacc-a.example.com"}`))
		f.on("GET", "/domain/read/"+idC, ok(`{"id":"`+idC+`","name":"www.example.com"}`))
		f.on("GET", "/app/read/"+idB, ok(`{"id":"`+idB+`","name":"tfacc-app"}`))
		f.on("GET", "/app/read/"+idC, ok(`{"id":"`+idC+`","name":"production_app"}`))
		f.on("POST", "/site/create/", ok(`[{"id":"`+idA+`","name":"tfacc-site"}]`))
		ctx := context.Background()

		good := SiteCreate{Name: "tfacc-site", Domains: []string{idB}, Routes: []Route{{App: idB, URI: "/"}}}
		if _, err := c.Sites().Create(ctx, good); err != nil {
			t.Fatalf("all references prefixed: %v", err)
		}
		before := posts(f)

		_, err := c.Sites().Create(ctx, SiteCreate{Name: "tfacc-site", Domains: []string{idB, idC}})
		if !errors.Is(err, ErrSafetyPrefix) {
			t.Errorf("foreign domain: err = %v, want ErrSafetyPrefix", err)
		}
		_, err = c.Sites().Update(ctx, SiteUpdate{ID: idA, Routes: []Route{{App: idB, URI: "/"}, {App: idC, URI: "/x"}}})
		if !errors.Is(err, ErrSafetyPrefix) {
			t.Errorf("foreign app: err = %v, want ErrSafetyPrefix", err)
		}
		if n := posts(f) - before; n != 0 {
			t.Errorf("%d POST requests were sent", n)
		}
	})
}

func TestReadOnlySendsNoWrite(t *testing.T) {
	f, c, _ := newFake(t)
	c.cfg.ReadOnly = true
	f.on("GET", "/domain/read/"+idA, reply{status: 200, body: `{"id":"` + idA + `","name":"example.com","state":"READY","ready":true}`})

	if _, err := c.Domains().Read(context.Background(), idA); err != nil {
		t.Fatalf("a read must still work: %v", err)
	}
	_, createErr := c.Domains().Create(context.Background(), DomainCreate{Name: "example.com"})
	_, updateErr := c.Domains().Update(context.Background(), DomainUpdate{ID: idA})
	deleteErr := c.Domains().Delete(context.Background(), idA)
	for verb, err := range map[string]error{"create": createErr, "update": updateErr, "delete": deleteErr} {
		if !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s: got %v, want ErrReadOnly", verb, err)
		}
	}
	for _, call := range f.calls {
		if call.method != http.MethodGet {
			t.Errorf("%s %s reached the API", call.method, call.path)
		}
	}
}

func TestLogsAreRedacted(t *testing.T) {
	f, c, logs := newFake(t)
	f.on("POST", "/osuser/create/", ok(`[{"id":"`+idA+`","name":"tfacc-abc123","default_password":"SECRET-ONE"}]`))
	f.on("GET", "/domain/read/"+idA, ok(`{"id":"`+idA+`","name":"tfacc-a.example.com","dkim_privkey":"SECRET-TWO","dkim_pubkey":"SECRET-THREE"}`))
	f.on("GET", "/app/read/"+idA, reply{status: 400, body: `{"token":"SECRET-FOUR"}`})

	ctx := context.Background()
	if _, err := c.OSUsers().Create(ctx, OSUserCreate{Server: idB, Name: "tfacc-abc123", Password: "SECRET-FIVE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Domains().Read(ctx, idA); err != nil {
		t.Fatal(err)
	}
	_, apiErr := c.Apps().Read(ctx, idA)

	all := strings.Join(*logs, "\n") + "\n" + apiErr.Error()
	for _, secret := range []string{testToken, "SECRET-ONE", "SECRET-TWO", "SECRET-THREE", "SECRET-FOUR", "SECRET-FIVE"} {
		if strings.Contains(all, secret) {
			t.Errorf("%q leaked into the log or an error", secret)
		}
	}
	if len(*logs) != 3 || !strings.Contains(all, "tfacc-abc123") {
		t.Errorf("expected 3 log records that keep non-secret fields, got:\n%s", all)
	}
}

func TestValidationErrorOnSecretFieldStaysReadable(t *testing.T) {
	f, c, _ := newFake(t)
	f.on("POST", "/osuser/update/",
		reply{status: 400, body: `[{"password":["Ensure this field has at least 10 characters."]}]`},
		reply{status: 400, body: `[{"password":["The value SECRET-SIX is too common."]}]`})

	_, err := c.OSUsers().Update(context.Background(), OSUserUpdate{ID: idA, Password: "SECRET-SIX"})
	if !strings.Contains(err.Error(), "at least 10 characters") {
		t.Errorf("err = %q, want the validation text", err)
	}
	_, err = c.OSUsers().Update(context.Background(), OSUserUpdate{ID: idA, Password: "SECRET-SIX"})
	if strings.Contains(err.Error(), "SECRET-SIX") {
		t.Errorf("err = %q echoes the password that was sent", err)
	}
}

func TestCancelledRequestTakesNoThrottleSlot(t *testing.T) {
	f, c, _ := newFake(t)
	c.minInterval = 200 * time.Millisecond
	f.on("GET", "/ip/list/", ok(`[]`))

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for range 5 {
		_, _ = c.IPs().List(cancelled)
	}
	start := time.Now()
	if _, err := c.IPs().List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Errorf("the live request waited %v behind cancelled ones", elapsed)
	}
}

func TestThrottleSpacesRequests(t *testing.T) {
	f, c, _ := newFake(t)
	c.minInterval = 20 * time.Millisecond
	f.on("GET", "/ip/list/", ok(`[]`))

	start := time.Now()
	for range 4 {
		if _, err := c.IPs().List(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Errorf("4 requests took %v, want at least 60ms", elapsed)
	}
}
