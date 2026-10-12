package provider

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"

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

func accClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{Token: os.Getenv(tokenEnvVar), SafetyPrefix: accPrefix})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// accNoDomainsLeft fails when any domain with the test prefix still exists,
// whichever test made it.
func accNoDomainsLeft(t *testing.T) resource.TestCheckFunc {
	return func(*terraform.State) error {
		domains, err := accClient(t).Domains().List(context.Background())
		if err != nil {
			return err
		}
		for _, d := range domains {
			if strings.HasPrefix(d.Name, accPrefix) {
				return fmt.Errorf("a test domain was left behind (state %s)", d.State)
			}
		}
		return nil
	}
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

func TestAccOSUser(t *testing.T) {
	sandbox := accEnv(t, "OPALSTACK_TEST_OSUSER")
	accEnv(t, tokenEnvVar)
	first, second := accName(), accName()
	const addr = "opalstack_osuser.u"
	// The new user goes on the server the sandbox user lives on.
	config := func(name string) string {
		return accProvider + fmt.Sprintf(`
data "opalstack_osuser" "sandbox" { name = %q }
resource "opalstack_osuser" "u" {
  name      = %q
  server_id = data.opalstack_osuser.sandbox.server_id
}
`, sandbox, name)
	}
	noPassword := []string{"default_password"}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories,
		CheckDestroy: func(*terraform.State) error {
			users, err := accClient(t).OSUsers().List(context.Background())
			if err != nil {
				return err
			}
			for _, u := range users {
				if strings.HasPrefix(u.Name, accPrefix) {
					return fmt.Errorf("a test shell user was left behind (state %s)", u.State)
				}
			}
			return nil
		},
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
			{
				Config:           config(second),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionDestroyBeforeCreate),
				Check:            resource.TestCheckResourceAttr(addr, "name", second),
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

// accLogApp records what the API holds for an app's settings, which the spec
// does not describe. Values under secret-looking keys are left out.
func accLogApp(t *testing.T, label string) resource.TestCheckFunc {
	const addr = "opalstack_app.a"
	return func(s *terraform.State) error {
		app, err := accClient(t).Apps().Read(context.Background(), s.RootModule().Resources[addr].Primary.ID)
		if err != nil {
			return err
		}
		for key, value := range app.JSON {
			if strings.Contains(key, "pass") || strings.Contains(key, "secret") || strings.Contains(key, "key") || strings.Contains(key, "token") {
				app.JSON[key] = "REDACTED"
			} else if s, ok := value.(string); ok && len(s) > 40 {
				app.JSON[key] = s[:40] + "..."
			}
		}
		t.Logf("FINDING %s: type=%s port=%v installer_url=%v json=%#v", label, app.Type, app.Port != nil, app.InstallerURL != nil, app.JSON)
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
		CheckDestroy: func(*terraform.State) error {
			apps, err := accClient(t).Apps().List(context.Background())
			if err != nil {
				return err
			}
			for _, a := range apps {
				if strings.HasPrefix(a.Name, accPrefix) || a.OSUser == osuserID {
					return fmt.Errorf("a test app was left behind (state %s)", a.State)
				}
			}
			return nil
		},
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
		CheckDestroy: func(s *terraform.State) error {
			ctx := context.Background()
			c := accClient(t)
			sites, err := c.Sites().List(ctx)
			if err != nil {
				return err
			}
			for _, site := range sites {
				if strings.HasPrefix(site.Name, accPrefix) {
					return fmt.Errorf("a test site was left behind (state %s)", site.State)
				}
			}
			apps, err := c.Apps().List(ctx)
			if err != nil {
				return err
			}
			for _, a := range apps {
				if strings.HasPrefix(a.Name, accPrefix) {
					return fmt.Errorf("a test app was left behind (state %s)", a.State)
				}
			}
			return accNoDomainsLeft(t)(s)
		},
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
