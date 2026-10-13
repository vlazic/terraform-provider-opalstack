package provider

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"golang.org/x/crypto/ssh"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

// The TestFinding tests are probes, not regression tests: each asks the real
// API a question the spec does not answer and logs the answer on a FINDING
// line, which is then written up in API_FINDINGS.md. They pass whatever the
// answer is, and fail only when the probe itself could not run. They are not
// part of `make testacc`; run them with `make testacc RUN=^TestFinding`.
//
// They write through the client with the safety prefix set, to objects named
// like every other test object, so the sweeper covers what they leave behind.

type probe struct {
	t      *testing.T
	c      *client.Client
	ctx    context.Context
	base   string // the base domain for tests
	osuser string // id of the sandbox shell user
	mask   *strings.Replacer
	// answered is the redacted body of the last answer to a create or an update.
	answered string
}

func newProbe(t *testing.T) *probe {
	t.Helper()
	if os.Getenv(resource.EnvTfAcc) == "" {
		t.Skip("a probe of the real API: set TF_ACC=1")
	}
	p := &probe{t: t, base: accEnv(t, "OPALSTACK_TEST_DOMAIN_BASE"), osuser: accEnv(t, "OPALSTACK_TEST_OSUSER_ID")}
	c, err := client.New(client.Config{
		Token: accEnv(t, tokenEnvVar), SafetyPrefix: accPrefix, SafetyOSUserID: p.osuser,
		Log: func(_ context.Context, _ string, fields map[string]any) {
			if method, _ := fields["method"].(string); method == "POST" {
				p.answered, _ = fields["response"].(string)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.c = c
	// Nothing of the account is to be read off a FINDING line.
	p.mask = strings.NewReplacer(p.base, "<base>", accEnv(t, "OPALSTACK_TEST_OSUSER"), "<sandbox>")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	t.Cleanup(cancel)
	p.ctx = ctx
	return p
}

var uuidInText = regexp.MustCompile(`[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}`)

func (p *probe) find(format string, args ...any) {
	p.t.Helper()
	p.t.Log("FINDING " + uuidInText.ReplaceAllString(p.mask.Replace(fmt.Sprintf(format, args...)), "<uuid>"))
}

// answer describes how the API took a request: accepted, or the status and body.
func answer(err error) string {
	var apiErr *client.APIError
	switch {
	case err == nil:
		return "accepted"
	case errors.As(err, &apiErr):
		return fmt.Sprintf("HTTP %d %s", apiErr.Status, apiErr.Body)
	}
	return "error: " + err.Error()
}

// later deletes an object when the test ends. Cleanups run last in, first out,
// so a child made after its parent goes first.
func later[T any](p *probe, k interface {
	deleter
	Read(context.Context, string) (*T, error)
}, id string) {
	p.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
		// Read first: with the safety prefix, a delete of an object that is gone is a refusal.
		_, err := k.Read(ctx, id)
		if err == nil {
			err = k.Delete(ctx, id)
		}
		if err == nil {
			err = k.WaitDeleted(ctx, id)
		}
		if err != nil && !errors.Is(err, client.ErrNotFound) {
			p.t.Errorf("cleanup: %s", p.mask.Replace(err.Error()))
		}
	})
}

func (p *probe) must(err error) {
	p.t.Helper()
	if err != nil {
		p.t.Fatal(p.mask.Replace(err.Error()))
	}
}

func (p *probe) domain() *client.Domain {
	p.t.Helper()
	d, err := p.c.Domains().Create(p.ctx, client.DomainCreate{Name: accName() + "." + p.base})
	p.must(err)
	later(p, p.c.Domains(), d.ID)
	d, err = p.c.Domains().WaitReady(p.ctx, d.ID)
	p.must(err)
	return d
}

// app makes a static app, the simplest kind, under a shell user.
func (p *probe) app(osuser string) *client.App {
	p.t.Helper()
	a, err := p.c.Apps().Create(p.ctx, client.AppCreate{Name: accName(), OSUser: osuser, Type: "STA"})
	p.must(err)
	later(p, p.c.Apps(), a.ID)
	a, err = p.c.Apps().WaitReady(p.ctx, a.ID)
	p.must(err)
	return a
}

// ip4 is the primary IPv4 address of the sandbox shell user's server.
func (p *probe) ip4() *string {
	p.t.Helper()
	user, err := p.c.OSUsers().Read(p.ctx, p.osuser)
	p.must(err)
	ips, err := p.c.IPs().List(p.ctx)
	p.must(err)
	for _, ip := range ips {
		if ip.Server == user.Server && ip.Type == "4" && ip.Primary {
			return &ip.ID
		}
	}
	p.t.Fatal("the sandbox server has no primary IPv4 address")
	return nil
}

func (p *probe) site(req client.SiteCreate) *client.Site {
	p.t.Helper()
	req.Name, req.IP4 = accName(), p.ip4()
	s, err := p.c.Sites().Create(p.ctx, req)
	p.must(err)
	later(p, p.c.Sites(), s.ID)
	s, err = p.c.Sites().WaitReady(p.ctx, s.ID)
	p.must(err)
	return s
}

// settings renders an app's json for a FINDING line, without anything secret.
func settings(json map[string]any) string {
	out := map[string]any{}
	for key, value := range json {
		switch s, isString := value.(string); {
		case client.IsSecretKey(key):
			out[key] = "REDACTED"
		case isString && len(s) > 40:
			out[key] = s[:40] + "..."
		default:
			out[key] = value
		}
	}
	return fmt.Sprintf("%v", out)
}

// What does a request the API refuses look like?
func TestFindingErrors(t *testing.T) {
	p := newProbe(t)
	d, a := p.domain(), p.app(p.osuser)

	if dup, err := p.c.Domains().Create(p.ctx, client.DomainCreate{Name: d.Name}); err == nil {
		later(p, p.c.Domains(), dup.ID)
		p.find("duplicate domain name: accepted, same id=%v", dup.ID == d.ID)
	} else {
		p.find("duplicate domain name: %s", answer(err))
	}
	if dup, err := p.c.Apps().Create(p.ctx, client.AppCreate{Name: a.Name, OSUser: p.osuser, Type: "STA"}); err == nil {
		later(p, p.c.Apps(), dup.ID)
		p.find("duplicate app name under one shell user: accepted, same id=%v", dup.ID == a.ID)
	} else {
		p.find("duplicate app name under one shell user: %s", answer(err))
	}
	if bad, err := p.c.Domains().Create(p.ctx, client.DomainCreate{Name: accName() + "_bad!." + p.base}); err == nil {
		later(p, p.c.Domains(), bad.ID)
		p.find("domain name with invalid characters: accepted, is_valid_hostname=%v", bad.IsValidHostname)
	} else {
		p.find("domain name with invalid characters: %s", answer(err))
	}
	if bad, err := p.c.Apps().Create(p.ctx, client.AppCreate{Name: accName(), OSUser: p.osuser, Type: "XXX"}); err == nil {
		later(p, p.c.Apps(), bad.ID)
		p.find("app type outside the enum: accepted as %s", bad.Type)
	} else {
		p.find("app type outside the enum: %s", answer(err))
	}
	if bad, err := p.c.DNSRecords().Create(p.ctx, client.DNSRecordCreate{Domain: d.ID, Type: "XXX", Content: "x"}); err == nil {
		later(p, p.c.DNSRecords(), bad.ID)
		p.find("dns record type outside the enum: accepted as %s", bad.Type)
	} else {
		p.find("dns record type outside the enum: %s", answer(err))
	}
	if bad, err := p.c.DNSRecords().Create(p.ctx, client.DNSRecordCreate{Domain: d.ID, Type: "A", Content: "not an address"}); err == nil {
		later(p, p.c.DNSRecords(), bad.ID)
		p.find("A record with content that is no address: accepted")
	} else {
		p.find("A record with content that is no address: %s", answer(err))
	}
	missing := "11111111-1111-1111-1111-111111111111"
	req := client.SiteCreate{Name: accName(), IP4: &missing, Domains: []string{d.ID}, Routes: []client.Route{{App: a.ID, URI: "/"}}}
	if bad, err := p.c.Sites().Create(p.ctx, req); err == nil {
		later(p, p.c.Sites(), bad.ID)
		p.find("site with an ip4 id that does not exist: accepted")
	} else {
		p.find("site with an ip4 id that does not exist: %s", answer(err))
	}
}

// Can a name be used again right after its delete is queued, and after it is gone?
func TestFindingNameReuse(t *testing.T) {
	p := newProbe(t)

	d := p.domain()
	p.must(p.c.Domains().Delete(p.ctx, d.ID))
	if again, err := p.c.Domains().Create(p.ctx, client.DomainCreate{Name: d.Name}); err == nil {
		later(p, p.c.Domains(), again.ID)
		p.find("domain name reused while its delete is queued: accepted, same id=%v state=%s", again.ID == d.ID, again.State)
	} else {
		p.find("domain name reused while its delete is queued: %s", answer(err))
	}
	p.must(p.c.Domains().WaitDeleted(p.ctx, d.ID))

	a := p.app(p.osuser)
	p.must(p.c.Apps().Delete(p.ctx, a.ID))
	if again, err := p.c.Apps().Create(p.ctx, client.AppCreate{Name: a.Name, OSUser: p.osuser, Type: "STA"}); err == nil {
		later(p, p.c.Apps(), again.ID)
		p.find("app name reused while its delete is queued: accepted, same id=%v state=%s", again.ID == a.ID, again.State)
		ready, err := p.c.Apps().WaitReady(p.ctx, again.ID)
		p.find("the app made under the reused name: ready=%v %s", ready != nil, answer(err))
	} else {
		p.find("app name reused while its delete is queued: %s", answer(err))
	}
	p.must(p.c.Apps().WaitDeleted(p.ctx, a.ID))

	b := p.app(p.osuser)
	p.must(p.c.Apps().Delete(p.ctx, b.ID))
	p.must(p.c.Apps().WaitDeleted(p.ctx, b.ID))
	if again, err := p.c.Apps().Create(p.ctx, client.AppCreate{Name: b.Name, OSUser: p.osuser, Type: "STA"}); err == nil {
		later(p, p.c.Apps(), again.ID)
		p.find("app name reused once the first app is gone: accepted")
	} else {
		p.find("app name reused once the first app is gone: %s", answer(err))
	}
}

// What happens to a delete of an object something else still refers to?
func TestFindingParents(t *testing.T) {
	p := newProbe(t)
	// outcome waits for a delete the API accepted and says what became of it.
	outcome := func(k deleter, id string) string {
		ctx, cancel := context.WithTimeout(p.ctx, 2*time.Minute)
		defer cancel()
		if err := k.WaitDeleted(ctx, id); err != nil {
			return "not gone after 2 minutes: " + err.Error()
		}
		return "gone"
	}
	siteNow := func(id string) string {
		s, err := p.c.Sites().Read(p.ctx, id)
		if err != nil {
			return answer(err)
		}
		return fmt.Sprintf("state=%s domains=%d routes=%d", s.State, len(s.Domains), len(s.Routes))
	}

	d1, a1 := p.domain(), p.app(p.osuser)
	s1 := p.site(client.SiteCreate{Domains: []string{d1.ID}, Routes: []client.Route{{App: a1.ID, URI: "/"}}})
	if err := p.c.Apps().Delete(p.ctx, a1.ID); err != nil {
		p.find("delete an app a site routes to: %s", answer(err))
	} else {
		p.find("delete an app a site routes to: accepted, app %s, site then %s", outcome(p.c.Apps(), a1.ID), siteNow(s1.ID))
	}

	d2, a2 := p.domain(), p.app(p.osuser)
	s2 := p.site(client.SiteCreate{Domains: []string{d2.ID}, Routes: []client.Route{{App: a2.ID, URI: "/"}}})
	if err := p.c.Domains().Delete(p.ctx, d2.ID); err != nil {
		p.find("delete a domain a site serves: %s", answer(err))
	} else {
		p.find("delete a domain a site serves: accepted, domain %s, site then %s", outcome(p.c.Domains(), d2.ID), siteNow(s2.ID))
	}

}

func TestFindingShellUserWithApp(t *testing.T) {
	p := newProbe(t)
	sandbox, err := p.c.OSUsers().Read(p.ctx, p.osuser)
	p.must(err)
	u, err := p.c.OSUsers().Create(p.ctx, client.OSUserCreate{Server: sandbox.Server, Name: accName()})
	p.must(err)
	later(p, p.c.OSUsers(), u.ID)
	_, err = p.c.OSUsers().WaitReady(p.ctx, u.ID)
	p.must(err)
	a := p.app(u.ID)
	if err := p.c.OSUsers().Delete(p.ctx, u.ID); err != nil {
		p.find("delete a shell user that has an app: %s", answer(err))
		return
	}
	waitErr := p.c.OSUsers().WaitDeleted(p.ctx, u.ID)
	_, readErr := p.c.Apps().Read(p.ctx, a.ID)
	p.find("delete a shell user that has an app: accepted; user gone: %s; a read of its app afterwards: %s", answer(waitErr), answer(readErr))
}

// What does a create that cannot succeed look like? The state enum has no failure value.
func TestFindingFailedCreate(t *testing.T) {
	p := newProbe(t)
	for label, url := range map[string]string{
		"an installer_url that answers 404":  "https://example.com/tfacc-no-such-installer.sh",
		"an installer_url on no host at all": "https://tfacc-no-such-host.invalid/install.sh",
	} {
		a, err := p.c.Apps().Create(p.ctx, client.AppCreate{Name: accName(), OSUser: p.osuser, Type: "CUS", InstallerURL: &url})
		if err != nil {
			p.find("app with %s: %s", label, answer(err))
			continue
		}
		later(p, p.c.Apps(), a.ID)
		// Until it is ready or failed. A failed install took some ten minutes to show.
		seen, last := []string{a.State}, a.State
		for start := time.Now(); time.Since(start) < 15*time.Minute; time.Sleep(20 * time.Second) {
			now, err := p.c.Apps().Read(p.ctx, a.ID)
			if err != nil {
				seen = append(seen, answer(err))
				break
			}
			if now.State != last {
				last = now.State
				seen = append(seen, fmt.Sprintf("%s after %s", now.State, time.Since(start).Round(time.Second)))
			}
			if now.Ready || strings.HasSuffix(now.State, "_FAILED") {
				break
			}
		}
		p.find("app with %s: accepted; states: %s", label, strings.Join(seen, " -> "))
	}
}

// Which settings does each app type get, and what does a bad one answer?
func TestFindingAppJSON(t *testing.T) {
	p := newProbe(t)
	var apa *client.App
	for _, appType := range []string{"STA", "NPF", "APA", "CUS", "SLS", "SLP", "SLN", "SVN", "DAV"} {
		a, err := p.c.Apps().Create(p.ctx, client.AppCreate{Name: accName(), OSUser: p.osuser, Type: appType})
		if err != nil {
			p.find("%s created without json: %s", appType, answer(err))
			continue
		}
		later(p, p.c.Apps(), a.ID)
		ready, err := p.c.Apps().WaitReady(p.ctx, a.ID)
		if err != nil {
			p.find("%s created without json: accepted, then %s", appType, answer(err))
			continue
		}
		p.find("%s created without json: port=%v json=%s", appType, ready.Port != nil, settings(ready.JSON))
		if appType == "APA" {
			apa = ready
		}
	}
	if apa == nil {
		t.Fatal("no APA app to try settings on")
	}
	for label, json := range map[string]map[string]any{
		"an unknown key":                    {"tfacc_no_such_setting": 1},
		"php_version as a word":             {"php_version": "banana"},
		"php_version that does not exist":   {"php_version": 12},
		"php_version as the string \"83\"":  {"php_version": "83"},
		"an empty object":                   {},
		"a setting of another type's (CUS)": {"external_port_access": true},
	} {
		_, err := p.c.Apps().Update(p.ctx, client.AppUpdate{ID: apa.ID, JSON: json})
		if err != nil {
			p.find("APA updated with %s: %s", label, answer(err))
			continue
		}
		now, err := p.c.Apps().WaitReady(p.ctx, apa.ID)
		p.must(err)
		p.find("APA updated with %s: accepted, json now %s", label, settings(now.JSON))
	}
}

// What does generate_le do for a domain that resolves nowhere?
func TestFindingGenerateLE(t *testing.T) {
	p := newProbe(t)
	d, a, yes := p.domain(), p.app(p.osuser), true
	s := p.site(client.SiteCreate{Domains: []string{d.ID}, Routes: []client.Route{{App: a.ID, URI: "/"}}, GenerateLE: &yes})
	p.find("site created with generate_le: generate_le=%v cert=%v", s.GenerateLE, s.Cert != nil)

	seen := []string{}
	for start := time.Now(); time.Since(start) < 2*time.Minute; time.Sleep(15 * time.Second) {
		now, err := p.c.Sites().Read(p.ctx, s.ID)
		p.must(err)
		state := fmt.Sprintf("%s generate_le=%v cert=%v", now.State, now.GenerateLE, now.Cert != nil)
		if len(seen) == 0 || !strings.HasPrefix(seen[len(seen)-1], state) {
			seen = append(seen, fmt.Sprintf("%s after %s", state, time.Since(start).Round(time.Second)))
		}
	}
	p.find("the same site over 2 minutes: %s", strings.Join(seen, " -> "))

	records, err := p.c.DNSRecords().List(p.ctx)
	p.must(err)
	var types []string
	for _, r := range records {
		if r.Domain == d.ID {
			types = append(types, r.Type)
		}
	}
	p.find("dns records of its domain: %v", types)

	no := false
	_, err = p.c.Sites().Update(p.ctx, client.SiteUpdate{ID: s.ID, GenerateLE: &no})
	p.find("generate_le turned off again: %s", answer(err))
	if err == nil {
		now, err := p.c.Sites().WaitReady(p.ctx, s.ID)
		p.must(err)
		p.find("after turning it off: generate_le=%v cert=%v", now.GenerateLE, now.Cert != nil)
	}
}

// Is a delete honoured when it arrives while the create is still pending?
func TestFindingDeleteWhilePending(t *testing.T) {
	p := newProbe(t)
	watch := func(what string, state func() (string, error)) {
		seen := []string{}
		for start := time.Now(); time.Since(start) < 90*time.Second; time.Sleep(5 * time.Second) {
			now, err := state()
			if err != nil {
				now = answer(err)
			}
			if len(seen) == 0 || !strings.HasPrefix(seen[len(seen)-1], now+" ") {
				seen = append(seen, fmt.Sprintf("%s after %s", now, time.Since(start).Round(time.Second)))
			}
			if err != nil {
				break
			}
		}
		p.find("%s deleted while PENDING_CREATE, then: %s", what, strings.Join(seen, " -> "))
	}

	a, err := p.c.Apps().Create(p.ctx, client.AppCreate{Name: accName(), OSUser: p.osuser, Type: "STA"})
	p.must(err)
	later(p, p.c.Apps(), a.ID)
	p.find("delete of an app in state %s: %s", a.State, answer(p.c.Apps().Delete(p.ctx, a.ID)))
	watch("app", func() (string, error) {
		now, err := p.c.Apps().Read(p.ctx, a.ID)
		if err != nil {
			return "", err
		}
		return now.State, nil
	})

	d, err := p.c.Domains().Create(p.ctx, client.DomainCreate{Name: accName() + "." + p.base})
	p.must(err)
	later(p, p.c.Domains(), d.ID)
	p.find("delete of a domain in state %s: %s", d.State, answer(p.c.Domains().Delete(p.ctx, d.ID)))
	watch("domain", func() (string, error) {
		now, err := p.c.Domains().Read(p.ctx, d.ID)
		if err != nil {
			return "", err
		}
		return now.State, nil
	})
}

// took describes how a create or update went, with the redacted answer when it
// was accepted: the spec does not say which fields come back.
func (p *probe) took(err error) string {
	if err != nil {
		return answer(err)
	}
	// Masked before it is cut: half a UUID would not be recognised as one.
	body := uuidInText.ReplaceAllString(p.mask.Replace(p.answered), "<uuid>")
	if len(body) > 700 {
		body = body[:700] + "..."
	}
	return "accepted " + body
}

// What do database users and databases answer? MariaDB and PostgreSQL share a shape.
func TestFindingMaria(t *testing.T) {
	p := newProbe(t)
	probeDatabases(p, p.c.MariaUsers(), p.c.MariaDBs())
}

func TestFindingPsql(t *testing.T) {
	p := newProbe(t)
	probeDatabases(p, p.c.PsqlUsers(), p.c.PsqlDBs())
}

func probeDatabases(p *probe, users client.DBUserKind, dbs client.DBKind) {
	sandbox, err := p.c.OSUsers().Read(p.ctx, p.osuser)
	p.must(err)

	plain, err := users.Create(p.ctx, client.DBUserCreate{Server: sandbox.Server, Name: accName()})
	p.find("user created without a password: %s", p.took(err))
	p.must(err)
	later(p, users, plain.ID)
	_, err = users.WaitReady(p.ctx, plain.ID)
	p.must(err)

	given, err := users.Create(p.ctx, client.DBUserCreate{Server: sandbox.Server, Name: accName(), Password: accPassword()})
	p.find("user created with a password: %s", p.took(err))
	p.must(err)
	later(p, users, given.ID)
	_, err = users.WaitReady(p.ctx, given.ID)
	p.must(err)

	_, err = users.Update(p.ctx, client.DBUserUpdate{ID: given.ID, Password: accPassword()})
	p.find("user password changed: %s", p.took(err))
	if err == nil {
		_, err = users.WaitReady(p.ctx, given.ID)
		p.must(err)
	}

	db, err := dbs.Create(p.ctx, client.DBCreate{Name: accName(), Server: sandbox.Server, Users: []string{plain.ID}, ReadOnlyUsers: []string{given.ID}})
	p.find("database created with one user of each kind: %s", p.took(err))
	p.must(err)
	later(p, dbs, db.ID)
	_, err = dbs.WaitReady(p.ctx, db.ID)
	p.must(err)

	_, err = dbs.Update(p.ctx, client.DBUpdate{ID: db.ID, Users: []string{plain.ID, given.ID}, ReadOnlyUsers: []string{}})
	p.find("database users changed, read-only list emptied: %s", p.took(err))
	if err == nil {
		_, err = dbs.WaitReady(p.ctx, db.ID)
		p.must(err)
	}
	if dup, err := dbs.Create(p.ctx, client.DBCreate{Name: db.Name, Server: sandbox.Server}); err == nil {
		later(p, dbs, dup.ID)
		p.find("second database of the same name: accepted")
	} else {
		p.find("second database of the same name: %s", answer(err))
	}
	p.find("delete a user a database still names: %s", answer(users.Delete(p.ctx, plain.ID)))
}

// What do mailboxes and addresses answer?
func TestFindingMail(t *testing.T) {
	p := newProbe(t)
	servers, err := p.c.ListServers(p.ctx)
	p.must(err)
	if len(servers.IMAP) == 0 {
		t.Fatal("the account has no IMAP server")
	}
	p.find("the account has %d IMAP and %d SMTP servers", len(servers.IMAP), len(servers.SMTP))
	d := p.domain()

	box, err := p.c.MailUsers().Create(p.ctx, client.MailUserCreate{IMAPServer: servers.IMAP[0].ID, Name: accName()})
	p.find("mailbox created without a password: %s", p.took(err))
	p.must(err)
	later(p, p.c.MailUsers(), box.ID)
	_, err = p.c.MailUsers().WaitReady(p.ctx, box.ID)
	p.must(err)
	_, err = p.c.MailUsers().Update(p.ctx, client.MailUserUpdate{ID: box.ID, Password: accPassword()})
	p.find("mailbox password changed: %s", p.took(err))
	if err == nil {
		_, err = p.c.MailUsers().WaitReady(p.ctx, box.ID)
		p.must(err)
	}

	source := accName() + "@" + d.Name
	addr, err := p.c.Addresses().Create(p.ctx, client.AddressCreate{Source: source, Destinations: []string{box.ID}, Forwards: []string{}})
	p.find("address created with one mailbox: %s", p.took(err))
	p.must(err)
	later(p, p.c.Addresses(), addr.ID)
	_, err = p.c.Addresses().WaitReady(p.ctx, addr.ID)
	p.must(err)

	_, err = p.c.Addresses().Update(p.ctx, client.AddressUpdate{ID: addr.ID, Destinations: []string{}, Forwards: []string{"tfacc-forward@example.com"}})
	p.find("address changed to forward only: %s", p.took(err))
	if err == nil {
		_, err = p.c.Addresses().WaitReady(p.ctx, addr.ID)
		p.must(err)
	}
	_, err = p.c.Addresses().Update(p.ctx, client.AddressUpdate{ID: addr.ID, Destinations: []string{}, Forwards: []string{}})
	p.find("address with neither mailbox nor forward: %s", p.took(err))
	if err == nil {
		_, err = p.c.Addresses().WaitReady(p.ctx, addr.ID)
		p.must(err)
	}
	_, err = p.c.Addresses().Update(p.ctx, client.AddressUpdate{ID: addr.ID, Destinations: []string{box.ID}, Forwards: []string{}})
	p.must(err)
	_, err = p.c.Addresses().WaitReady(p.ctx, addr.ID)
	p.must(err)

	if dup, err := p.c.Addresses().Create(p.ctx, client.AddressCreate{Source: source, Destinations: []string{box.ID}, Forwards: []string{}}); err == nil {
		later(p, p.c.Addresses(), dup.ID)
		p.find("second address with the same source: accepted")
	} else {
		p.find("second address with the same source: %s", answer(err))
	}
	other, err := p.c.Addresses().Create(p.ctx, client.AddressCreate{Source: accName() + "@tfacc-no-such-domain.example.com", Destinations: []string{box.ID}, Forwards: []string{}})
	if err == nil {
		later(p, p.c.Addresses(), other.ID)
	}
	p.find("address on a domain the account does not have: %s", p.took(err))

	if err := p.c.MailUsers().Delete(p.ctx, box.ID); err != nil {
		p.find("delete a mailbox an address delivers to: %s", answer(err))
		return
	}
	waitErr := p.c.MailUsers().WaitDeleted(p.ctx, box.ID)
	now, readErr := p.c.Addresses().Read(p.ctx, addr.ID)
	left := answer(readErr)
	if readErr == nil {
		left = fmt.Sprintf("state=%s destinations=%d", now.State, len(now.Destinations))
	}
	p.find("delete a mailbox an address delivers to: accepted; mailbox gone: %s; the address afterwards: %s", answer(waitErr), left)
}

// What does a certificate answer, and what does a site do with one?
func TestFindingCert(t *testing.T) {
	p := newProbe(t)
	d, a := p.domain(), p.app(p.osuser)
	certPEM, keyPEM := accSelfSigned(t, d.Name)

	cert, err := p.c.Certs().Create(p.ctx, client.CertCreate{Name: accName(), Cert: certPEM, Key: keyPEM})
	body := p.took(err)
	if i := strings.Index(body, "-----BEGIN"); i >= 0 {
		body = body[:i] + "<pem>..."
	}
	p.find("certificate created: %s", body)
	p.must(err)
	later(p, p.c.Certs(), cert.ID)
	read, err := p.c.Certs().Read(p.ctx, cert.ID)
	p.must(err)
	p.find("certificate as read: exp_date set=%v sha256 set=%v intermediates null=%v; the answer to the create has a state: %v",
		read.ExpDate != "", read.SHA256 != "", read.Intermediates == nil, strings.Contains(p.answered, `"state"`))

	_, err = p.c.Certs().Create(p.ctx, client.CertCreate{Name: accName(), Cert: certPEM, Key: "not a key"})
	p.find("certificate with a key that is no key: %s", answer(err))
	if dup, err := p.c.Certs().Create(p.ctx, client.CertCreate{Name: cert.Name, Cert: certPEM, Key: keyPEM}); err == nil {
		later(p, p.c.Certs(), dup.ID)
		p.find("second certificate of the same name: accepted")
	} else {
		p.find("second certificate of the same name: %s", answer(err))
	}

	newCert, newKey := accSelfSigned(t, d.Name)
	_, err = p.c.Certs().Update(p.ctx, client.CertUpdate{ID: cert.ID, Cert: newCert, Key: newKey})
	p.find("certificate and key replaced in place: %s", answer(err))
	_, err = p.c.Certs().Update(p.ctx, client.CertUpdate{ID: cert.ID, Name: accName()})
	p.find("certificate renamed: %s", answer(err))

	s := p.site(client.SiteCreate{Domains: []string{d.ID}, Routes: []client.Route{{App: a.ID, URI: "/"}}, Cert: &cert.ID})
	p.find("site created with the certificate: cert set=%v generate_le=%v", s.Cert != nil && *s.Cert == cert.ID, s.GenerateLE)
	if err := p.c.Certs().Delete(p.ctx, cert.ID); err != nil {
		p.find("delete a certificate a site serves: %s", answer(err))
		return
	}
	waitErr := p.c.Certs().WaitDeleted(p.ctx, cert.ID)
	now, err := p.c.Sites().Read(p.ctx, s.ID)
	p.must(err)
	p.find("delete a certificate a site serves: accepted; certificate gone: %s; the site's cert afterwards set=%v", answer(waitErr), now.Cert != nil)
}

// What does a shell user's environment variable answer, and what may an update change?
func TestFindingOSVar(t *testing.T) {
	p := newProbe(t)
	vars := p.c.OSVars()
	describe := func(id string) string {
		v, err := vars.Read(p.ctx, id)
		if err != nil {
			return answer(err)
		}
		return fmt.Sprintf("state=%s ready=%t content=%q osusers=%d global=%t", v.State, v.Ready, v.Content, len(v.OSUsers), v.Global)
	}

	name := accName()
	v, err := vars.Create(p.ctx, client.OSVarCreate{Name: name, Content: "one", OSUsers: []string{p.osuser}})
	p.find("variable created for one shell user: %s", p.took(err))
	p.must(err)
	later(p, vars, v.ID)
	p.find("the variable right after the create: %s", describe(v.ID))
	_, err = vars.WaitReady(p.ctx, v.ID)
	p.find("waiting for the variable to be ready: %s; then %s", answer(err), describe(v.ID))
	p.must(err)

	content := "two words"
	_, err = vars.Update(p.ctx, client.OSVarUpdate{ID: v.ID, Content: &content})
	p.find("content changed, nothing else sent: %s", p.took(err))
	if err == nil {
		_, err = vars.WaitReady(p.ctx, v.ID)
		p.must(err)
		p.find("after the content change: %s", describe(v.ID))
	}
	none := []string{}
	_, err = vars.Update(p.ctx, client.OSVarUpdate{ID: v.ID, OSUsers: &none})
	p.find("shell users emptied: %s", p.took(err))
	if err == nil {
		_, err = vars.WaitReady(p.ctx, v.ID)
		p.must(err)
		p.find("after emptying the shell users: %s", describe(v.ID))
	}
	one := []string{p.osuser}
	_, err = vars.Update(p.ctx, client.OSVarUpdate{ID: v.ID, OSUsers: &one})
	p.find("shell user given back: %s", p.took(err))
	if err == nil {
		_, err = vars.WaitReady(p.ctx, v.ID)
		p.must(err)
	}

	if dup, err := vars.Create(p.ctx, client.OSVarCreate{Name: name, Content: "again", OSUsers: []string{p.osuser}}); err == nil {
		later(p, vars, dup.ID)
		p.find("second variable with the same name for the same shell user: accepted")
	} else {
		p.find("second variable with the same name for the same shell user: %s", answer(err))
	}
	if bare, err := vars.Create(p.ctx, client.OSVarCreate{Name: accName()}); err == nil {
		later(p, vars, bare.ID)
		p.find("variable with no content and no shell user: %s", p.took(nil))
	} else {
		p.find("variable with no content and no shell user: %s", answer(err))
	}
	yes := true
	_, err = vars.Create(p.ctx, client.OSVarCreate{Name: accName(), Content: "x", Global: &yes})
	p.find("a global variable with the safety prefix set: %s", answer(err))

	p.must(vars.Delete(p.ctx, v.ID))
	p.find("variable deleted: %s", answer(vars.WaitDeleted(p.ctx, v.ID)))
}

// What does a mailbox answer for its filter rules and its autoresponder?
func TestFindingMailFilter(t *testing.T) {
	p := newProbe(t)
	servers, err := p.c.ListServers(p.ctx)
	p.must(err)
	if len(servers.IMAP) == 0 {
		t.Fatal("the account has no IMAP server")
	}
	boxes := p.c.MailUsers()
	describe := func(id string) string {
		b, err := boxes.Read(p.ctx, id)
		if err != nil {
			return answer(err)
		}
		return fmt.Sprintf("procmailrc=%q enable=%t subject=%q message=%q noreply=%q",
			b.Procmailrc, b.AutoresponderEnable, b.AutoresponderSubject, b.AutoresponderMessage, b.AutoresponderNoreply)
	}
	settle := func(id string, err error) bool {
		if err != nil {
			return false
		}
		_, err = boxes.WaitReady(p.ctx, id)
		p.must(err)
		return true
	}

	plain, err := boxes.Create(p.ctx, client.MailUserCreate{IMAPServer: servers.IMAP[0].ID, Name: accName()})
	p.must(err)
	later(p, boxes, plain.ID)
	settle(plain.ID, nil)
	p.find("a mailbox created with none of the fields: %s", describe(plain.ID))

	rules, subject, message, on := "# tfacc\n", "tfacc subject", "tfacc message", true
	noreply := []string{"tfacc-noreply@example.com"}
	full, err := boxes.Create(p.ctx, client.MailUserCreate{IMAPServer: servers.IMAP[0].ID, Name: accName(), MailFilterChange: client.MailFilterChange{
		Procmailrc: &rules, AutoresponderEnable: &on, AutoresponderSubject: &subject, AutoresponderMessage: &message, AutoresponderNoreply: &noreply,
	}})
	p.find("a mailbox created with all of the fields: %s", p.took(err))
	if err == nil {
		later(p, boxes, full.ID)
		settle(full.ID, nil)
		p.find("that mailbox read back: %s", describe(full.ID))
	}

	_, err = boxes.Update(p.ctx, client.MailUserUpdate{ID: plain.ID, MailFilterChange: client.MailFilterChange{
		Procmailrc: &rules, AutoresponderEnable: &on, AutoresponderSubject: &subject, AutoresponderMessage: &message, AutoresponderNoreply: &noreply,
	}})
	p.find("all of the fields set by an update: %s", p.took(err))
	if settle(plain.ID, err) {
		p.find("after that update: %s", describe(plain.ID))
	}
	off := false
	_, err = boxes.Update(p.ctx, client.MailUserUpdate{ID: plain.ID, MailFilterChange: client.MailFilterChange{AutoresponderEnable: &off}})
	p.find("autoresponder switched off, nothing else sent: %s", p.took(err))
	if settle(plain.ID, err) {
		p.find("after switching it off: %s", describe(plain.ID))
	}
	empty, nobody := "", []string{}
	_, err = boxes.Update(p.ctx, client.MailUserUpdate{ID: plain.ID, MailFilterChange: client.MailFilterChange{
		Procmailrc: &empty, AutoresponderSubject: &empty, AutoresponderMessage: &empty, AutoresponderNoreply: &nobody,
	}})
	p.find("every text field set to empty: %s", p.took(err))
	if settle(plain.ID, err) {
		p.find("after emptying them: %s", describe(plain.ID))
	}
	_, err = boxes.Update(p.ctx, client.MailUserUpdate{ID: plain.ID, Password: accPassword()})
	p.find("a password change with no filter field: %s", answer(err))
	if settle(plain.ID, err) {
		p.find("after the password change: %s", describe(plain.ID))
	}
}

// Does a password set through osuser/update/ take effect? Answered by logging
// in over SSH as a test shell user. Host keys are not checked: the probe sends
// only a password it generated a moment ago, for a user it deletes afterwards.
func TestFindingOSUserPassword(t *testing.T) {
	p := newProbe(t)
	sandbox, err := p.c.OSUsers().Read(p.ctx, p.osuser)
	p.must(err)
	servers, err := p.c.ListServers(p.ctx)
	p.must(err)
	host := ""
	for _, s := range servers.Web {
		if s.ID == sandbox.Server {
			host = s.Hostname
		}
	}
	if host == "" {
		t.Fatal("the sandbox shell user's server is not among the web servers")
	}
	login := func(user, password string) string {
		conn, err := ssh.Dial("tcp", host+":22", &ssh.ClientConfig{
			User: user, Auth: []ssh.AuthMethod{ssh.Password(password)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 30 * time.Second,
		})
		if err != nil {
			if strings.Contains(err.Error(), "unable to authenticate") {
				return "refused"
			}
			return "could not connect: " + strings.ReplaceAll(err.Error(), host, "<server>")
		}
		_ = conn.Close()
		return "accepted"
	}

	users := p.c.OSUsers()
	u, err := users.Create(p.ctx, client.OSUserCreate{Server: sandbox.Server, Name: accName()})
	p.must(err)
	later(p, users, u.ID)
	first := u.DefaultPassword
	_, err = users.WaitReady(p.ctx, u.ID)
	p.must(err)
	p.find("a new shell user, login with the password the API chose: %s", login(u.Name, first))

	chosen := accPassword()
	_, err = users.Update(p.ctx, client.OSUserUpdate{ID: u.ID, Password: chosen})
	p.find("password set through update: %s", answer(err))
	p.must(err)
	_, err = users.WaitReady(p.ctx, u.ID)
	p.must(err)
	p.find("login with the password that was set: %s", login(u.Name, chosen))
	// One failed attempt only: more could get this address banned from the server.
	p.find("login with the password the API chose, after the change: %s", login(u.Name, first))

	onCreate := accPassword()
	w, err := users.Create(p.ctx, client.OSUserCreate{Server: sandbox.Server, Name: accName(), Password: onCreate})
	p.find("a shell user created with a password of its own: %s", p.took(err))
	if err == nil {
		later(p, users, w.ID)
		_, err = users.WaitReady(p.ctx, w.ID)
		p.must(err)
		p.find("login with the password given on create: %s", login(w.Name, onCreate))
	}
}

// Which json keys does each app type know? One update per type names every
// candidate with a word as its value. The refusal names the keys the type does
// not have, and the keys that want another kind of value. The apps are thrown
// away afterwards, so a request that is accepted does no harm.
func TestFindingAppJSONKeys(t *testing.T) {
	p := newProbe(t)
	sandbox, err := p.c.OSUsers().Read(p.ctx, p.osuser)
	p.must(err)
	candidates := []string{
		"php_version", "fpm_type", "fpm_max_children", "fpm_start_servers", "fpm_min_spare_servers",
		"fpm_max_spare_servers", "fpm_max_requests", "fpm_process_idle_timeout", "app_exec", "url_fopen",
		"sym_link_path", "auto_site_url", "add_https_security_headers", "external_port_access", "subroot",
		"db_name", "db_user", "db_type", "db_host", "app_user", "app_email", "app_name", "proxy_pass",
		"memory_limit", "upload_max_filesize", "post_max_size", "max_execution_time", "node_version",
		"python_version", "ruby_version",
	}
	wrong := map[string]any{}
	for _, key := range candidates {
		// A plain word: a nested value made the API answer 500.
		wrong[key] = "tfacc"
	}
	p.find("candidate keys sent: %s", strings.Join(candidates, " "))
	for _, appType := range []string{"STA", "NPF", "APA", "CUS", "SLS", "SLP", "SLN", "SVN", "DAV"} {
		create := client.AppCreate{Name: accName(), OSUser: p.osuser, Type: appType}
		if strings.HasPrefix(appType, "SL") {
			create.JSON = map[string]any{"sym_link_path": "/home/" + sandbox.Name + "/apps"}
		}
		a, err := p.c.Apps().Create(p.ctx, create)
		if err != nil {
			p.find("%s could not be created: %s", appType, answer(err))
			continue
		}
		later(p, p.c.Apps(), a.ID)
		ready, err := p.c.Apps().WaitReady(p.ctx, a.ID)
		if err != nil {
			p.find("%s created, then %s", appType, answer(err))
			continue
		}
		// sym_link_path is checked before the schema is: refused outright for a type
		// that has none, and wanted as a path by the three that do.
		asked := maps.Clone(wrong)
		delete(asked, "sym_link_path")
		if path, has := ready.JSON["sym_link_path"]; has {
			asked["sym_link_path"] = path
		}
		_, err = p.c.Apps().Update(p.ctx, client.AppUpdate{ID: a.ID, JSON: asked})
		p.find("%s (json after create %s) answers every candidate with: %s", appType, settings(ready.JSON), answer(err))
		if err == nil {
			_, err = p.c.Apps().WaitReady(p.ctx, a.ID)
			p.must(err)
		}
	}
}

// What does generate_le do for a domain that does resolve to the server?
func TestFindingLetsEncrypt(t *testing.T) {
	p := newProbe(t)
	sandbox, err := p.c.OSUsers().Read(p.ctx, p.osuser)
	p.must(err)
	ips, err := p.c.IPs().List(p.ctx)
	p.must(err)
	address := ""
	for _, ip := range ips {
		if ip.Server == sandbox.Server && ip.Type == "4" && ip.Primary {
			address = ip.IP
		}
	}
	if address == "" {
		t.Fatal("the sandbox server has no primary IPv4 address")
	}
	mask := func(s string) string { return strings.ReplaceAll(s, address, "<ip>") }

	d := p.domain()
	rec, err := p.c.DNSRecords().Create(p.ctx, client.DNSRecordCreate{Domain: d.ID, Type: "A", Content: address})
	p.find("an A record for the test domain, pointing at the server: %s", mask(p.took(err)))
	p.must(err)
	later(p, p.c.DNSRecords(), rec.ID)
	_, err = p.c.DNSRecords().WaitReady(p.ctx, rec.ID)
	p.must(err)

	resolved := "never within 5 minutes"
	for start := time.Now(); time.Since(start) < 5*time.Minute; time.Sleep(15 * time.Second) {
		if addrs, err := net.LookupHost(d.Name); err == nil && slices.Contains(addrs, address) {
			resolved = "after " + time.Since(start).Round(time.Second).String()
			break
		}
	}
	p.find("the test domain resolves to the server from here: %s", resolved)

	a, yes := p.app(p.osuser), true
	s := p.site(client.SiteCreate{Domains: []string{d.ID}, Routes: []client.Route{{App: a.ID, URI: "/"}}, GenerateLE: &yes})
	p.find("site created with generate_le: generate_le=%v cert=%v", s.GenerateLE, s.Cert != nil)

	seen, certID := []string{}, ""
	for start := time.Now(); time.Since(start) < 12*time.Minute && certID == ""; time.Sleep(20 * time.Second) {
		now, err := p.c.Sites().Read(p.ctx, s.ID)
		p.must(err)
		if now.Cert != nil {
			certID = *now.Cert
		}
		state := fmt.Sprintf("%s generate_le=%v cert=%v", now.State, now.GenerateLE, now.Cert != nil)
		if len(seen) == 0 || !strings.HasPrefix(seen[len(seen)-1], state) {
			seen = append(seen, fmt.Sprintf("%s after %s", state, time.Since(start).Round(time.Second)))
		}
	}
	p.find("the site over up to 12 minutes: %s", strings.Join(seen, " -> "))
	if certID == "" {
		return
	}
	later(p, p.c.Certs(), certID)
	c, err := p.c.Certs().Read(p.ctx, certID)
	p.must(err)
	p.find("the certificate the site got: name=%q expires=%s has_intermediates=%v name_has_prefix=%v",
		c.Name, c.ExpDate, c.Intermediates != nil && *c.Intermediates != "", strings.HasPrefix(c.Name, accPrefix))

	no := false
	_, err = p.c.Sites().Update(p.ctx, client.SiteUpdate{ID: s.ID, GenerateLE: &no})
	p.find("generate_le turned off again: %s", answer(err))
	if err == nil {
		now, err := p.c.Sites().WaitReady(p.ctx, s.ID)
		p.must(err)
		_, readErr := p.c.Certs().Read(p.ctx, certID)
		p.find("after turning it off: generate_le=%v the site still names a cert=%v, the certificate object: %s",
			now.GenerateLE, now.Cert != nil, answer(readErr))
	}
}
