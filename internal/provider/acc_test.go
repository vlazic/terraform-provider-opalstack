package provider

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

// The acceptance tests run against a real account, and only with TF_ACC set.
// They create, change and delete objects whose names start with accPrefix, and
// the provider is configured to refuse any other write. The sandbox comes from
// the environment, so no name of the account is written here:
//
//	OPALSTACK_TOKEN             a token for the account
//	OPALSTACK_TEST_DOMAIN_BASE  an existing domain; test domains are created under it
const accPrefix = "tfacc-"

var accFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"opalstack": providerserver.NewProtocol6WithError(New("acctest")()),
}

const accProvider = `
provider "opalstack" {
  safety_prefix = "` + accPrefix + `"
}
`

func accEnv(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" && os.Getenv(resource.EnvTfAcc) != "" {
		t.Fatalf("%s must be set for acceptance tests", name)
	}
	return v
}

// accName returns accPrefix plus six random lowercase alphanumerics.
func accName() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := []byte(accPrefix)
	for range 6 {
		b = append(b, chars[rand.IntN(len(chars))])
	}
	return string(b)
}

func accClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{Token: os.Getenv(tokenEnvVar), SafetyPrefix: accPrefix})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAccDomain(t *testing.T) {
	base := accEnv(t, "OPALSTACK_TEST_DOMAIN_BASE")
	accEnv(t, tokenEnvVar)
	first, second := accName()+"."+base, accName()+"."+base
	const addr = "opalstack_domain.d"
	expect := func(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
		return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, action)}}
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories,
		CheckDestroy: func(*terraform.State) error {
			domains, err := accClient(t).Domains().List(context.Background())
			if err != nil {
				return err
			}
			for _, d := range domains {
				if strings.HasPrefix(d.Name, accPrefix) {
					return fmt.Errorf("a test domain was left behind (state %s)", d.State)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: accProvider + `resource "opalstack_domain" "d" { name = "` + first + `" }`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", first),
					resource.TestCheckResourceAttr(addr, "delegated_dns_zone", "false"),
					resource.TestCheckResourceAttrSet(addr, "id"),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateId: first},
			// No in-place update here: delegated_dns_zone is the only field that can
			// change, and the API refuses it for a name under a domain the account
			// already has, which every test domain is. See API_FINDINGS.md.
			{
				Config:           accProvider + `resource "opalstack_domain" "d" { name = "` + second + `" }`,
				ConfigPlanChecks: expect(plancheck.ResourceActionDestroyBeforeCreate),
				Check:            resource.TestCheckResourceAttr(addr, "name", second),
			},
			{ // deleted outside Terraform: the next plan recreates it
				PreConfig: func() {
					ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
					defer cancel()
					domains := accClient(t).Domains()
					all, err := domains.List(ctx)
					if err != nil {
						t.Fatal(err)
					}
					for _, d := range all {
						if d.Name != second {
							continue
						}
						if err := domains.Delete(ctx, d.ID); err != nil {
							t.Fatal(err)
						}
						if err := domains.WaitDeleted(ctx, d.ID); err != nil {
							t.Fatal(err)
						}
					}
				},
				Config:           accProvider + `resource "opalstack_domain" "d" { name = "` + second + `" }`,
				ConfigPlanChecks: expect(plancheck.ResourceActionCreate),
			},
		},
	})
}
