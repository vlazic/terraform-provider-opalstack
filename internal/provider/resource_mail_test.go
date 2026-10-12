package provider

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func mailConfig(boxExtra, source, addressExtra string) string {
	return fmt.Sprintf(`
resource "opalstack_mailuser" "m" {
  name           = "tfacc-box"
  imap_server_id = %q
  %s
}
resource "opalstack_address" "a" {
  source = %q
  %s
}
`, serverID, boxExtra, source, addressExtra)
}

func TestMailResources(t *testing.T) {
	f, factories, block := fakeProvider(t)
	const box, address = "opalstack_mailuser.m", "opalstack_address.a"
	noPassword := []string{"default_password", "password"}
	delivered := "mailuser_ids = [opalstack_mailuser.m.id]"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if n := len(f.ids("mailuser")) + len(f.ids("address")); n != 0 {
				return fmt.Errorf("%d objects were left behind", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: block + mailConfig("", "tfacc-info@example.com", delivered),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(box, "default_password", "fake-default-password"),
					resource.TestCheckResourceAttr(address, "mailuser_ids.#", "1"),
					resource.TestCheckResourceAttr(address, "forwards.#", "0"),
					func(*terraform.State) error {
						if body := f.lastBody("address/create"); !strings.Contains(body, `"forwards":[]`) {
							return fmt.Errorf("forwards must always be sent: %s", body)
						}
						return nil
					},
				),
			},
			{Config: block + mailConfig("", "tfacc-info@example.com", delivered), ConfigPlanChecks: accExpect(address, plancheck.ResourceActionNoop)},
			{ResourceName: box, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: noPassword},
			{ResourceName: box, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: noPassword, ImportStateId: "tfacc-box"},
			{ResourceName: address, ImportState: true, ImportStateVerify: true},
			{ResourceName: address, ImportState: true, ImportStateVerify: true, ImportStateId: "tfacc-info@example.com"},
			{ // a password in place; the address forwards instead of delivering
				Config: block + mailConfig(`password = "first-Password1"`, "tfacc-info@example.com", `forwards = ["someone@example.org"]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(box, plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "mailuser_ids.#", "0"),
					resource.TestCheckResourceAttr(address, "forwards.#", "1"),
					func(*terraform.State) error {
						if body := f.lastBody("address/update"); !strings.Contains(body, `"destinations":[]`) {
							return fmt.Errorf("the emptied list must be sent: %s", body)
						}
						if body := f.lastBody("mailuser/update"); !strings.Contains(body, "first-Password1") {
							return fmt.Errorf("unexpected mailbox update: %s", body)
						}
						return nil
					},
				),
			},
			{ // another source is another address
				Config:           block + mailConfig(`password = "first-Password1"`, "tfacc-sales@example.com", delivered),
				ConfigPlanChecks: accExpect(address, plancheck.ResourceActionDestroyBeforeCreate),
			},
		},
	})
}
