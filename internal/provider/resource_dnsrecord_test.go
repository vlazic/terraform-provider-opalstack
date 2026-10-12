package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestDNSRecordResource(t *testing.T) {
	f, factories, block := fakeProvider(t)
	const addr = "opalstack_dnsrecord.r"
	config := func(recordType, content, extra string) string {
		return block + fmt.Sprintf(`
resource "opalstack_domain" "d" { name = "tfacc-a.example.com" }
resource "opalstack_dnsrecord" "r" {
  domain_id = opalstack_domain.d.id
  type      = %q
  content   = %q
  %s
}
`, recordType, content, extra)
	}
	expect := func(action plancheck.ResourceActionType) resource.ConfigPlanChecks { return accExpect(addr, action) }

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if n := len(f.ids("dnsrecord")) + len(f.ids("domain")); n != 0 {
				return fmt.Errorf("%d objects were left behind", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{ // create, with priority and ttl left to the API
				Config: config("A", "192.0.2.1", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "content", "192.0.2.1"),
					resource.TestCheckResourceAttr(addr, "priority", "10"),
					resource.TestCheckResourceAttr(addr, "ttl", "3600"),
					resource.TestCheckResourceAttrPair(addr, "domain_id", "opalstack_domain.d", "id"),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{ // content and ttl change in place
				Config:           config("A", "192.0.2.2", "ttl = 300"),
				ConfigPlanChecks: expect(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "content", "192.0.2.2"),
					resource.TestCheckResourceAttr(addr, "ttl", "300"),
					resource.TestCheckResourceAttr(addr, "priority", "10"),
				),
			},
			{ // a new type replaces the record
				Config:           config("TXT", "hello", "ttl = 300"),
				ConfigPlanChecks: expect(plancheck.ResourceActionDestroyBeforeCreate),
			},
			{ // deleted outside Terraform: the next plan recreates it
				PreConfig: func() {
					for _, id := range f.ids("dnsrecord") {
						f.edit(id, nil)
					}
				},
				Config:           config("TXT", "hello", "ttl = 300"),
				ConfigPlanChecks: expect(plancheck.ResourceActionCreate),
			},
		},
	})

	// Its own case: a config that does not validate cannot be destroyed either.
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config:      config("SPF", "hello", ""),
			ExpectError: regexp.MustCompile(`(?s)Invalid Attribute Value`),
		}},
	})
}
