package provider

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
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
func accLogApp(t *testing.T, label, addr string) resource.TestCheckFunc {
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
					accLogApp(t, "STA as created", addr),
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
					accLogApp(t, "after STA to APA in place", addr),
				),
			},
			{
				Config:           config(second, "APA", ""),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionDestroyBeforeCreate),
				Check:            accLogApp(t, "APA as created", addr),
			},
			{ // a managed setting; the framework fails the step if a plan remains afterwards
				Config:           config(second, "APA", `json = jsonencode({ php_version = 83 })`),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionUpdate),
				Check:            accLogApp(t, "APA with php_version 83", addr),
			},
			{ // to CUS the app is replaced: in place the API would assign no port
				Config:           config(second, "CUS", ""),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionDestroyBeforeCreate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(addr, "port"),
					accLogApp(t, "CUS as created", addr),
				),
			},
		},
	})
}
