package provider

import (
	"cmp"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Placeholder ids. Nothing here comes from a real account.
const (
	serverID = "10000000-0000-0000-0000-000000000001"
	ipID     = "20000000-0000-0000-0000-000000000001"
	ip6ID    = "20000000-0000-0000-0000-000000000002"
	osuserID = "30000000-0000-0000-0000-000000000001"
	domainID = "40000000-0000-0000-0000-000000000001"
	appID    = "50000000-0000-0000-0000-000000000001"
	app2ID   = "50000000-0000-0000-0000-000000000002"
)

// fakeAccount is the answer to each GET path of a small made-up account.
var fakeAccount = map[string]string{
	"/server/list/": `{"web_servers":[{"id":"` + serverID + `","hostname":"opal1.opalstack.com","type":"web"}],
		"imap_servers":[],"smtp_servers":[]}`,
	"/server/read/" + serverID: `{"id":"` + serverID + `","hostname":"opal1.opalstack.com","type":"web"}`,
	"/ip/list/": `[{"id":"` + ipID + `","ip":"192.0.2.10","server":"` + serverID + `","type":"4","primary":true},
		{"id":"` + ip6ID + `","ip":"2001:db8::10","server":"` + serverID + `","type":"6","primary":true}]`,
	"/osuser/list/":            `[{"id":"` + osuserID + `","name":"sandbox","server":"` + serverID + `","state":"READY","ready":true}]`,
	"/osuser/read/" + osuserID: `{"id":"` + osuserID + `","name":"sandbox","server":"` + serverID + `","state":"READY","ready":true}`,
	"/domain/list/": `[{"id":"` + domainID + `","name":"example.com","delegated_dns_zone":false,"is_valid_hostname":true,
		"dkim_record":null,"dkim_pubkey":null,"dkim_privkey":"PRIVATE","state":"READY","ready":true}]`,
	"/app/list/": `[{"id":"` + appID + `","name":"site","osuser":"` + osuserID + `","osuser_name":"sandbox","server":"` + serverID + `",
		"type":"STA","port":null,"installer_url":null,"json":{"php_version":"83"},"state":"READY","ready":true},
		{"id":"` + app2ID + `","name":"twice","osuser":"` + osuserID + `","type":"STA","json":{}},
		{"id":"` + appID + `","name":"twice","osuser":"` + serverID + `","type":"STA","json":{}}]`,
}

// fakeProvider serves fakeAccount over TLS and returns the provider factories
// and a provider block that points at it. The test is skipped without a
// terraform binary, so the offline unit tests never try to download one.
func fakeProvider(t *testing.T) (map[string]func() (tfprotov6.ProviderServer, error), string) {
	t.Helper()
	// A version-manager shim is on PATH but answers only inside a directory that
	// pins a version, and the test framework runs terraform in a temporary one.
	bin := cmp.Or(os.Getenv("TF_ACC_TERRAFORM_PATH"), "terraform")
	probe := exec.Command(bin, "version")
	probe.Dir = t.TempDir()
	if err := probe.Run(); err != nil {
		t.Skipf("no usable terraform binary (%s): set TF_ACC_TERRAFORM_PATH, as make test does", bin)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := fakeAccount[r.URL.Path[len("/api/v1"):]]
		if !ok || r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"id":["Not found."]}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
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
	return factories, block
}

func TestDataSources(t *testing.T) {
	factories, block := fakeProvider(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: block + `
data "opalstack_osuser" "u" { name = "sandbox" }
data "opalstack_osuser" "by_id" { id = data.opalstack_osuser.u.id }
data "opalstack_server" "s" { id = data.opalstack_osuser.u.server_id }
data "opalstack_server" "by_hostname" { hostname = "opal1.opalstack.com" }
data "opalstack_ip" "v4" { server_id = data.opalstack_server.s.id }
data "opalstack_ip" "v6" {
  server_id = data.opalstack_server.s.id
  type      = "6"
}
data "opalstack_domain" "d" { name = "example.com" }
data "opalstack_app" "a" { name = "site" }
data "opalstack_app" "narrowed" {
  name      = "twice"
  osuser_id = data.opalstack_osuser.u.id
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.opalstack_osuser.u", "id", osuserID),
				resource.TestCheckResourceAttr("data.opalstack_osuser.by_id", "name", "sandbox"),
				resource.TestCheckResourceAttr("data.opalstack_server.s", "hostname", "opal1.opalstack.com"),
				resource.TestCheckResourceAttr("data.opalstack_server.by_hostname", "id", serverID),
				resource.TestCheckResourceAttr("data.opalstack_ip.v4", "ip", "192.0.2.10"),
				resource.TestCheckResourceAttr("data.opalstack_ip.v4", "primary", "true"),
				resource.TestCheckResourceAttr("data.opalstack_ip.v6", "id", ip6ID),
				resource.TestCheckResourceAttr("data.opalstack_domain.d", "id", domainID),
				resource.TestCheckResourceAttr("data.opalstack_domain.d", "is_valid_hostname", "true"),
				resource.TestCheckNoResourceAttr("data.opalstack_domain.d", "dkim_record"),
				resource.TestCheckNoResourceAttr("data.opalstack_domain.d", "dkim_privkey"),
				resource.TestCheckResourceAttr("data.opalstack_app.a", "id", appID),
				resource.TestCheckResourceAttr("data.opalstack_app.a", "osuser_name", "sandbox"),
				resource.TestCheckResourceAttr("data.opalstack_app.a", "json", `{"php_version":"83"}`),
				resource.TestCheckNoResourceAttr("data.opalstack_app.a", "port"),
				resource.TestCheckResourceAttr("data.opalstack_app.narrowed", "id", app2ID),
			),
		}},
	})
}

func TestDataSourceLookupErrors(t *testing.T) {
	factories, block := fakeProvider(t)
	for name, tc := range map[string]struct{ config, want string }{
		"no match":        {`data "opalstack_domain" "d" { name = "missing.example.com" }`, `No domain matches`},
		"several matches": {`data "opalstack_app" "a" { name = "twice" }`, `More than one app matches`},
		"both id and name": {`data "opalstack_osuser" "u" {
  id   = "` + osuserID + `"
  name = "sandbox"
}`, `(?s)Invalid Attribute Combination`},
		"neither":    {`data "opalstack_osuser" "u" {}`, `(?s)Missing Attribute Configuration`},
		"unknown id": {`data "opalstack_osuser" "u" { id = "` + appID + `" }`, `Cannot read the shell user`},
		"bad ip family": {`data "opalstack_ip" "i" {
  server_id = "` + serverID + `"
  type      = "5"
}`, `(?s)Invalid Attribute Value`},
	} {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories,
				Steps:                    []resource.TestStep{{Config: block + tc.config, ExpectError: regexp.MustCompile(tc.want)}},
			})
		})
	}
}
