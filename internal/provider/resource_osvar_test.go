package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestOSVarResource(t *testing.T) {
	f, factories, block := fakeProvider(t)
	const addr, twin = "opalstack_osvar.v", "80000000-0000-0000-0000-000000000002"
	config := func(name, extra string) string {
		return block + fmt.Sprintf(`
resource "opalstack_osvar" "v" {
  name = %q
  %s
}
`, name, extra)
	}
	expect := func(action plancheck.ResourceActionType) resource.ConfigPlanChecks { return accExpect(addr, action) }
	given := fmt.Sprintf("osuser_ids = [%q]", osuserID)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if n := len(f.ids("osvar")); n != 0 {
				return fmt.Errorf("%d variables were left behind", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("tfacc-VAR", `content = "one"`+"\n  "+given),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "content", "one"),
					resource.TestCheckResourceAttr(addr, "global", "false"),
					resource.TestCheckTypeSetElemAttr(addr, "osuser_ids.*", osuserID),
					f.sent("osvar/create", []string{`"content":"one"`, `"osusers":["` + osuserID + `"]`}, []string{`"global"`}),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateId: "tfacc-VAR"},
			{ // the content changes in place, and nothing else is sent
				Config:           config("tfacc-VAR", `content = "two words"`+"\n  "+given),
				ConfigPlanChecks: expect(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "content", "two words"),
					f.sent("osvar/update", []string{`"content":"two words"`}, []string{`"osusers"`, `"global"`, `"name"`}),
				),
			},
			{ // taken from every shell user, and given to all of them instead
				Config:           config("tfacc-VAR", `content = "two words"`+"\n  osuser_ids = []\n  global = true"),
				ConfigPlanChecks: expect(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "osuser_ids.#", "0"),
					resource.TestCheckResourceAttr(addr, "global", "true"),
					f.sent("osvar/update", []string{`"osusers":[]`, `"global":true`}, []string{`"content"`}),
				),
			},
			{ // global and the shell users left out: both stay as the API has them
				Config:           config("tfacc-VAR", `content = "two words"`),
				ConfigPlanChecks: expect(plancheck.ResourceActionNoop),
				Check:            resource.TestCheckResourceAttr(addr, "global", "true"),
			},
			{ // the content left out too, and global switched off: nothing else is sent
				Config:           config("tfacc-VAR", "global = false"),
				ConfigPlanChecks: expect(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "global", "false"),
					resource.TestCheckResourceAttr(addr, "content", "two words"),
					f.sent("osvar/update", []string{`"global":false`}, []string{`"osusers"`, `"content"`}),
				),
			},
			{ // a new name is another variable: created global, with no content and no shell user
				Config:           config("tfacc-OTHER", "global = true"),
				ConfigPlanChecks: expect(plancheck.ResourceActionDestroyBeforeCreate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "content", ""),
					resource.TestCheckResourceAttr(addr, "osuser_ids.#", "0"),
					resource.TestCheckResourceAttr(addr, "global", "true"),
					f.sent("osvar/create", []string{`"name":"tfacc-OTHER"`, `"content":""`, `"osusers":[]`, `"global":true`}, nil),
				),
			},
			{ // a name is not unique: one that two variables carry does not import
				PreConfig:     func() { f.seed("osvar", obj{"id": twin, "name": "tfacc-OTHER"}) },
				ResourceName:  addr,
				ImportState:   true,
				ImportStateId: "tfacc-OTHER",
				ExpectError:   regexp.MustCompile(`More than one environment variable matches`),
			},
			{ // deleted outside Terraform: the next plan recreates it
				PreConfig: func() {
					for _, id := range f.ids("osvar") {
						f.edit(id, nil)
					}
				},
				Config:           config("tfacc-OTHER", "global = true"),
				ConfigPlanChecks: expect(plancheck.ResourceActionCreate),
			},
		},
	})

	// Its own case: a config that does not validate cannot be destroyed either.
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config:      config("not a name", ""),
			ExpectError: regexp.MustCompile(`(?s)Invalid Attribute Value`),
		}},
	})
}

// Whether the API trims the content is not verified. If it does, a content
// that ends in a newline, as one read from a file does, must not be a change.
func TestOSVarContentKeepsItsWhiteSpace(t *testing.T) {
	f, factories, block := fakeProvider(t)
	f.stored = trimmed("osvar", "content")
	const addr = "opalstack_osvar.v"
	config := func(content string) string {
		return block + fmt.Sprintf(`
resource "opalstack_osvar" "v" {
  name    = "tfacc-VAR"
  content = %q
}
`, content)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: config("one\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "content", "one\n"), // as written
					func(*terraform.State) error {
						if got := f.get(f.ids("osvar")[0])["content"]; got != "one" {
							return fmt.Errorf("the fake stores the content as %q", got)
						}
						return nil
					},
				),
			},
			{Config: config("one\n"), ConfigPlanChecks: expectEmptyPlan()},
			{ // another content is still a change, and is kept as written too
				Config:           config(" two\n"),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "content", " two\n"),
					f.sent("osvar/update", []string{`"content":" two\n"`}, nil),
				),
			},
		},
	})

	// Adopted, the state holds what the API has, and the configuration still
	// changes nothing.
	f.seed("osvar", obj{"id": recordID, "name": "tfacc-HELD", "content": "one"})
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: block + `
import {
  to = opalstack_osvar.v
  id = "tfacc-HELD"
}
resource "opalstack_osvar" "v" {
  name    = "tfacc-HELD"
  content = "one\n"
}
`,
				Check: resource.TestCheckResourceAttr(addr, "content", "one"),
			},
			{Config: block + keepOnRemove("opalstack_osvar.v")},
		},
	})
	if n := f.wrote("osvar/update"); n != 1 {
		t.Errorf("%d updates were sent, want the one that changed the content", n)
	}
}
