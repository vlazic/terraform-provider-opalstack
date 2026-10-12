package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAppResource(t *testing.T) {
	f, factories, block := fakeProvider(t)
	seedAccount(f)
	const addr = "opalstack_app.a"
	config := func(name, appType, extra string) string {
		return block + fmt.Sprintf(`
resource "opalstack_app" "a" {
  name      = %q
  osuser_id = %q
  type      = %q
  %s
}
`, name, osuserID, appType, extra)
	}
	expect := func(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
		return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, action)}}
	}
	unchanged := func(config string) resource.TestStep {
		return resource.TestStep{Config: config, ConfigPlanChecks: expect(plancheck.ResourceActionNoop)}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if n := len(f.named("app", "tfacc-")); n != 0 {
				return fmt.Errorf("%d apps were left behind", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{ // no json: the API's own settings do not show up as a diff
				Config: config("tfacc-app", "STA", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "osuser_name", "sandbox"),
					resource.TestCheckResourceAttr(addr, "server_id", serverID),
					resource.TestCheckNoResourceAttr(addr, "json"),
					resource.TestCheckNoResourceAttr(addr, "port"),
				),
			},
			unchanged(config("tfacc-app", "STA", "")),
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateId: "tfacc-app"},
			{ // type and json change in place
				Config:           config("tfacc-app", "APA", `json = jsonencode({ php_version = 83 })`),
				ConfigPlanChecks: expect(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "type", "APA"),
					resource.TestCheckResourceAttr(addr, "json", `{"php_version":83}`),
				),
			},
			// The same settings written differently are not a change.
			unchanged(config("tfacc-app", "APA", `json = "{ \"php_version\" : 83 }"`)),
			{ // a managed setting changed outside Terraform is put back
				PreConfig: func() {
					for _, id := range f.named("app", "tfacc-") {
						f.edit(id, func(o obj) { o["json"].(obj)["php_version"] = float64(74) })
					}
				},
				Config:           config("tfacc-app", "APA", `json = jsonencode({ php_version = 83 })`),
				ConfigPlanChecks: expect(plancheck.ResourceActionUpdate),
			},
			{ // to the type with a port: replaced, because only a create assigns one
				Config:           config("tfacc-app", "CUS", ""),
				ConfigPlanChecks: expect(plancheck.ResourceActionDestroyBeforeCreate),
				Check:            resource.TestCheckResourceAttrSet(addr, "port"),
			},
			{ // and back
				Config:           config("tfacc-app", "APA", `json = jsonencode({ php_version = 83 })`),
				ConfigPlanChecks: expect(plancheck.ResourceActionDestroyBeforeCreate),
				Check:            resource.TestCheckNoResourceAttr(addr, "port"),
			},
			{ // a new name replaces the app
				Config:           config("tfacc-app2", "APA", `json = jsonencode({ php_version = 83 })`),
				ConfigPlanChecks: expect(plancheck.ResourceActionDestroyBeforeCreate),
			},
		},
	})
}
