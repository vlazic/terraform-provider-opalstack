package provider

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// The fake API does not parse certificates, so any text will do.
func certConfig(name, cert, extra string) string {
	return fmt.Sprintf(`
resource "opalstack_cert" "c" {
  name = %q
  cert = %q
  key  = "fake-key"
  %s
}
`, name, cert, extra)
}

func TestCertResource(t *testing.T) {
	f, factories, block := fakeProvider(t)
	const addr = "opalstack_cert.c"
	noKey := []string{"key"}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if n := len(f.ids("cert")); n != 0 {
				return fmt.Errorf("%d certificates were left behind", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: block + certConfig("tfacc-cert", "first certificate\n", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "exp_date", "2030-01-01T00:00:00Z"),
					resource.TestCheckResourceAttrSet(addr, "sha256_fingerprint"),
					resource.TestCheckNoResourceAttr(addr, "intermediates"),
				),
			},
			{ // the API trims the text: that is not a change
				PreConfig:        func() { f.edit(f.ids("cert")[0], func(o obj) { o["cert"] = "first certificate" }) },
				Config:           block + certConfig("tfacc-cert", "first certificate\n", ""),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionNoop),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: append(noKey, "cert")},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: append(noKey, "cert"), ImportStateId: "tfacc-cert"},
			{ // a renewal and a new name, in place, with certificate and key sent together
				Config:           block + certConfig("tfacc-renewed", "second certificate", `intermediates = "chain"`),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", "tfacc-renewed"),
					resource.TestCheckResourceAttr(addr, "intermediates", "chain"),
					func(*terraform.State) error {
						body := f.lastBody("cert/update")
						for _, want := range []string{`"cert":"second certificate"`, `"key":"fake-key"`, `"intermediates":"chain"`} {
							if !strings.Contains(body, want) {
								return fmt.Errorf("the update lacks %s: %s", want, body)
							}
						}
						return nil
					},
				),
			},
			{ // intermediates taken away again: sent as null
				Config:           block + certConfig("tfacc-renewed", "second certificate", ""),
				ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionUpdate),
				Check: func(*terraform.State) error {
					if body := f.lastBody("cert/update"); !strings.Contains(body, `"intermediates":null`) {
						return fmt.Errorf("intermediates were not cleared: %s", body)
					}
					return nil
				},
			},
		},
	})
}
