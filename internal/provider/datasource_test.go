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
	recordID = "80000000-0000-0000-0000-000000000001"
	certID   = "90000000-0000-0000-0000-000000000001"
	cert2ID  = "90000000-0000-0000-0000-000000000002"
	cert3ID  = "90000000-0000-0000-0000-000000000003"
	imapID   = "a0000000-0000-0000-0000-000000000001"
	boxID    = "a0000000-0000-0000-0000-000000000002"
	addrID   = "b0000000-0000-0000-0000-000000000001"
	mariaUID = "c0000000-0000-0000-0000-000000000001"
	mariaID  = "c0000000-0000-0000-0000-000000000002"
	psqlUID  = "d0000000-0000-0000-0000-000000000001"
	psqlID   = "d0000000-0000-0000-0000-000000000002"
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
	f.seed("dnsrecord", obj{"id": recordID, "domain": domainID, "type": "MX", "content": "mail.example.com", "priority": 20})
	f.seed("cert", obj{"id": certID, "name": "wildcard", "cert": "a certificate\n", "intermediates": "a chain\n",
		"key": "PRIVATE", "sha256_fingerprint": "AA:BB"})
	f.seed("cert", obj{"id": cert2ID, "name": "renewed", "cert": "one\n", "key": "PRIVATE"})
	f.seed("cert", obj{"id": cert3ID, "name": "renewed", "cert": "two\n", "key": "PRIVATE"})
	f.seed("mailuser", obj{"id": boxID, "name": "info", "imap_server": imapID, "procmailrc": ":0\ninfo/",
		"autoresponder_enable": true, "autoresponder_subject": "Away", "autoresponder_message": "Back on Monday.",
		"autoresponder_noreply": []any{"list@example.org"}})
	f.seed("address", obj{"id": addrID, "source": "info@example.com", "destinations": []any{boxID}, "forwards": []any{"someone@example.org"}})
	f.seed("mariauser", obj{"id": mariaUID, "name": "shop", "server": serverID, "external": true})
	f.seed("mariadb", obj{"id": mariaID, "name": "shop", "server": serverID, "charset": "utf8mb4", "dbusers_readwrite": []any{mariaUID}})
	f.seed("psqluser", obj{"id": psqlUID, "name": "wiki", "server": serverID})
	f.seed("psqldb", obj{"id": psqlID, "name": "wiki", "server": serverID, "dbusers_readonly": []any{psqlUID}})
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
data "opalstack_dnsrecord" "mx" { id = "` + recordID + `" }
data "opalstack_cert" "c" { name = "wildcard" }
data "opalstack_cert" "by_id" { id = "` + cert2ID + `" }
data "opalstack_mailuser" "m" { name = "info" }
data "opalstack_mailuser" "by_id" { id = data.opalstack_mailuser.m.id }
data "opalstack_address" "a" { source = "info@example.com" }
data "opalstack_address" "by_id" { id = data.opalstack_address.a.id }
data "opalstack_mariauser" "u" { name = "shop" }
data "opalstack_mariauser" "by_id" { id = data.opalstack_mariauser.u.id }
data "opalstack_mariadb" "db" { name = "shop" }
data "opalstack_mariadb" "by_id" { id = data.opalstack_mariadb.db.id }
data "opalstack_psqluser" "u" { name = "wiki" }
data "opalstack_psqluser" "by_id" { id = data.opalstack_psqluser.u.id }
data "opalstack_psqldb" "db" { name = "wiki" }
data "opalstack_psqldb" "by_id" { id = data.opalstack_psqldb.db.id }
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
				resource.TestCheckResourceAttr("data.opalstack_dnsrecord.mx", "domain_id", domainID),
				resource.TestCheckResourceAttr("data.opalstack_dnsrecord.mx", "type", "MX"),
				resource.TestCheckResourceAttr("data.opalstack_dnsrecord.mx", "content", "mail.example.com"),
				resource.TestCheckResourceAttr("data.opalstack_dnsrecord.mx", "priority", "20"),
				resource.TestCheckResourceAttr("data.opalstack_dnsrecord.mx", "ttl", "3600"),
				resource.TestCheckResourceAttr("data.opalstack_cert.c", "id", certID),
				resource.TestCheckResourceAttr("data.opalstack_cert.c", "cert", "a certificate\n"),
				resource.TestCheckResourceAttr("data.opalstack_cert.c", "intermediates", "a chain\n"),
				resource.TestCheckResourceAttr("data.opalstack_cert.c", "exp_date", "2030-01-01T00:00:00Z"),
				resource.TestCheckResourceAttr("data.opalstack_cert.c", "sha256_fingerprint", "AA:BB"),
				resource.TestCheckNoResourceAttr("data.opalstack_cert.c", "key"),
				resource.TestCheckResourceAttr("data.opalstack_cert.by_id", "name", "renewed"),
				resource.TestCheckNoResourceAttr("data.opalstack_cert.by_id", "intermediates"),
				resource.TestCheckResourceAttr("data.opalstack_mailuser.m", "id", boxID),
				resource.TestCheckResourceAttr("data.opalstack_mailuser.m", "imap_server_id", imapID),
				resource.TestCheckNoResourceAttr("data.opalstack_mailuser.m", "password"),
				resource.TestCheckResourceAttr("data.opalstack_mailuser.m", "procmailrc", ":0\ninfo/"),
				resource.TestCheckResourceAttr("data.opalstack_mailuser.m", "autoresponder_enable", "true"),
				resource.TestCheckResourceAttr("data.opalstack_mailuser.m", "autoresponder_subject", "Away"),
				resource.TestCheckResourceAttr("data.opalstack_mailuser.m", "autoresponder_message", "Back on Monday."),
				resource.TestCheckResourceAttr("data.opalstack_mailuser.m", "autoresponder_noreply.#", "1"),
				resource.TestCheckTypeSetElemAttr("data.opalstack_mailuser.m", "autoresponder_noreply.*", "list@example.org"),
				resource.TestCheckResourceAttr("data.opalstack_mailuser.by_id", "procmailrc", ":0\ninfo/"),
				resource.TestCheckResourceAttr("data.opalstack_mailuser.by_id", "name", "info"),
				resource.TestCheckResourceAttr("data.opalstack_address.a", "id", addrID),
				resource.TestCheckTypeSetElemAttr("data.opalstack_address.a", "mailuser_ids.*", boxID),
				resource.TestCheckTypeSetElemAttr("data.opalstack_address.a", "forwards.*", "someone@example.org"),
				resource.TestCheckResourceAttr("data.opalstack_address.by_id", "source", "info@example.com"),
				resource.TestCheckResourceAttr("data.opalstack_mariauser.u", "id", mariaUID),
				resource.TestCheckResourceAttr("data.opalstack_mariauser.u", "server_id", serverID),
				resource.TestCheckResourceAttr("data.opalstack_mariauser.u", "external", "true"),
				resource.TestCheckNoResourceAttr("data.opalstack_mariauser.u", "password"),
				resource.TestCheckResourceAttr("data.opalstack_mariauser.by_id", "name", "shop"),
				resource.TestCheckResourceAttr("data.opalstack_mariadb.db", "id", mariaID),
				resource.TestCheckResourceAttr("data.opalstack_mariadb.db", "charset", "utf8mb4"),
				resource.TestCheckTypeSetElemAttr("data.opalstack_mariadb.db", "user_ids.*", mariaUID),
				resource.TestCheckResourceAttr("data.opalstack_mariadb.db", "readonly_user_ids.#", "0"),
				resource.TestCheckResourceAttr("data.opalstack_mariadb.by_id", "name", "shop"),
				resource.TestCheckResourceAttr("data.opalstack_psqluser.u", "id", psqlUID),
				resource.TestCheckResourceAttr("data.opalstack_psqluser.u", "external", "false"),
				resource.TestCheckResourceAttr("data.opalstack_psqluser.by_id", "name", "wiki"),
				resource.TestCheckResourceAttr("data.opalstack_psqldb.db", "id", psqlID),
				resource.TestCheckResourceAttr("data.opalstack_psqldb.db", "user_ids.#", "0"),
				resource.TestCheckTypeSetElemAttr("data.opalstack_psqldb.db", "readonly_user_ids.*", psqlUID),
				resource.TestCheckResourceAttr("data.opalstack_psqldb.by_id", "name", "wiki"),
			),
		}},
	})
}

func TestDataSourceLookupErrors(t *testing.T) {
	f, factories, block := fakeProvider(t)
	seedAccount(f)
	for name, tc := range map[string]struct{ config, want string }{
		"no match":             {`data "opalstack_domain" "d" { name = "missing.example.com" }`, `No domain matches`},
		"several matches":      {`data "opalstack_app" "a" { name = "twice" }`, `More than one app matches`},
		"several certificates": {`data "opalstack_cert" "c" { name = "renewed" }`, `More than one certificate matches`},
		"no address":           {`data "opalstack_address" "a" { source = "missing@example.com" }`, `No address matches`},
		"other engine":         {`data "opalstack_psqldb" "db" { name = "shop" }`, `No PostgreSQL database matches`},
		"record without id":    {`data "opalstack_dnsrecord" "r" {}`, `(?s)Missing required argument`},
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
