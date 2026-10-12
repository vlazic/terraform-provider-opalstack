package provider

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// databaseConfig is two users and one database of an engine, "maria" or "psql".
func databaseConfig(engine, serverID, user, userExtra, db, dbExtra string) string {
	return fmt.Sprintf(`
resource "opalstack_%[1]suser" "u" {
  name      = %[3]q
  server_id = %[2]q
  %[4]s
}
resource "opalstack_%[1]suser" "reader" {
  name      = "tfacc-reader"
  server_id = %[2]q
}
resource "opalstack_%[1]sdb" "d" {
  name      = %[5]q
  server_id = %[2]q
  %[6]s
}
`, engine, serverID, user, userExtra, db, dbExtra)
}

func TestDatabaseResources(t *testing.T) {
	for _, engine := range []string{"maria", "psql"} {
		t.Run(engine, func(t *testing.T) {
			f, factories, block := fakeProvider(t)
			user, db := "opalstack_"+engine+"user.u", "opalstack_"+engine+"db.d"
			noPassword := []string{"default_password", "password"}
			config := func(user, userExtra, db, dbExtra string) string {
				return block + databaseConfig(engine, serverID, user, userExtra, db, dbExtra)
			}
			access := "user_ids = [opalstack_" + engine + "user.u.id]\n  readonly_user_ids = [opalstack_" + engine + "user.reader.id]"

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories,
				CheckDestroy: func(*terraform.State) error {
					if n := len(f.ids(engine+"user")) + len(f.ids(engine+"db")); n != 0 {
						return fmt.Errorf("%d objects were left behind", n)
					}
					return nil
				},
				Steps: []resource.TestStep{
					{
						Config: config("tfacc-one", "", "tfacc-db", ""),
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr(user, "external", "false"),
							resource.TestCheckResourceAttr(user, "default_password", "fake-default-password"),
							resource.TestCheckNoResourceAttr(user, "password"),
							resource.TestCheckResourceAttr(db, "charset", "utf8"),
							resource.TestCheckResourceAttr(db, "user_ids.#", "0"),
							resource.TestCheckResourceAttr(db, "readonly_user_ids.#", "0"),
						),
					},
					{Config: config("tfacc-one", "", "tfacc-db", ""), ConfigPlanChecks: accExpect(db, plancheck.ResourceActionNoop)},
					{ResourceName: user, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: noPassword},
					{ResourceName: user, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: noPassword, ImportStateId: "tfacc-one"},
					{ResourceName: db, ImportState: true, ImportStateVerify: true},
					{ResourceName: db, ImportState: true, ImportStateVerify: true, ImportStateId: "tfacc-db"},
					{ // a password and outside access, both in place; access to the database, in place
						Config: config("tfacc-one", `password = "first-Password1"`+"\n  external = true", "tfacc-db", access),
						ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(user, plancheck.ResourceActionUpdate),
							plancheck.ExpectResourceAction(db, plancheck.ResourceActionUpdate),
						}},
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr(user, "external", "true"),
							resource.TestCheckResourceAttr(user, "default_password", "fake-default-password"),
							resource.TestCheckResourceAttr(db, "user_ids.#", "1"),
							resource.TestCheckResourceAttr(db, "readonly_user_ids.#", "1"),
							func(*terraform.State) error {
								if body := f.lastBody(engine + "user/update"); !strings.Contains(body, `"password":"first-Password1"`) || !strings.Contains(body, `"external":true`) {
									return fmt.Errorf("unexpected user update: %s", body)
								}
								return nil
							},
						),
					},
					{ // only the password changes: external is not sent again
						Config:           config("tfacc-one", `password = "second-Password2"`+"\n  external = true", "tfacc-db", access),
						ConfigPlanChecks: accExpect(user, plancheck.ResourceActionUpdate),
						Check: func(*terraform.State) error {
							if body := f.lastBody(engine + "user/update"); !strings.Contains(body, `second-Password2`) || strings.Contains(body, "external") {
								return fmt.Errorf("unexpected user update: %s", body)
							}
							return nil
						},
					},
					{ // access taken away again: both lists are sent empty
						Config:           config("tfacc-one", `password = "second-Password2"`+"\n  external = true", "tfacc-db", ""),
						ConfigPlanChecks: accExpect(db, plancheck.ResourceActionUpdate),
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr(db, "user_ids.#", "0"),
							func(*terraform.State) error {
								if body := f.lastBody(engine + "db/update"); !strings.Contains(body, `"dbusers_readwrite":[]`) || !strings.Contains(body, `"dbusers_readonly":[]`) {
									return fmt.Errorf("unexpected database update: %s", body)
								}
								return nil
							},
						),
					},
					{ // a charset and a new name replace
						Config: config("tfacc-two", "", "tfacc-db", `charset = "latin1"`),
						ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(user, plancheck.ResourceActionDestroyBeforeCreate),
							plancheck.ExpectResourceAction(db, plancheck.ResourceActionDestroyBeforeCreate),
						}},
						Check: resource.TestCheckResourceAttr(db, "charset", "latin1"),
					},
				},
			})
		})
	}
}
