package provider

import (
	"regexp"
	"testing"

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
	app3ID   = "50000000-0000-0000-0000-000000000003"
	siteID   = "60000000-0000-0000-0000-000000000001"
)

// seedAccount fills the fake with a small made-up account.
func seedAccount(f *fakeAPI) {
	f.seed("server", obj{"id": serverID, "hostname": "opal1.opalstack.com", "type": "web"})
	f.seed("ip", obj{"id": ipID, "ip": "192.0.2.10", "server": serverID, "type": "4", "primary": true})
	f.seed("ip", obj{"id": ip6ID, "ip": "2001:db8::10", "server": serverID, "type": "6", "primary": true})
	f.seed("osuser", obj{"id": osuserID, "name": "sandbox", "server": serverID})
	f.seed("domain", obj{"id": domainID, "name": "example.com"})
	f.seed("app", obj{"id": appID, "name": "site", "osuser": osuserID, "osuser_name": "sandbox", "server": serverID,
		"type": "STA", "port": nil, "installer_url": nil, "json": obj{"php_version": "83"}})
	f.seed("app", obj{"id": app2ID, "name": "twice", "osuser": osuserID, "type": "STA", "json": obj{}})
	f.seed("app", obj{"id": app3ID, "name": "twice", "osuser": serverID, "type": "STA", "json": obj{}})
	f.seed("site", obj{"id": siteID, "name": "www", "server": serverID, "ip4": ipID, "domains": []any{domainID},
		"routes": []any{obj{"id": "70000000-0000-0000-0000-000000000001", "app": appID, "uri": "/"}}, "generate_le": true})
}

func TestDataSources(t *testing.T) {
	f, factories, block := fakeProvider(t)
	seedAccount(f)
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
data "opalstack_site" "w" { name = "www" }
data "opalstack_site" "by_id" { id = data.opalstack_site.w.id }
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
				resource.TestCheckResourceAttr("data.opalstack_site.w", "id", siteID),
				resource.TestCheckTypeSetElemAttr("data.opalstack_site.w", "domain_ids.*", domainID),
				resource.TestCheckTypeSetElemNestedAttrs("data.opalstack_site.w", "routes.*", map[string]string{"app_id": appID, "uri": "/"}),
				resource.TestCheckResourceAttr("data.opalstack_site.w", "ip4_id", ipID),
				resource.TestCheckNoResourceAttr("data.opalstack_site.w", "cert_id"),
				resource.TestCheckResourceAttr("data.opalstack_site.w", "generate_le", "true"),
				resource.TestCheckResourceAttr("data.opalstack_site.by_id", "name", "www"),
			),
		}},
	})
}

func TestDataSourceLookupErrors(t *testing.T) {
	f, factories, block := fakeProvider(t)
	seedAccount(f)
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
