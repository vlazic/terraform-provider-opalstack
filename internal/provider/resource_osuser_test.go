package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func osuserConfig(name, serverID string) string {
	return fmt.Sprintf(`
resource "opalstack_osuser" "u" {
  name      = %q
  server_id = %q
}
`, name, serverID)
}

func TestOSUserResource(t *testing.T) {
	f, factories, block := fakeProvider(t)
	const addr = "opalstack_osuser.u"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if n := f.count("osuser"); n != 0 {
				return fmt.Errorf("%d shell users were left behind", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: block + osuserConfig("tfacc-one", serverID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", "tfacc-one"),
					resource.TestCheckResourceAttr(addr, "server_id", serverID),
					resource.TestCheckResourceAttr(addr, "default_password", "fake-default-password"),
				),
			},
			// The password exists only in the answer to the create.
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"default_password"}},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"default_password"}, ImportStateId: "tfacc-one"},
			{ // an unchanged config plans nothing and keeps the password
				Config:             block + osuserConfig("tfacc-one", serverID),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
			{ // a new name replaces the shell user
				Config: block + osuserConfig("tfacc-two", serverID),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(addr, plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.TestCheckResourceAttr(addr, "default_password", "fake-default-password"),
			},
		},
	})
}
