package provider

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

// The acceptance tests run against a real account, and only with TF_ACC set.
// They create, change and delete objects whose names start with accPrefix, and
// the provider is configured to refuse any other write. The sandbox comes from
// the environment, so no name of the account is written here:
//
//	OPALSTACK_TOKEN             a token for the account
//	OPALSTACK_TEST_DOMAIN_BASE  an existing domain; test domains are created under it
const accPrefix = "tfacc-"

var accFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"opalstack": providerserver.NewProtocol6WithError(New("acctest")()),
}

const accProvider = `
provider "opalstack" {
  safety_prefix = "` + accPrefix + `"
}
`

func accEnv(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" && os.Getenv(resource.EnvTfAcc) != "" {
		t.Fatalf("%s must be set for acceptance tests", name)
	}
	return v
}

// accName returns accPrefix plus six random lowercase alphanumerics.
func accName() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := []byte(accPrefix)
	for range 6 {
		b = append(b, chars[rand.IntN(len(chars))])
	}
	return string(b)
}

// accPassword is a throwaway password with some 160 bits of randomness, for a
// login that exists on a real server while a test runs.
func accPassword() string {
	raw := make([]byte, 20)
	if _, err := crand.Read(raw); err != nil {
		panic(err)
	}
	return "Aa1-" + hex.EncodeToString(raw)
}

func accClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{Token: os.Getenv(tokenEnvVar), SafetyPrefix: accPrefix})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// accNoneLeft fails when any object of a kind still has the test prefix,
// whichever test made it. The client is built when the check runs: building it
// earlier would need a token in a run that skips the acceptance tests.
func accNoneLeft[T any](t *testing.T, what string, kind func(*client.Client) client.ReadKind[T], name func(T) string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		all, err := kind(accClient(t)).List(context.Background())
		if err != nil {
			return err
		}
		for _, o := range all {
			if strings.HasPrefix(name(o), accPrefix) {
				return fmt.Errorf("a test %s was left behind", what)
			}
		}
		return nil
	}
}

func accNoDomainsLeft(t *testing.T) resource.TestCheckFunc {
	return accNoneLeft(t, "domain", func(c *client.Client) client.ReadKind[client.Domain] { return c.Domains().ReadKind }, func(d client.Domain) string { return d.Name })
}

func accNoAppsLeft(t *testing.T) resource.TestCheckFunc {
	return accNoneLeft(t, "app", func(c *client.Client) client.ReadKind[client.App] { return c.Apps().ReadKind }, func(a client.App) string { return a.Name })
}

func accExpect(addr string, action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, action)}}
}

func TestAccDomain(t *testing.T) {
	base := accEnv(t, "OPALSTACK_TEST_DOMAIN_BASE")
	accEnv(t, tokenEnvVar)
	first, second := accName()+"."+base, accName()+"."+base
	const addr = "opalstack_domain.d"
	expect := func(action plancheck.ResourceActionType) resource.ConfigPlanChecks { return accExpect(addr, action) }

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories,
		CheckDestroy:             accNoDomainsLeft(t),
		Steps: []resource.TestStep{
			{
				Config: accProvider + `resource "opalstack_domain" "d" { name = "` + first + `" }`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", first),
					resource.TestCheckResourceAttr(addr, "delegated_dns_zone", "false"),
					resource.TestCheckResourceAttrSet(addr, "id"),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateId: first},
			// No in-place update here: delegated_dns_zone is the only field that can
			// change, and the API refuses it for a name under a domain the account
			// already has, which every test domain is. See API_FINDINGS.md.
			{
				Config:           accProvider + `resource "opalstack_domain" "d" { name = "` + second + `" }`,
				ConfigPlanChecks: expect(plancheck.ResourceActionDestroyBeforeCreate),
				Check:            resource.TestCheckResourceAttr(addr, "name", second),
			},
			{ // deleted outside Terraform: the next plan recreates it
				PreConfig: func() {
					ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
					defer cancel()
					domains := accClient(t).Domains()
					all, err := domains.List(ctx)
					if err != nil {
						t.Fatal(err)
					}
					for _, d := range all {
						if d.Name != second {
							continue
						}
						if err := domains.Delete(ctx, d.ID); err != nil {
							t.Fatal(err)
						}
						if err := domains.WaitDeleted(ctx, d.ID); err != nil {
							t.Fatal(err)
						}
					}
				},
				Config:           accProvider + `resource "opalstack_domain" "d" { name = "` + second + `" }`,
				ConfigPlanChecks: expect(plancheck.ResourceActionCreate),
			},
		},
	})
}

func TestAccDNSRecord(t *testing.T) {
	base := accEnv(t, "OPALSTACK_TEST_DOMAIN_BASE")
	accEnv(t, tokenEnvVar)
	domain := accName() + "." + base
	const addr = "opalstack_dnsrecord.r"
	config := func(recordType, content, extra string) string {
		return accProvider + fmt.Sprintf(`
resource "opalstack_domain" "d" { name = %q }
resource "opalstack_dnsrecord" "r" {
  domain_id = opalstack_domain.d.id
  type      = %q
  content   = %q
  %s
}
`, domain, recordType, content, extra)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories,
		CheckDestroy:             accNoDomainsLeft(t),
		Steps: []resource.TestStep{
			{ // priority and ttl are left to the API
				Config: config("A", "192.0.2.1", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "content", "192.0.2.1"),
					resource.TestCheckResourceAttr(addr, "priority", "10"),
					resource.TestCheckResourceAttr(addr, "ttl", "3600"),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{
				Config:           config("A", "192.0.2.2", "ttl = 300"),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "content", "192.0.2.2"),
					resource.TestCheckResourceAttr(addr, "ttl", "300"),
				),
			},
			{
				Config:           config("TXT", "tfacc test record", "ttl = 300"),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionDestroyBeforeCreate),
			},
		},
	})
}

// accOSUser is the shell user of TestAccOSUser, which accLogin logs in as.
const accOSUser = "opalstack_osuser.u"

func TestAccOSUser(t *testing.T) {
	sandbox := accEnv(t, "OPALSTACK_TEST_OSUSER")
	accEnv(t, tokenEnvVar)
	first, second, third := accName(), accName(), accName()
	const addr = accOSUser
	// The new user goes on the server the sandbox user lives on.
	configWith := func(name, extra string) string {
		return accProvider + fmt.Sprintf(`
data "opalstack_osuser" "sandbox" { name = %q }
resource "opalstack_osuser" "u" {
  name      = %q
  server_id = data.opalstack_osuser.sandbox.server_id
  %s
}
`, sandbox, name, extra)
	}
	config := func(name string) string { return configWith(name, "") }
	noPassword := []string{"default_password", "password"}
	woFirst, woSecond, woCreate := accPassword(), accPassword(), accPassword()
	woPlan, woState := neverKept(woFirst, woSecond, woCreate)
	key := testPGPKey(t, nil)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories,
		CheckDestroy:             accNoneLeft(t, "shell user", func(c *client.Client) client.ReadKind[client.OSUser] { return c.OSUsers().ReadKind }, func(u client.OSUser) string { return u.Name }),
		Steps: []resource.TestStep{
			{
				Config: config(first),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", first),
					resource.TestCheckResourceAttrPair(addr, "server_id", "data.opalstack_osuser.sandbox", "server_id"),
					resource.TestCheckResourceAttrSet(addr, "default_password"),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: noPassword},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: noPassword, ImportStateId: first},
			{ // a password of its own, set in place; TestFindingOSUserPassword is what logs in with one
				Config:           configWith(first, `password = "`+accPassword()+`"`),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionUpdate),
			},
			{ // a write-only one instead, kept in neither the plan nor the state; a login proves it took
				Config:            configWith(first, writeOnly(woFirst, 1)),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: append(woPlan.PreApply, plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate))},
				ConfigStateChecks: woState,
				Check:             accLogin(t, func(*terraform.State) (string, error) { return woFirst, nil }),
			},
			{ // a new version sends the new one
				Config:            configWith(first, writeOnly(woSecond, 2)),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: append(woPlan.PreApply, plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate))},
				ConfigStateChecks: woState,
				Check:             accLogin(t, func(*terraform.State) (string, error) { return woSecond, nil }),
			},
			{ // created with a write-only password and no key: the API's own is kept nowhere, and the given one works
				Config:            configWith(second, writeOnly(woCreate, 1)),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: append(woPlan.PreApply, plancheck.ExpectResourceAction(addr, plancheck.ResourceActionDestroyBeforeCreate))},
				ConfigStateChecks: woState,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", second),
					resource.TestCheckNoResourceAttr(addr, "default_password"),
					resource.TestCheckNoResourceAttr(addr, "encrypted_default_password"),
					accLogin(t, func(*terraform.State) (string, error) { return woCreate, nil }),
				),
			},
			{ // created with a key of the run's own: the API's password is kept only encrypted, and works
				Config:           configWith(third, fmt.Sprintf("pgp_key = %q", armored(t, key, false))),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionDestroyBeforeCreate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", third),
					accEncrypted(addr, key),
					accLogin(t, accDecrypted(addr, key)),
				),
			},
		},
	})
}

// accSandboxProvider also names the sandbox shell user, the one unprefixed
// object a test app may refer to.
func accSandboxProvider(t *testing.T) (block, osuserID string) {
	osuserID = accEnv(t, "OPALSTACK_TEST_OSUSER_ID")
	return fmt.Sprintf(`
provider "opalstack" {
  safety_prefix    = %q
  safety_osuser_id = %q
}
`, accPrefix, osuserID), osuserID
}

// accLogin logs in over SSH, once, as the shell user at accOSUser, with the
// password the function gives. The failure masks the host and any address.
func accLogin(t *testing.T, password func(*terraform.State) (string, error)) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		user := s.RootModule().Resources[accOSUser].Primary.Attributes
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		host, err := webHost(ctx, accClient(t), user["server_id"])
		if err != nil {
			return err
		}
		secret, err := password(s)
		if err != nil {
			return err
		}
		if got := sshLogin(host, user["name"], secret); got != "accepted" {
			return fmt.Errorf("SSH login as %s: %s", user["name"], got)
		}
		return nil
	}
}

// accDecrypted is the password the API chose for the object at addr, decrypted
// with the run's key.
func accDecrypted(addr string, key *openpgp.Entity) func(*terraform.State) (string, error) {
	return func(s *terraform.State) (string, error) {
		return pgpDecrypt(key, s.RootModule().Resources[addr].Primary.Attributes["encrypted_default_password"])
	}
}

// accEncrypted checks that the object at addr keeps the password the API chose
// only encrypted, to the run's key.
func accEncrypted(addr string, key *openpgp.Entity) resource.TestCheckFunc {
	return resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckNoResourceAttr(addr, "default_password"),
		resource.TestCheckResourceAttr(addr, "key_fingerprint", pgpFingerprint(key)),
		func(s *terraform.State) error {
			password, err := accDecrypted(addr, key)(s)
			if err != nil {
				return fmt.Errorf("encrypted_default_password does not decrypt: %w", err)
			}
			if password == "" {
				return errors.New("encrypted_default_password decrypts to nothing")
			}
			return nil
		},
	)
}

// accLogApp records what the API holds for an app's settings, which the spec
// does not describe. Values under secret-looking keys are left out.
func accLogApp(t *testing.T, label string) resource.TestCheckFunc {
	const addr = "opalstack_app.a"
	return func(s *terraform.State) error {
		app, err := accClient(t).Apps().Read(context.Background(), s.RootModule().Resources[addr].Primary.ID)
		if err != nil {
			return err
		}
		t.Logf("FINDING %s: type=%s port=%v installer_url=%v json=%s", label, app.Type, app.Port != nil, app.InstallerURL != nil, settings(app.JSON))
		return nil
	}
}

func TestAccApp(t *testing.T) {
	accEnv(t, tokenEnvVar)
	provider, osuserID := accSandboxProvider(t)
	first, second := accName(), accName()
	const addr = "opalstack_app.a"
	config := func(name, appType, extra string) string {
		return provider + fmt.Sprintf(`
resource "opalstack_app" "a" {
  name      = %q
  osuser_id = %q
  type      = %q
  %s
}
`, name, osuserID, appType, extra)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories,
		CheckDestroy:             accNoAppsLeft(t),
		Steps: []resource.TestStep{
			{
				Config: config(first, "STA", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", first),
					resource.TestCheckResourceAttrSet(addr, "server_id"),
					resource.TestCheckResourceAttrSet(addr, "osuser_name"),
					accLogApp(t, "STA as created"),
				),
			},
			{Config: config(first, "STA", ""), ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionNoop)},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateId: first},
			{
				Config:           config(first, "APA", ""),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "type", "APA"),
					accLogApp(t, "after STA to APA in place"),
				),
			},
			{
				Config:           config(second, "APA", ""),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionDestroyBeforeCreate),
				Check:            accLogApp(t, "APA as created"),
			},
			{ // a managed setting; the framework fails the step if a plan remains afterwards
				Config:           config(second, "APA", `json = jsonencode({ php_version = 83 })`),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionUpdate),
				Check:            accLogApp(t, "APA with php_version 83"),
			},
			{ // to CUS the app is replaced: in place the API would assign no port
				Config:           config(second, "CUS", ""),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionDestroyBeforeCreate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(addr, "port"),
					accLogApp(t, "CUS as created"),
				),
			},
		},
	})
}

// accLogSite records what the API made of a site, and which DNS records exist
// for its domains: the spec says nothing about either.
func accLogSite(t *testing.T, label string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		c := accClient(t)
		site, err := c.Sites().Read(ctx, s.RootModule().Resources["opalstack_site.s"].Primary.ID)
		if err != nil {
			return err
		}
		records, err := c.DNSRecords().List(ctx)
		if err != nil {
			return err
		}
		var recordTypes, uris []string
		for _, rec := range records {
			if slices.Contains(site.Domains, rec.Domain) {
				recordTypes = append(recordTypes, rec.Type)
			}
		}
		for _, r := range site.Routes {
			uris = append(uris, r.URI)
		}
		t.Logf("FINDING %s: ip4=%v ip6=%v cert=%v primary_domain=%v redirect=%v generate_le=%v disabled=%v domains=%d route uris=%q dns records of its domains=%v",
			label, site.IP4 != nil, site.IP6 != nil, site.Cert != nil, site.PrimaryDomain != nil,
			site.Redirect, site.GenerateLE, site.Disabled, len(site.Domains), uris, recordTypes)
		return nil
	}
}

func TestAccSite(t *testing.T) {
	base := accEnv(t, "OPALSTACK_TEST_DOMAIN_BASE")
	accEnv(t, tokenEnvVar)
	provider, osuserID := accSandboxProvider(t)
	domainA, domainB := accName()+"."+base, accName()+"."+base
	appOne, appTwo, siteName := accName(), accName(), accName()
	const addr = "opalstack_site.s"
	config := func(site string) string {
		return provider + fmt.Sprintf(`
data "opalstack_osuser" "sandbox" { id = %[1]q }
data "opalstack_ip" "v4" { server_id = data.opalstack_osuser.sandbox.server_id }

resource "opalstack_domain" "a" { name = %[2]q }
resource "opalstack_domain" "b" { name = %[3]q }
resource "opalstack_app" "one" {
  name      = %[4]q
  osuser_id = %[1]q
  type      = "STA"
}
resource "opalstack_app" "two" {
  name      = %[5]q
  osuser_id = %[1]q
  type      = "STA"
}
resource "opalstack_site" "s" {
%[6]s
}
`, osuserID, domainA, domainB, appOne, appTwo, site)
	}
	// No certificate is requested: the test domains do not resolve anywhere.
	minimal := fmt.Sprintf(`
  name        = %q
  domain_ids  = [opalstack_domain.a.id]
  routes      = [{ app_id = opalstack_app.one.id }]
  ip4_id      = data.opalstack_ip.v4.id
  generate_le = false`, siteName)
	full := fmt.Sprintf(`
  name       = %q
  domain_ids = [opalstack_domain.a.id, opalstack_domain.b.id]
  routes = [
    { app_id = opalstack_app.one.id },
    { app_id = opalstack_app.two.id, uri = "/two/" },
  ]
  ip4_id      = data.opalstack_ip.v4.id
  generate_le = false
  disabled    = true`, siteName+"-b")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories,
		// When a step fails, what the API holds for the test site says why.
		ErrorCheck: func(stepErr error) error {
			sites, err := accClient(t).Sites().List(context.Background())
			if err != nil {
				return stepErr
			}
			for _, site := range sites {
				if strings.HasPrefix(site.Name, accPrefix) {
					var uris []string
					for _, r := range site.Routes {
						uris = append(uris, r.URI)
					}
					t.Logf("FINDING on error: state=%s domains=%d route uris=%q disabled=%v", site.State, len(site.Domains), uris, site.Disabled)
				}
			}
			return stepErr
		},
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			accNoneLeft(t, "site", func(c *client.Client) client.ReadKind[client.Site] { return c.Sites().ReadKind }, func(s client.Site) string { return s.Name }),
			accNoAppsLeft(t),
			accNoDomainsLeft(t),
		),
		Steps: []resource.TestStep{
			{
				Config: config(minimal),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", siteName),
					resource.TestCheckResourceAttr(addr, "domain_ids.#", "1"),
					resource.TestCheckResourceAttr(addr, "routes.#", "1"),
					accLogSite(t, "created with one domain and one route"),
				),
			},
			{Config: config(minimal), ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionNoop)},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateId: siteName},
			{
				Config:           config(full),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", siteName+"-b"),
					resource.TestCheckResourceAttr(addr, "domain_ids.#", "2"),
					resource.TestCheckResourceAttr(addr, "routes.#", "2"),
					resource.TestCheckResourceAttr(addr, "disabled", "true"),
					resource.TestCheckResourceAttrPair(addr, "ip4_id", "data.opalstack_ip.v4", "id"),
					accLogSite(t, "renamed, two domains, two routes, disabled"),
				),
			},
			{Config: config(full), ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionNoop)},
		},
	})
}

func TestAccMaria(t *testing.T) {
	accDatabases(t, maria, "utf8mb4")
}

func TestAccPsql(t *testing.T) {
	accDatabases(t, psql, "latin1")
}

// accDatabases runs one engine through users, a database and the access
// between them. The databases go on the server the sandbox shell user lives on.
func accDatabases(t *testing.T, engine dbEngine, charset string) {
	sandbox := accEnv(t, "OPALSTACK_TEST_OSUSER")
	accEnv(t, tokenEnvVar)
	userName, readerName, dbName, secondDB, pgpUser := accName(), accName(), accName(), accName(), accName()
	userType, dbType := "opalstack"+engine.userType, "opalstack"+engine.dbType
	user, db := userType+".u", dbType+".d"
	configNamed := func(userName, userExtra, db, dbExtra string) string {
		return accProvider + fmt.Sprintf(`
data "opalstack_osuser" "sandbox" { name = %[2]q }
resource "%[1]s" "u" {
  name      = %[3]q
  server_id = data.opalstack_osuser.sandbox.server_id
  %[4]s
}
resource "%[1]s" "reader" {
  name      = %[5]q
  server_id = data.opalstack_osuser.sandbox.server_id
}
resource "%[8]s" "d" {
  name      = %[6]q
  server_id = data.opalstack_osuser.sandbox.server_id
  %[7]s
}
`, userType, sandbox, userName, userExtra, readerName, db, dbExtra, dbType)
	}
	config := func(userExtra, db, dbExtra string) string { return configNamed(userName, userExtra, db, dbExtra) }
	access := "user_ids = [" + userType + ".u.id]\n  readonly_user_ids = [" + userType + ".reader.id]"
	password := `password = "` + accPassword() + `"`
	noPassword := []string{"default_password", "password"}
	woFirst, woSecond := accPassword(), accPassword()
	woPlan, woState := neverKept(woFirst, woSecond)
	noAccess := "user_ids = []\n  readonly_user_ids = []"
	key := testPGPKey(t, nil)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			accNoneLeft(t, "database", func(c *client.Client) client.ReadKind[client.DB] { return engine.dbs(c).ReadKind }, func(d client.DB) string { return d.Name }),
			accNoneLeft(t, "database user", func(c *client.Client) client.ReadKind[client.DBUser] { return engine.users(c).ReadKind }, func(u client.DBUser) string { return u.Name }),
		),
		Steps: []resource.TestStep{
			{
				Config: config("", dbName, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(user, "name", userName),
					resource.TestCheckResourceAttr(user, "external", "false"),
					resource.TestCheckResourceAttrSet(user, "default_password"),
					resource.TestCheckResourceAttrSet(db, "charset"),
					resource.TestCheckResourceAttr(db, "user_ids.#", "0"),
				),
			},
			{Config: config("", dbName, ""), ConfigPlanChecks: accExpect(db, plancheck.ResourceActionNoop)},
			{ResourceName: user, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: noPassword},
			{ResourceName: user, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: noPassword, ImportStateId: userName},
			{ResourceName: db, ImportState: true, ImportStateVerify: true},
			{ResourceName: db, ImportState: true, ImportStateVerify: true, ImportStateId: dbName},
			{
				Config: config(password+"\n  external = true", dbName, access),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(user, plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction(db, plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(user, "external", "true"),
					resource.TestCheckResourceAttr(db, "user_ids.#", "1"),
					resource.TestCheckResourceAttr(db, "readonly_user_ids.#", "1"),
				),
			},
			{Config: config(password+"\n  external = true", dbName, access), ConfigPlanChecks: accExpect(db, plancheck.ResourceActionNoop)},
			{
				Config:           config(password+"\n  external = true", dbName, noAccess),
				ConfigPlanChecks: accExpect(db, plancheck.ResourceActionUpdate),
				Check:            resource.TestCheckResourceAttr(db, "user_ids.#", "0"),
			},
			{ // a write-only password instead, kept in neither the plan nor the state
				Config:            config(writeOnly(woFirst, 1)+"external = true", dbName, noAccess),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: append(woPlan.PreApply, plancheck.ExpectResourceAction(user, plancheck.ResourceActionUpdate))},
				ConfigStateChecks: woState,
			},
			{ // a new version sends the new one
				Config:            config(writeOnly(woSecond, 2)+"external = true", dbName, noAccess),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: append(woPlan.PreApply, plancheck.ExpectResourceAction(user, plancheck.ResourceActionUpdate))},
				ConfigStateChecks: woState,
			},
			{
				Config:           config(writeOnly(woSecond, 2)+"external = true", secondDB, `charset = "`+charset+`"`),
				ConfigPlanChecks: accExpect(db, plancheck.ResourceActionDestroyBeforeCreate),
				Check:            resource.TestCheckResourceAttr(db, "charset", charset),
			},
			{ // a user created with a key of the run's own keeps the API's password only encrypted
				Config:           configNamed(pgpUser, fmt.Sprintf("pgp_key = %q", armored(t, key, false)), secondDB, `charset = "`+charset+`"`),
				ConfigPlanChecks: accExpect(user, plancheck.ResourceActionDestroyBeforeCreate),
				Check:            accEncrypted(user, key),
			},
		},
	})
}

// TestAccOSVar gives its variable to the sandbox shell user and to nobody
// else. It never makes a global one: the safety prefix would refuse it.
func TestAccOSVar(t *testing.T) {
	accEnv(t, tokenEnvVar)
	provider, osuserID := accSandboxProvider(t)
	first, second := accName(), accName()
	const addr = "opalstack_osvar.v"
	config := func(name, extra string) string {
		return provider + fmt.Sprintf(`
resource "opalstack_osvar" "v" {
  name = %q
  %s
}
`, name, extra)
	}
	given := fmt.Sprintf("osuser_ids = [%q]", osuserID)
	expect := func(action plancheck.ResourceActionType) resource.ConfigPlanChecks { return accExpect(addr, action) }

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories,
		CheckDestroy: accNoneLeft(t, "environment variable", func(c *client.Client) client.ReadKind[client.OSVar] { return c.OSVars().ReadKind },
			func(v client.OSVar) string { return v.Name }),
		Steps: []resource.TestStep{
			{
				Config: config(first, `content = "one"`+"\n  "+given),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", first),
					resource.TestCheckResourceAttr(addr, "content", "one"),
					resource.TestCheckResourceAttr(addr, "global", "false"),
					resource.TestCheckTypeSetElemAttr(addr, "osuser_ids.*", osuserID),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateId: first},
			{ // the content in place
				Config:           config(first, `content = "two words"`+"\n  "+given),
				ConfigPlanChecks: expect(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "content", "two words"),
					resource.TestCheckResourceAttr(addr, "osuser_ids.#", "1"),
				),
			},
			{ // taken from the shell user: it belongs to nobody, and keeps its content
				Config:           config(first, `content = "two words"`+"\n  osuser_ids = []"),
				ConfigPlanChecks: expect(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "content", "two words"),
					resource.TestCheckResourceAttr(addr, "osuser_ids.#", "0"),
				),
			},
			{ // a new name replaces the variable; with no content it is empty
				Config:           config(second, given),
				ConfigPlanChecks: expect(plancheck.ResourceActionDestroyBeforeCreate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", second),
					resource.TestCheckResourceAttr(addr, "content", ""),
				),
			},
		},
	})
}

func TestAccMail(t *testing.T) {
	if os.Getenv(resource.EnvTfAcc) == "" {
		t.Skip("an acceptance test: set TF_ACC=1")
	}
	base := accEnv(t, "OPALSTACK_TEST_DOMAIN_BASE")
	servers, err := accClient(t).ListServers(context.Background())
	if err != nil || len(servers.IMAP) == 0 {
		t.Fatalf("no IMAP server to put a test mailbox on: %v", err)
	}
	domain, boxName, local, other, pgpBox := accName()+"."+base, accName(), accName(), accName(), accName()
	const box, address = "opalstack_mailuser.m", "opalstack_address.a"
	configNamed := func(boxName, boxExtra, local, addressExtra string) string {
		return accProvider + fmt.Sprintf(`
data "opalstack_server" "imap" { hostname = %[1]q }
resource "opalstack_domain" "d" { name = %[2]q }
resource "opalstack_mailuser" "m" {
  name           = %[3]q
  imap_server_id = data.opalstack_server.imap.id
  %[4]s
}
resource "opalstack_address" "a" {
  source = "%[5]s@${opalstack_domain.d.name}"
  %[6]s
}
`, servers.IMAP[0].Hostname, domain, boxName, boxExtra, local, addressExtra)
	}
	config := func(boxExtra, local, addressExtra string) string {
		return configNamed(boxName, boxExtra, local, addressExtra)
	}
	delivered := "mailuser_ids = [opalstack_mailuser.m.id]"
	woFirst, woSecond := accPassword(), accPassword()
	woPlan, woState := neverKept(woFirst, woSecond)
	key := testPGPKey(t, nil)
	password := `password = "` + accPassword() + `"`
	noPassword := []string{"default_password", "password"}
	forwarded := `forwards = ["tfacc-forward@example.com"]` + "\n  mailuser_ids = []"
	const filter = `
  procmailrc = <<-EOT
    # tfacc
  EOT
  autoresponder_enable  = true
  autoresponder_subject = "tfacc subject"
  autoresponder_message = <<-EOT
    tfacc message
  EOT
  autoresponder_noreply = ["tfacc-noreply@example.com"]
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			accNoneLeft(t, "address", func(c *client.Client) client.ReadKind[client.Address] { return c.Addresses().ReadKind }, func(a client.Address) string { return a.Source }),
			accNoneLeft(t, "mailbox", func(c *client.Client) client.ReadKind[client.MailUser] { return c.MailUsers().ReadKind }, func(u client.MailUser) string { return u.Name }),
			accNoDomainsLeft(t),
		),
		Steps: []resource.TestStep{
			{
				Config: config("", local, delivered),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(box, "name", boxName),
					resource.TestCheckResourceAttrSet(box, "default_password"),
					resource.TestCheckResourceAttr(address, "source", local+"@"+domain),
					resource.TestCheckResourceAttr(address, "mailuser_ids.#", "1"),
					resource.TestCheckResourceAttr(address, "forwards.#", "0"),
				),
			},
			{Config: config("", local, delivered), ConfigPlanChecks: accExpect(address, plancheck.ResourceActionNoop)},
			{ResourceName: box, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: noPassword},
			{ResourceName: box, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: noPassword, ImportStateId: boxName},
			{ResourceName: address, ImportState: true, ImportStateVerify: true},
			{ResourceName: address, ImportState: true, ImportStateVerify: true, ImportStateId: local + "@" + domain},
			{
				Config: config(password, local, forwarded),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(box, plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "mailuser_ids.#", "0"),
					resource.TestCheckResourceAttr(address, "forwards.#", "1"),
				),
			},
			{Config: config(password, local, forwarded), ConfigPlanChecks: accExpect(address, plancheck.ResourceActionNoop)},
			{ // filter rules and autoresponder on the existing mailbox; the heredoc ends in a newline the API drops
				Config:           config(password+filter, local, forwarded),
				ConfigPlanChecks: accExpect(box, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(box, "procmailrc", "# tfacc\n"),
					resource.TestCheckResourceAttr(box, "autoresponder_enable", "true"),
					resource.TestCheckResourceAttr(box, "autoresponder_subject", "tfacc subject"),
					resource.TestCheckResourceAttr(box, "autoresponder_message", "tfacc message\n"),
					resource.TestCheckTypeSetElemAttr(box, "autoresponder_noreply.*", "tfacc-noreply@example.com"),
				),
			},
			{ // one changed, two cleared, the autoresponder off; the rules are left out and stay
				Config: config(password+`
  autoresponder_enable  = false
  autoresponder_subject = "tfacc gone"
  autoresponder_message = ""
  autoresponder_noreply = []`, local, forwarded),
				ConfigPlanChecks: accExpect(box, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(box, "procmailrc", "# tfacc\n"),
					resource.TestCheckResourceAttr(box, "autoresponder_enable", "false"),
					resource.TestCheckResourceAttr(box, "autoresponder_subject", "tfacc gone"),
					resource.TestCheckResourceAttr(box, "autoresponder_message", ""),
					resource.TestCheckResourceAttr(box, "autoresponder_noreply.#", "0"),
				),
			},
			{ // all five left out again: no change, and the mailbox keeps what it has
				Config:           config(password, local, forwarded),
				ConfigPlanChecks: accExpect(box, plancheck.ResourceActionNoop),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(box, "autoresponder_subject", "tfacc gone"),
					func(s *terraform.State) error {
						got, err := accClient(t).MailUsers().Read(context.Background(), s.RootModule().Resources[box].Primary.ID)
						if err != nil {
							return err
						}
						if got.Procmailrc != "# tfacc" || got.AutoresponderSubject != "tfacc gone" {
							return fmt.Errorf("the mailbox lost a field the configuration stopped naming")
						}
						return nil
					},
				),
			},
			{ // a write-only password instead, kept in neither the plan nor the state
				Config:            config(writeOnly(woFirst, 1), local, forwarded),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: append(woPlan.PreApply, plancheck.ExpectResourceAction(box, plancheck.ResourceActionUpdate))},
				ConfigStateChecks: woState,
			},
			{ // a new version sends the new one
				Config:            config(writeOnly(woSecond, 2), local, forwarded),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: append(woPlan.PreApply, plancheck.ExpectResourceAction(box, plancheck.ResourceActionUpdate))},
				ConfigStateChecks: woState,
			},
			{ // a mailbox created with a key of the run's own keeps the API's password only encrypted
				Config: configNamed(pgpBox, fmt.Sprintf("pgp_key = %q", armored(t, key, false)), other, delivered),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(box, plancheck.ResourceActionDestroyBeforeCreate),
					plancheck.ExpectResourceAction(address, plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: accEncrypted(box, key),
			},
		},
	})
}

// accSelfSigned makes a throwaway certificate for a host name and its key,
// both PEM encoded. No test ever uses a real certificate.
func accSelfSigned(t *testing.T, host string) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(30 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(crand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

func TestAccCert(t *testing.T) {
	base := accEnv(t, "OPALSTACK_TEST_DOMAIN_BASE")
	accEnv(t, tokenEnvVar)
	provider, osuserID := accSandboxProvider(t)
	domain, appName, siteName, first, second := accName()+"."+base, accName(), accName(), accName(), accName()
	const addr = "opalstack_cert.c"
	certOne, keyOne := accSelfSigned(t, domain)
	certTwo, keyTwo := accSelfSigned(t, domain)
	config := func(name, cert, key string) string {
		return provider + fmt.Sprintf(`
data "opalstack_osuser" "sandbox" { id = %[1]q }
data "opalstack_ip" "v4" { server_id = data.opalstack_osuser.sandbox.server_id }
resource "opalstack_domain" "d" { name = %[2]q }
resource "opalstack_app" "a" {
  name      = %[3]q
  osuser_id = %[1]q
  type      = "STA"
}
resource "opalstack_cert" "c" {
  name = %[5]q
  cert = %[6]q
  key  = %[7]q
}
resource "opalstack_site" "s" {
  name        = %[4]q
  domain_ids  = [opalstack_domain.d.id]
  routes      = [{ app_id = opalstack_app.a.id }]
  ip4_id      = data.opalstack_ip.v4.id
  cert_id     = opalstack_cert.c.id
  generate_le = false
}
`, osuserID, domain, appName, siteName, name, cert, key)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			accNoneLeft(t, "certificate", func(c *client.Client) client.ReadKind[client.Cert] { return c.Certs().ReadKind }, func(c client.Cert) string { return c.Name }),
			accNoneLeft(t, "site", func(c *client.Client) client.ReadKind[client.Site] { return c.Sites().ReadKind }, func(s client.Site) string { return s.Name }),
			accNoAppsLeft(t),
			accNoDomainsLeft(t),
		),
		Steps: []resource.TestStep{
			{
				Config: config(first, certOne, keyOne),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", first),
					resource.TestCheckResourceAttrSet(addr, "exp_date"),
					resource.TestCheckResourceAttrSet(addr, "sha256_fingerprint"),
					resource.TestCheckResourceAttrPair("opalstack_site.s", "cert_id", addr, "id"),
				),
			},
			{Config: config(first, certOne, keyOne), ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionNoop)},
			// The API returns the PEM text without the white space around it.
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"key", "cert"}},
			{ // renewed and renamed in place: the site keeps pointing at the same id
				Config: config(second, certTwo, keyTwo),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("opalstack_site.s", plancheck.ResourceActionNoop),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", second),
					resource.TestCheckResourceAttrPair("opalstack_site.s", "cert_id", addr, "id"),
				),
			},
			{Config: config(second, certTwo, keyTwo), ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionNoop)},
		},
	})
}
