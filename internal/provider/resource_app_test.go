package provider

import (
	"fmt"
	"regexp"
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
	expect := func(action plancheck.ResourceActionType) resource.ConfigPlanChecks { return accExpect(addr, action) }
	unchanged := func(config string) resource.TestStep {
		return resource.TestStep{Config: config, ConfigPlanChecks: expect(plancheck.ResourceActionNoop)}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if n := len(f.ids("app", "tfacc-")); n != 0 {
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
					for _, id := range f.ids("app", "tfacc-") {
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

func TestAppResourceImportedInstaller(t *testing.T) {
	f, factories, block := fakeProvider(t)
	seedAccount(f)
	const installed = "50000000-0000-0000-0000-000000000009"
	f.seed("app", obj{"id": installed, "name": "tfacc-installed", "osuser": osuserID, "osuser_name": "sandbox", "server": serverID,
		"type": "CUS", "port": 20001, "installer_url": "https://example.com/install.sh", "json": obj{}})
	config := block + `
resource "opalstack_app" "a" {
  name      = "tfacc-installed"
  osuser_id = "` + osuserID + `"
  type      = "CUS"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: config, ResourceName: "opalstack_app.a", ImportState: true, ImportStateId: installed, ImportStatePersist: true},
			{ // the configuration does not repeat the installer, and that is not a change
				Config:           config,
				ConfigPlanChecks: accExpect("opalstack_app.a", plancheck.ResourceActionNoop),
				Check:            resource.TestCheckResourceAttr("opalstack_app.a", "installer_url", "https://example.com/install.sh"),
			},
		},
	})
	// The one delete is the test's own teardown. A replacement would show as a create.
	if creates, deletes := f.wrote("app/create"), f.wrote("app/delete"); creates != 0 || deletes != 1 {
		t.Errorf("%d creates and %d deletes were sent for an imported app, want 0 and 1", creates, deletes)
	}
}

func TestAppResourceJSONMustBeAnObject(t *testing.T) {
	_, factories, block := fakeProvider(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: block + `
resource "opalstack_app" "a" {
  name      = "tfacc-app"
  osuser_id = "` + osuserID + `"
  type      = "APA"
  json      = "[1, 2]"
}`,
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`must be a JSON object`),
		}},
	})
}
