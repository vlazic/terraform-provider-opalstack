package provider

import (
	"fmt"
	"reflect"
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

// The API replaces the whole json on an update, so an update must send back the
// settings made in the control panel, and the keys the configuration stopped
// naming keep their values.
func TestAppResourceKeepsUnmanagedSettings(t *testing.T) {
	f, factories, block := fakeProvider(t)
	seedAccount(f)
	config := func(settings string) string {
		if settings != "" {
			settings = "json = jsonencode(" + settings + ")"
		}
		return block + `
resource "opalstack_app" "a" {
  name      = "tfacc-app"
  osuser_id = "` + osuserID + `"
  type      = "APA"
  ` + settings + `
}`
	}
	stored := func() obj { return f.get(f.ids("app", "tfacc-")[0])["json"].(obj) }
	want := func(want obj) resource.TestCheckFunc {
		return func(*terraform.State) error {
			if got := stored(); !reflect.DeepEqual(got, want) {
				return fmt.Errorf("the app's json is %v, want %v", got, want)
			}
			return nil
		}
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: config(`{ php_version = 83 }`)},
			{
				PreConfig: func() {
					f.edit(f.ids("app", "tfacc-")[0], func(o obj) { o["json"].(obj)["gzip"] = true })
				},
				Config: config(`{ php_version = 84 }`),
				Check:  want(obj{"auto_site_url": false, "gzip": true, "php_version": float64(84)}),
			},
			{ // php_version is no longer named and keeps 84, not the server's default
				Config: config(`{ expires = "1d" }`),
				Check:  want(obj{"auto_site_url": false, "expires": "1d", "gzip": true, "php_version": float64(84)}),
			},
			{Config: config(`{ expires = "1d", php_version = 84 }`)},
			{ // only removing a key is no change to the app: no json is sent
				Config: config(`{ expires = "1d" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					want(obj{"auto_site_url": false, "expires": "1d", "gzip": true, "php_version": float64(84)}),
					f.sent("app/update", nil, []string{`"json"`}),
				),
			},
			{ // without the attribute nothing is managed, and no json is sent
				Config: config(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					want(obj{"auto_site_url": false, "expires": "1d", "gzip": true, "php_version": float64(84)}),
					f.sent("app/update", nil, []string{`"json"`}),
				),
			},
		},
	})
}

// A type change sends no json unless the settings change or the app leaves a
// symlink type, whose sym_link_path no other type takes.
func TestAppResourceTypeChangeSettings(t *testing.T) {
	f, factories, block := fakeProvider(t)
	seedAccount(f)
	config := func(appType, extra string) string {
		return block + `
resource "opalstack_app" "a" {
  name      = "tfacc-app"
  osuser_id = "` + osuserID + `"
  type      = "` + appType + `"
  ` + extra + `
}`
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: config("SLS", "")},
			{
				PreConfig: func() {
					f.edit(f.ids("app", "tfacc-")[0], func(o obj) {
						o["json"].(obj)["sym_link_path"], o["json"].(obj)["gzip"] = "/home/sandbox/apps/", true
					})
				},
				Config: config("SLP", ""),
				Check:  f.sent("app/update", []string{`"SLP"`}, []string{`"json"`}),
			},
			{
				Config: config("STA", ""),
				Check:  f.sent("app/update", []string{`"gzip":true`}, []string{"sym_link_path"}),
			},
			{Config: config("APA", `json = jsonencode({ php_version = 83 })`)},
			{ // the same settings under another type: no json is sent
				Config: config("NPF", `json = jsonencode({ php_version = 83 })`),
				Check:  f.sent("app/update", []string{`"NPF"`}, []string{`"json"`}),
			},
		},
	})
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
