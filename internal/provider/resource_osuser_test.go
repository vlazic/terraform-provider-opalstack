package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func osuserConfig(name string) string {
	return fmt.Sprintf(`
resource "opalstack_osuser" "u" {
  name      = %q
  server_id = %q
}
`, name, serverID)
}

func osuserWithPassword(name, password string) string {
	return fmt.Sprintf(`
resource "opalstack_osuser" "u" {
  name      = %q
  server_id = %q
  password  = %q
}
`, name, serverID, password)
}

func TestOSUserResource(t *testing.T) {
	f, factories, block := fakeProvider(t)
	const addr = "opalstack_osuser.u"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if n := len(f.ids("osuser")); n != 0 {
				return fmt.Errorf("%d shell users were left behind", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: block + osuserConfig("tfacc-one"),
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
				Config:             block + osuserConfig("tfacc-one"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
			{ // a password is set in place, and nothing else is sent with it
				Config:           block + osuserWithPassword("tfacc-one", "tfacc-password-one"),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "default_password", "fake-default-password"),
					f.sent("osuser/update", []string{`"password":"tfacc-password-one"`}, []string{`"name"`, `"server"`}),
				),
			},
			{ // taken out of the configuration again: no request, the shell user keeps it
				Config:           block + osuserConfig("tfacc-one"),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionUpdate),
				Check: func(*terraform.State) error {
					if n := f.wrote("osuser/update"); n != 1 {
						return fmt.Errorf("%d updates were sent, want the one that set the password", n)
					}
					return nil
				},
			},
			{ // a new name replaces the shell user
				Config:           block + osuserConfig("tfacc-two"),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionDestroyBeforeCreate),
				Check:            resource.TestCheckResourceAttr(addr, "default_password", "fake-default-password"),
			},
			{ // created with a password: the create carries it, and the state holds the API's own as well
				Config:           block + osuserWithPassword("tfacc-three", "tfacc-password-two"),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionDestroyBeforeCreate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "password", "tfacc-password-two"),
					resource.TestCheckResourceAttr(addr, "default_password", "fake-default-password"),
					f.sent("osuser/create", []string{`"name":"tfacc-three"`, `"password":"tfacc-password-two"`}, nil),
					func(*terraform.State) error {
						if n := f.wrote("osuser/update"); n != 1 {
							return fmt.Errorf("%d updates were sent, want none for a password given on create", n)
						}
						return nil
					},
				),
			},
		},
	})
}
