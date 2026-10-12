package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func domainConfig(name string, delegated bool) string {
	return fmt.Sprintf(`
resource "opalstack_domain" "d" {
  name               = %q
  delegated_dns_zone = %t
}
`, name, delegated)
}

func TestDomainResource(t *testing.T) {
	f, factories, block := fakeProvider(t)
	const addr = "opalstack_domain.d"
	var firstID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if left := f.named("domain", "tfacc-"); len(left) != 0 {
				return fmt.Errorf("%d test domains were left behind", len(left))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{ // create, with the optional attribute left to the API
				Config: block + `resource "opalstack_domain" "d" { name = "tfacc-a.example.com" }`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", "tfacc-a.example.com"),
					resource.TestCheckResourceAttr(addr, "delegated_dns_zone", "false"),
					resource.TestCheckResourceAttr(addr, "is_valid_hostname", "true"),
					resource.TestCheckNoResourceAttr(addr, "dkim_privkey"),
					resource.TestCheckResourceAttrWith(addr, "id", func(id string) error { firstID = id; return nil }),
				),
			},
			{ // import by UUID
				ResourceName: addr, ImportState: true, ImportStateVerify: true,
			},
			{ // import by name
				ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateId: "tfacc-a.example.com",
			},
			{ // update in place
				Config: block + domainConfig("tfacc-a.example.com", true),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "delegated_dns_zone", "true"),
					resource.TestCheckResourceAttrWith(addr, "id", func(id string) error {
						if id != firstID {
							return fmt.Errorf("the id changed from %s to %s on an in-place update", firstID, id)
						}
						return nil
					}),
				),
			},
			{ // a new name replaces the domain
				Config: block + domainConfig("tfacc-b.example.com", true),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(addr, plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.TestCheckResourceAttr(addr, "name", "tfacc-b.example.com"),
			},
			{ // deleted outside Terraform: the next plan recreates it
				PreConfig: func() {
					for _, id := range f.named("domain", "tfacc-b.") {
						f.remove(id)
					}
				},
				Config: block + domainConfig("tfacc-b.example.com", true),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(addr, plancheck.ResourceActionCreate),
				}},
			},
		},
	})

	if n := f.wrote("domain/create"); n != 3 {
		t.Errorf("%d creates were sent, want 3", n)
	}
	if n := f.wrote("domain/update"); n != 1 {
		t.Errorf("%d updates were sent, want 1", n)
	}
}

func TestDomainResourceSafetyPrefix(t *testing.T) {
	f, factories, block := fakeProvider(t)
	block = regexp.MustCompile(`\n}\n$`).ReplaceAllString(block, "\n  safety_prefix = \"tfacc-\"\n}\n")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config:      block + `resource "opalstack_domain" "d" { name = "www.example.com" }`,
			ExpectError: regexp.MustCompile(`refused by the safety prefix`),
		}},
	})
	if n := f.wrote("domain/create"); n != 0 {
		t.Errorf("%d creates reached the API", n)
	}
}
