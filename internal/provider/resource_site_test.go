package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestSiteResource(t *testing.T) {
	f, factories, block := fakeProvider(t)
	seedAccount(f)
	const addr = "opalstack_site.s"
	// Two domains and two apps to route between; the site varies per step.
	config := func(site string) string {
		return block + fmt.Sprintf(`
resource "opalstack_domain" "a" { name = "tfacc-a.example.com" }
resource "opalstack_domain" "b" { name = "tfacc-b.example.com" }
resource "opalstack_app" "one" {
  name      = "tfacc-one"
  osuser_id = %[1]q
  type      = "STA"
}
resource "opalstack_app" "two" {
  name      = "tfacc-two"
  osuser_id = %[1]q
  type      = "STA"
}
resource "opalstack_site" "s" {
%[2]s
}
`, osuserID, site)
	}
	expect := func(action plancheck.ResourceActionType) resource.ConfigPlanChecks { return accExpect(addr, action) }
	const minimal = `
  name       = "tfacc-site"
  domain_ids = [opalstack_domain.a.id]
  routes     = [{ app_id = opalstack_app.one.id }]
  ip6_id     = "` + ip6ID + `"`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if n := len(f.ids("site")); n != 0 {
				return fmt.Errorf("%d sites were left behind", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config(minimal),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "domain_ids.#", "1"),
					resource.TestCheckResourceAttr(addr, "routes.#", "1"),
					resource.TestCheckResourceAttr(addr, "server_id", serverID),
					resource.TestCheckResourceAttr(addr, "disabled", "false"),
					resource.TestCheckNoResourceAttr(addr, "ip4_id"),
					resource.TestCheckResourceAttr(addr, "ip6_id", ip6ID),
					resource.TestCheckTypeSetElemNestedAttrs(addr, "routes.*", map[string]string{"uri": "/"}),
				),
			},
			{Config: config(minimal), ConfigPlanChecks: expect(plancheck.ResourceActionNoop)},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateId: "tfacc-site"},
			{ // everything changes in place, the name included
				Config: config(`
  name       = "tfacc-site-renamed"
  domain_ids = [opalstack_domain.a.id, opalstack_domain.b.id]
  routes = [
    { app_id = opalstack_app.one.id },
    { app_id = opalstack_app.two.id, uri = "/two/" },
  ]
  ip4_id   = "` + ipID + `"
  disabled = true`),
				ConfigPlanChecks: expect(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", "tfacc-site-renamed"),
					resource.TestCheckResourceAttr(addr, "domain_ids.#", "2"),
					resource.TestCheckResourceAttr(addr, "routes.#", "2"),
					resource.TestCheckResourceAttr(addr, "ip4_id", ipID),
					resource.TestCheckResourceAttr(addr, "ip6_id", ip6ID), // left as it was
					resource.TestCheckResourceAttr(addr, "disabled", "true"),
				),
			},
			{ // the same sets in another order are not a change
				Config: config(`
  name       = "tfacc-site-renamed"
  domain_ids = [opalstack_domain.b.id, opalstack_domain.a.id]
  routes = [
    { app_id = opalstack_app.two.id, uri = "/two/" },
    { app_id = opalstack_app.one.id, uri = "/" },
  ]
  ip4_id   = "` + ipID + `"
  disabled = true`),
				ConfigPlanChecks: expect(plancheck.ResourceActionNoop),
			},
		},
	})
}

func TestSiteResourceValidation(t *testing.T) {
	_, factories, block := fakeProvider(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: block + `
resource "opalstack_site" "s" {
  name       = "tfacc-site"
  domain_ids = ["` + domainID + `"]
  routes     = [{ app_id = "` + appID + `", uri = "/no-trailing-slash" }]
  ip4_id     = "` + ipID + `"
}`,
			ExpectError: regexp.MustCompile(`must start and end with /`),
		}, {
			Config: block + `
resource "opalstack_site" "s" {
  name       = "tfacc-site"
  domain_ids = ["` + domainID + `"]
  routes     = [{ app_id = "` + appID + `" }]
}`,
			ExpectError: regexp.MustCompile(`(?s)Missing Attribute Configuration`),
		}},
	})
}
