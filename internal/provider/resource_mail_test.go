package provider

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

// filterFields are the mailbox attributes that version 0.2 added.
var filterFields = []string{"procmailrc", "autoresponder_enable", "autoresponder_subject", "autoresponder_message", "autoresponder_noreply"}

// A heredoc always ends in a newline, and the API stores the rules without it.
const filterConfig = `
  procmailrc = <<-EOT
    :0
    * ^Subject: tfacc
    tfacc/
  EOT
  autoresponder_enable  = true
  autoresponder_subject = "Away"
  autoresponder_message = "Back on Monday."
  autoresponder_noreply = ["list@example.org"]
`

const filterRules = ":0\n* ^Subject: tfacc\ntfacc/"

func boxConfig(extra string) string {
	return fmt.Sprintf(`
resource "opalstack_mailuser" "m" {
  name           = "tfacc-box"
  imap_server_id = %q
  %s
}
`, serverID, extra)
}

func mailConfig(boxExtra, source, addressExtra string) string {
	return boxConfig(boxExtra) + fmt.Sprintf(`
resource "opalstack_address" "a" {
  source = %q
  %s
}
`, source, addressExtra)
}

func TestMailResources(t *testing.T) {
	f, factories, block := fakeProvider(t)
	const box, address = "opalstack_mailuser.m", "opalstack_address.a"
	noPassword := []string{"default_password", "password"}
	delivered := "mailuser_ids = [opalstack_mailuser.m.id]"
	forwarded := `forwards = ["someone@example.org"]` + "\n  mailuser_ids = []"
	const password = `password = "first-Password1"`
	updates := 0

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
					// None of the filter fields is named: none is sent, and the API's defaults are read.
					resource.TestCheckResourceAttr(box, "procmailrc", ""),
					resource.TestCheckResourceAttr(box, "autoresponder_enable", "false"),
					resource.TestCheckResourceAttr(box, "autoresponder_noreply.#", "0"),
					resource.TestCheckResourceAttr(address, "mailuser_ids.#", "1"),
					resource.TestCheckResourceAttr(address, "forwards.#", "0"),
					func(*terraform.State) error {
						if body := f.lastBody("address/create"); !strings.Contains(body, `"forwards":[]`) {
							return fmt.Errorf("forwards must always be sent: %s", body)
						}
						return nil
					},
					f.sent("mailuser/create", nil, []string{"procmailrc", "autoresponder"}),
				),
			},
			{Config: block + mailConfig("", "tfacc-info@example.com", delivered), ConfigPlanChecks: accExpect(address, plancheck.ResourceActionNoop)},
			{ResourceName: box, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: noPassword},
			{ResourceName: box, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: noPassword, ImportStateId: "tfacc-box"},
			{ResourceName: address, ImportState: true, ImportStateVerify: true},
			{ResourceName: address, ImportState: true, ImportStateVerify: true, ImportStateId: "tfacc-info@example.com"},
			{ // a password in place; the address forwards instead of delivering
				Config: block + mailConfig(password, "tfacc-info@example.com", forwarded),
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
			{ // filter rules and autoresponder, set on an existing mailbox; the password is not sent again
				Config:           block + mailConfig(password+filterConfig, "tfacc-info@example.com", forwarded),
				ConfigPlanChecks: accExpect(box, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(box, "procmailrc", filterRules+"\n"), // as written
					resource.TestCheckResourceAttr(box, "autoresponder_enable", "true"),
					resource.TestCheckResourceAttr(box, "autoresponder_subject", "Away"),
					resource.TestCheckResourceAttr(box, "autoresponder_message", "Back on Monday."),
					resource.TestCheckTypeSetElemAttr(box, "autoresponder_noreply.*", "list@example.org"),
					func(*terraform.State) error {
						if got := f.get(f.ids("mailuser")[0])["procmailrc"]; got != filterRules {
							return fmt.Errorf("the fake stores the rules as %q", got)
						}
						return nil
					},
					f.sent("mailuser/update", []string{`"procmailrc":":0\n`}, []string{"password"}),
				),
			},
			{ResourceName: box, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: append(noPassword, "procmailrc")},
			{ // one field changed, two cleared, the autoresponder off; the rules are left out and stay
				Config: block + mailConfig(password+`
  autoresponder_enable  = false
  autoresponder_subject = "Gone"
  autoresponder_message = ""
  autoresponder_noreply = []`, "tfacc-info@example.com", forwarded),
				ConfigPlanChecks: accExpect(box, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(box, "procmailrc", filterRules+"\n"),
					resource.TestCheckResourceAttr(box, "autoresponder_enable", "false"),
					resource.TestCheckResourceAttr(box, "autoresponder_subject", "Gone"),
					resource.TestCheckResourceAttr(box, "autoresponder_message", ""),
					resource.TestCheckResourceAttr(box, "autoresponder_noreply.#", "0"),
					f.sent("mailuser/update",
						[]string{`"autoresponder_enable":false`, `"autoresponder_subject":"Gone"`, `"autoresponder_message":""`, `"autoresponder_noreply":[]`},
						[]string{"procmailrc", "password"}),
				),
			},
			{ // all five left out again: nothing changes and nothing is sent
				PreConfig:        func() { updates = f.wrote("mailuser/update") },
				Config:           block + mailConfig(password, "tfacc-info@example.com", forwarded),
				ConfigPlanChecks: accExpect(box, plancheck.ResourceActionNoop),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(box, "procmailrc", filterRules+"\n"),
					resource.TestCheckResourceAttr(box, "autoresponder_subject", "Gone"),
					func(*terraform.State) error {
						if n := f.wrote("mailuser/update") - updates; n != 0 {
							return fmt.Errorf("%d updates were sent for fields the configuration no longer names", n)
						}
						return nil
					},
				),
			},
			{ // another source is another address
				Config:           block + mailConfig(password, "tfacc-sales@example.com", delivered),
				ConfigPlanChecks: accExpect(address, plancheck.ResourceActionDestroyBeforeCreate),
			},
		},
	})
}

// Adopting an address with a configuration that names neither list must not
// empty them: a list the configuration leaves out stays as the API has it.
func TestAddressImportKeepsUnmentionedLists(t *testing.T) {
	f, factories, block := fakeProvider(t)
	const id, address = "80000000-0000-0000-0000-000000000001", "opalstack_address.a"
	f.seed("address", obj{"id": id, "source": "info@example.com", "destinations": []any{osuserID}, "forwards": []any{"someone@example.org"}})
	config := block + `
import {
  to = opalstack_address.a
  id = "info@example.com"
}
resource "opalstack_address" "a" { source = "info@example.com" }
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "mailuser_ids.#", "1"),
					resource.TestCheckResourceAttr(address, "forwards.#", "1"),
				),
			},
			{Config: block + `
removed {
  from = opalstack_address.a
  lifecycle {
    destroy = false
  }
}
`},
		},
	})
	if n := f.wrote("address/update"); n != 0 {
		t.Errorf("%d updates were sent for an address that was only imported", n)
	}
}

// A mailbox created with its filter rules and autoresponder: the create sends
// them, and the newline the API drops from the rules is not a change.
func TestMailUserFilterOnCreate(t *testing.T) {
	f, factories, block := fakeProvider(t)
	const box = "opalstack_mailuser.m"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		// The framework fails the step if a plan remains after it.
		Steps: []resource.TestStep{{
			Config: block + boxConfig(filterConfig),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(box, "procmailrc", filterRules+"\n"),
				resource.TestCheckResourceAttr(box, "autoresponder_enable", "true"),
				f.sent("mailuser/create", []string{`"procmailrc":":0\n`, `"autoresponder_enable":true`, `"autoresponder_noreply":["list@example.org"]`}, nil),
			),
		}},
	})
	if n := f.wrote("mailuser/update"); n != 0 {
		t.Errorf("%d updates were sent", n)
	}
}

// seedFilteredBox puts a mailbox with filter rules and an enabled autoresponder
// into the fake, and returns it as the fake holds it.
func seedFilteredBox(f *fakeAPI) obj {
	f.seed("mailuser", obj{"id": boxID, "name": "info", "imap_server": imapID})
	setFilter(f, boxID)
	return f.get(boxID)
}

func setFilter(f *fakeAPI, id string) {
	f.edit(id, func(o obj) {
		o["procmailrc"], o["autoresponder_enable"] = filterRules, true
		o["autoresponder_subject"], o["autoresponder_message"] = "Away", "Back on Monday."
		o["autoresponder_noreply"] = []any{"list@example.org"}
	})
}

// checkFilterRead is what the state must hold for such a mailbox.
func checkFilterRead(addr string) resource.TestCheckFunc {
	return resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckResourceAttr(addr, "procmailrc", filterRules),
		resource.TestCheckResourceAttr(addr, "autoresponder_enable", "true"),
		resource.TestCheckResourceAttr(addr, "autoresponder_subject", "Away"),
		resource.TestCheckResourceAttr(addr, "autoresponder_message", "Back on Monday."),
		resource.TestCheckTypeSetElemAttr(addr, "autoresponder_noreply.*", "list@example.org"),
	)
}

// Adopting a mailbox with a configuration that names none of the filter fields
// must leave them alone: no plan, no update, and the same object afterwards.
func TestMailUserImportKeepsUnmentionedFilter(t *testing.T) {
	f, factories, block := fakeProvider(t)
	before := seedFilteredBox(f)
	const box = "opalstack_mailuser.m"
	resourceBlock := `resource "opalstack_mailuser" "m" {
  name           = "info"
  imap_server_id = "` + imapID + `"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: block + `
import {
  to = opalstack_mailuser.m
  id = "info"
}
` + resourceBlock,
				Check: checkFilterRead(box),
			},
			{
				Config:           block + resourceBlock,
				ConfigPlanChecks: expectEmptyPlan(),
				Check:            checkFilterRead(box),
			},
			{ // naming what the mailbox already has is no change either, although the heredoc ends in a newline
				Config:           block + strings.Replace(resourceBlock, "}\n", filterConfig+"}\n", 1),
				ConfigPlanChecks: expectEmptyPlan(),
				Check:            checkFilterRead(box),
			},
			{Config: block + `
removed {
  from = opalstack_mailuser.m
  lifecycle {
    destroy = false
  }
}
`},
		},
	})
	if n := f.wrote("mailuser/update"); n != 0 {
		t.Errorf("%d updates were sent for a mailbox that was only imported", n)
	}
	if after := f.get(boxID); !reflect.DeepEqual(before, after) {
		t.Errorf("the mailbox changed:\nbefore %v\nafter  %v", before, after)
	}
}

// mailUser012 is the mailbox resource with the schema of version 0.1.2, which
// had none of the filter fields. It exists to write a state as that version did.
type mailUser012 struct{ mailUserResource }

type mailUser012Model struct {
	ID              types.String   `tfsdk:"id"`
	Name            types.String   `tfsdk:"name"`
	IMAPServerID    types.String   `tfsdk:"imap_server_id"`
	Password        types.String   `tfsdk:"password"`
	DefaultPassword types.String   `tfsdk:"default_password"`
	Timeouts        timeouts.Value `tfsdk:"timeouts"`
}

func (r *mailUser012) Schema(ctx context.Context, req fwresource.SchemaRequest, resp *fwresource.SchemaResponse) {
	r.mailUserResource.Schema(ctx, req, resp)
	for _, name := range filterFields {
		delete(resp.Schema.Attributes, name)
	}
}

func (r *mailUser012) Create(ctx context.Context, req fwresource.CreateRequest, resp *fwresource.CreateResponse) {
	var m mailUser012Model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.client.MailUsers().Create(ctx, client.MailUserCreate{IMAPServer: m.IMAPServerID.ValueString(), Name: m.Name.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the mailbox", err.Error())
		return
	}
	m.ID, m.DefaultPassword = types.StringValue(created.ID), types.StringValue(created.DefaultPassword)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Read keeps the state as it is: nothing changes the mailbox while this version runs.
func (r *mailUser012) Read(context.Context, fwresource.ReadRequest, *fwresource.ReadResponse) {}

// mailUserUnrefreshed is today's mailbox resource under `-refresh=false`,
// which the test framework cannot pass: its Read keeps the state as it is.
type mailUserUnrefreshed struct{ mailUserResource }

func (r *mailUserUnrefreshed) Read(context.Context, fwresource.ReadRequest, *fwresource.ReadResponse) {
}

// mailOnly is a provider for the fake with one mailbox resource in place of today's.
func mailOnly(f *fakeAPI, mailbox func() fwresource.Resource) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"opalstack": providerserver.NewProtocol6WithError(provider.Provider(oneResource{&opalstackProvider{version: "test", tune: f.tune}, mailbox})),
	}
}

type oneResource struct {
	*opalstackProvider
	resource func() fwresource.Resource
}

func (p oneResource) Resources(context.Context) []func() fwresource.Resource {
	return []func() fwresource.Resource{p.resource}
}

// The upgrade itself: a state written by version 0.1.2, a configuration that
// names none of the new fields, and a mailbox that has filter rules and an
// enabled autoresponder, set in the dashboard. The first plan of the new
// version must be empty, and a later change must send only what it changes.
func TestMailUserUpgradeFrom012(t *testing.T) {
	f, factories, block := fakeProvider(t)
	old := mailOnly(f, func() fwresource.Resource { return &mailUser012{} })
	const box = "opalstack_mailuser.m"
	config := func(extra string) string { return block + boxConfig(extra) }
	var before obj

	resource.UnitTest(t, resource.TestCase{
		Steps: []resource.TestStep{
			{ // version 0.1.2 creates the mailbox; its state has no filter field
				ProtoV6ProviderFactories: old,
				Config:                   config(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(box, "procmailrc"),
					resource.TestCheckNoResourceAttr(box, "autoresponder_enable"),
				),
			},
			{ // this version, the same configuration: nothing to do, and the fields are read
				PreConfig: func() {
					id := f.ids("mailuser")[0]
					setFilter(f, id)
					before = f.get(id)
				},
				ProtoV6ProviderFactories: factories,
				Config:                   config(""),
				ConfigPlanChecks:         expectEmptyPlan(),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkFilterRead(box),
					func(*terraform.State) error {
						if n := f.wrote("mailuser/update"); n != 0 {
							return fmt.Errorf("%d updates were sent by an upgrade", n)
						}
						if after := f.get(f.ids("mailuser")[0]); !reflect.DeepEqual(before, after) {
							return fmt.Errorf("the mailbox changed:\nbefore %v\nafter  %v", before, after)
						}
						return nil
					},
				),
			},
			{ // a password later on is sent alone
				ProtoV6ProviderFactories: factories,
				Config:                   config(`password = "first-Password1"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(box, plancheck.ResourceActionUpdate),
					// Planned as they are, not as "known after apply".
					plancheck.ExpectKnownValue(box, tfjsonpath.New("procmailrc"), knownvalue.StringExact(filterRules)),
					plancheck.ExpectKnownValue(box, tfjsonpath.New("autoresponder_enable"), knownvalue.Bool(true)),
					plancheck.ExpectKnownValue(box, tfjsonpath.New("autoresponder_subject"), knownvalue.StringExact("Away")),
					plancheck.ExpectKnownValue(box, tfjsonpath.New("autoresponder_message"), knownvalue.StringExact("Back on Monday.")),
					plancheck.ExpectKnownValue(box, tfjsonpath.New("autoresponder_noreply"), knownvalue.SetExact([]knownvalue.Check{knownvalue.StringExact("list@example.org")})),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					checkFilterRead(box),
					f.sent("mailuser/update", []string{"first-Password1"}, []string{"procmailrc", "autoresponder"}),
				),
			},
		},
	})
}

// The same upgrade with a first apply that does not refresh: the state still
// has none of the filter fields when a password is set. The update must send
// the password alone and must not contradict its plan, and the next ordinary
// run reads the fields and plans nothing.
func TestMailUserUpgradeFrom012Unrefreshed(t *testing.T) {
	f, factories, block := fakeProvider(t)
	const box = "opalstack_mailuser.m"
	const password = `password = "first-Password1"`
	resource.UnitTest(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				ProtoV6ProviderFactories: mailOnly(f, func() fwresource.Resource { return &mailUser012{} }),
				Config:                   block + boxConfig(""),
			},
			{
				PreConfig:                func() { setFilter(f, f.ids("mailuser")[0]) },
				ProtoV6ProviderFactories: mailOnly(f, func() fwresource.Resource { return &mailUserUnrefreshed{} }),
				Config:                   block + boxConfig(password),
				ConfigPlanChecks:         accExpect(box, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(box, "procmailrc"),
					f.sent("mailuser/update", []string{"first-Password1"}, []string{"procmailrc", "autoresponder"}),
				),
			},
			{
				ProtoV6ProviderFactories: factories,
				Config:                   block + boxConfig(password),
				ConfigPlanChecks:         expectEmptyPlan(),
				Check:                    checkFilterRead(box),
			},
		},
	})
}
