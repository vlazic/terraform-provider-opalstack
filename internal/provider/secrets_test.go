package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp/packet"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// passwordKind is one of the four resources with a password, as the tests of
// password_wo and pgp_key need it.
type passwordKind struct {
	kind   string // "osuser"
	config func(extra string) string
	// other is a change that is not the password, and otherWrites the number
	// of requests it sends.
	other       string
	otherWrites int
	// resent is whether a create with a password sends it again in an update.
	resent bool
	// new is today's resource.
	new func() fwresource.Resource
}

func (k passwordKind) addr() string { return "opalstack_" + k.kind + ".u" }

func passwordKinds() []passwordKind {
	block := func(kind, server string) func(string) string {
		return func(extra string) string {
			return fmt.Sprintf(`
resource "opalstack_%s" "u" {
  name = "tfacc-one"
  %s = %q
  %s
}
`, kind, server, serverID, extra)
		}
	}
	return []passwordKind{
		{"osuser", block("osuser", "server_id"), `timeouts = { update = "5m" }`, 0, false, newOSUserResource},
		{"mariauser", block("mariauser", "server_id"), "external = true", 1, true, newMariaUserResource},
		{"psqluser", block("psqluser", "server_id"), "external = true", 1, true, newPsqlUserResource},
		{"mailuser", block("mailuser", "imap_server_id"), `autoresponder_subject = "Away"`, 1, true, newMailUserResource},
	}
}

func writeOnly(password string, version int) string {
	return fmt.Sprintf("password_wo = %q\n  password_wo_version = %d\n  ", password, version)
}

// secretCheck fails a plan or a state that holds a secret in any attribute of
// any resource. Both are read as `terraform show -json` prints them.
type secretCheck struct{ secret string }

func (c secretCheck) find(v any) error {
	text, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if strings.Contains(string(text), c.secret) {
		return fmt.Errorf("%q is in it", c.secret)
	}
	return nil
}

// CheckPlan reads the planned values and changes. The configuration that the
// JSON plan also holds has the literal of the test, which no plan file stores.
func (c secretCheck) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	if err := c.find([]any{req.Plan.PriorState, req.Plan.PlannedValues, req.Plan.ResourceChanges}); err != nil {
		resp.Error = fmt.Errorf("the plan: %w", err)
	}
}

func (c secretCheck) CheckState(_ context.Context, req statecheck.CheckStateRequest, resp *statecheck.CheckStateResponse) {
	if err := c.find(req.State); err != nil {
		resp.Error = fmt.Errorf("the state: %w", err)
	}
}

// neverKept checks that the plan and the state of a step hold none of the secrets.
func neverKept(addr string, secrets ...string) (resource.ConfigPlanChecks, []statecheck.StateCheck) {
	plan := []plancheck.PlanCheck{plancheck.ExpectKnownValue(addr, tfjsonpath.New("password_wo"), knownvalue.Null())}
	state := []statecheck.StateCheck{statecheck.ExpectKnownValue(addr, tfjsonpath.New("password_wo"), knownvalue.Null())}
	for _, s := range secrets {
		plan = append(plan, secretCheck{s})
		state = append(state, secretCheck{s})
	}
	return resource.ConfigPlanChecks{PreApply: plan}, state
}

// A write-only password is sent on create, sent again only when its version
// changes, and is never in a plan or a state.
func TestPasswordWriteOnly(t *testing.T) {
	const first, second = "wo-first-Password1", "wo-second-Password2"
	for _, k := range passwordKinds() {
		t.Run(k.kind, func(t *testing.T) {
			f, factories, block := fakeProvider(t)
			addr, create, update := k.addr(), k.kind+"/create", k.kind+"/update"
			config := func(extra string) string { return block + k.config(extra) }
			writes := 0
			count := func() { writes = f.wrote(create) + f.wrote(update) }
			// sentSince checks the number of writes since count, and that the
			// last update carries the password or does not.
			sentSince := func(want int, password string) resource.TestCheckFunc {
				return func(*terraform.State) error {
					if n := f.wrote(create) + f.wrote(update) - writes; n != want {
						return fmt.Errorf("%d writes were sent, want %d", n, want)
					}
					if want == 0 {
						return nil
					}
					if body := f.lastBody(update); password != "" && !strings.Contains(body, `"password":"`+password+`"`) {
						return fmt.Errorf("the update lacks the password: %s", body)
					} else if password == "" && strings.Contains(body, "password") {
						return fmt.Errorf("the update sends a password: %s", body)
					}
					return nil
				}
			}
			firstPlan, firstState := neverKept(addr, first)
			secondPlan, secondState := neverKept(addr, first, second)
			onCreate := 0 // updates that follow the create
			if k.resent {
				onCreate = 1
			}

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories,
				Steps: []resource.TestStep{
					{
						Config:           config(writeOnly(first, 1)),
						ConfigPlanChecks: firstPlan,
						ConfigStateChecks: append(firstState,
							statecheck.ExpectKnownValue(addr, tfjsonpath.New("password_wo_version"), knownvalue.Int64Exact(1)),
							statecheck.ExpectKnownValue(addr, tfjsonpath.New("password"), knownvalue.Null()),
							// A write-only password without pgp_key keeps the API's own out of the state too.
							statecheck.ExpectKnownValue(addr, tfjsonpath.New("default_password"), knownvalue.Null()),
						),
						Check: resource.ComposeAggregateTestCheckFunc(
							f.sent(create, []string{`"password":"` + first + `"`}, nil),
							func(*terraform.State) error {
								if n := f.wrote(update); n != onCreate {
									return fmt.Errorf("%d updates followed the create, want %d", n, onCreate)
								}
								if k.resent && !strings.Contains(f.lastBody(update), first) {
									return fmt.Errorf("the update after the create lacks the password: %s", f.lastBody(update))
								}
								return nil
							},
						),
					},
					{ // another value under the same version is no change: nothing is planned or sent
						PreConfig:        count,
						Config:           config(writeOnly(second, 1)),
						ConfigPlanChecks: expectEmptyPlan(),
						Check:            sentSince(0, ""),
					},
					{ // a change to something else does not send the password
						PreConfig:         count,
						Config:            config(writeOnly(second, 1) + k.other),
						ConfigPlanChecks:  accExpect(addr, plancheck.ResourceActionUpdate),
						ConfigStateChecks: secondState,
						Check:             sentSince(k.otherWrites, ""),
					},
					{ // a new version sends the password the configuration has now
						PreConfig: count,
						Config:    config(writeOnly(second, 2) + k.other),
						ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: append(secondPlan.PreApply,
							plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate))},
						ConfigStateChecks: append(secondState,
							statecheck.ExpectKnownValue(addr, tfjsonpath.New("password_wo_version"), knownvalue.Int64Exact(2))),
						Check: sentSince(1, second),
					},
					{ // both taken out: the version leaves the state, and nothing is sent
						PreConfig:        count,
						Config:           config(k.other),
						ConfigPlanChecks: accExpect(addr, plancheck.ResourceActionUpdate),
						ConfigStateChecks: []statecheck.StateCheck{
							statecheck.ExpectKnownValue(addr, tfjsonpath.New("password_wo_version"), knownvalue.Null()),
						},
						Check: sentSince(0, ""),
					},
				},
			})
		})
	}
}

// The validators refuse, at plan time, what the attributes cannot mean.
func TestPasswordValidators(t *testing.T) {
	key := testPGPKey(t, nil)
	words := func(text string) *regexp.Regexp {
		return regexp.MustCompile(strings.Join(strings.Fields(regexp.QuoteMeta(text)), `\s+`))
	}
	for _, k := range passwordKinds() {
		t.Run(k.kind, func(t *testing.T) {
			f, factories, block := fakeProvider(t)
			step := func(extra, want string) resource.TestStep {
				return resource.TestStep{Config: block + k.config(extra), PlanOnly: true, ExpectError: words(want)}
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories,
				Steps: []resource.TestStep{
					step(`password = "a"`+"\n  "+writeOnly("b", 1), `"password" cannot be specified when "password_wo" is specified`),
					step(`password_wo = "b"`, `"password_wo_version" must be specified when "password_wo" is specified`),
					step(`password_wo_version = 1`, `"password_wo" must be specified when "password_wo_version" is specified`),
					// An empty value, such as a secret store that answered nothing, would move the version and send nothing.
					step(writeOnly("", 1), "string length must be at least 1"),
					step(`pgp_key = "not a key!"`, "neither an ASCII-armored key nor base64"),
					step(fmt.Sprintf("pgp_key = %q", armored(t, key, true)), "is a private key"),
				},
			})
			if n := len(f.writes); n != 0 {
				t.Errorf("%d writes were sent", n)
			}
		})
	}
}

// unchecked is one of today's resources whose pgp_key has no validator, so an
// unusable key reaches the create, as one that expired since the plan would.
type unchecked struct{ before030 }

func (r unchecked) Schema(ctx context.Context, req fwresource.SchemaRequest, resp *fwresource.SchemaResponse) {
	r.inner.Schema(ctx, req, resp)
	key := resp.Schema.Attributes["pgp_key"].(schema.StringAttribute)
	key.Validators = nil
	resp.Schema.Attributes["pgp_key"] = key
}

func (r unchecked) Create(ctx context.Context, req fwresource.CreateRequest, resp *fwresource.CreateResponse) {
	r.inner.Create(ctx, req, resp)
}

// The create checks the key again, before anything is sent.
func TestPGPKeyCheckedBeforeCreate(t *testing.T) {
	expired := testPGPKey(t, &packet.Config{Time: func() time.Time { return time.Now().Add(-48 * time.Hour) }, KeyLifetimeSecs: 3600})
	for _, k := range passwordKinds() {
		t.Run(k.kind, func(t *testing.T) {
			f, _, block := fakeProvider(t)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: withOnly(f, func() fwresource.Resource { return unchecked{before030{k.new()}} }),
				Steps: []resource.TestStep{{
					Config:      block + k.config(fmt.Sprintf("pgp_key = %q", armored(t, expired, false))),
					ExpectError: regexp.MustCompile(`the key has expired`),
				}},
			})
			if n := len(f.writes); n != 0 {
				t.Errorf("%d writes were sent", n)
			}
		})
	}
}

// With pgp_key, the password the API chooses is kept only encrypted to the key.
// A new key later changes nothing but the attribute itself.
func TestPGPKeyEncryptsTheDefaultPassword(t *testing.T) {
	key, next := testPGPKey(t, nil), testPGPKey(t, nil)
	fingerprint := fmt.Sprintf("%X", key.PrimaryKey.Fingerprint)
	for _, k := range passwordKinds() {
		t.Run(k.kind, func(t *testing.T) {
			f, factories, block := fakeProvider(t)
			addr := k.addr()
			config := func(pgpKey string) string { return block + k.config(fmt.Sprintf("pgp_key = %q", pgpKey)) }
			encrypted := resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckNoResourceAttr(addr, "default_password"),
				resource.TestCheckResourceAttr(addr, "key_fingerprint", fingerprint),
				func(s *terraform.State) error {
					got, err := pgpDecrypt(key, s.RootModule().Resources[addr].Primary.Attributes["encrypted_default_password"])
					if err != nil {
						return err
					}
					if got != "fake-default-password" {
						return fmt.Errorf("decrypted %q", got)
					}
					return nil
				},
			)
			clear := secretCheck{"fake-default-password"}

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories,
				Steps: []resource.TestStep{
					{
						Config:            config(armored(t, key, false)),
						ConfigStateChecks: []statecheck.StateCheck{clear},
						Check:             encrypted,
					},
					{Config: config(armored(t, key, false)), ConfigPlanChecks: expectEmptyPlan()},
					{ // another key, in the other form: stored, and nothing is encrypted again or sent
						Config: config(binaryBase64(t, next)),
						ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
							plancheck.ExpectKnownValue(addr, tfjsonpath.New("key_fingerprint"), knownvalue.StringExact(fingerprint)),
							plancheck.ExpectKnownValue(addr, tfjsonpath.New("default_password"), knownvalue.Null()),
						}},
						ConfigStateChecks: []statecheck.StateCheck{clear},
						Check: resource.ComposeAggregateTestCheckFunc(encrypted, func(*terraform.State) error {
							if n := f.wrote(k.kind + "/update"); n != 0 {
								return fmt.Errorf("%d updates were sent", n)
							}
							return nil
						}),
					},
					{ // imported, none of the three is known
						ResourceName: addr, ImportState: true, ImportStateVerify: true,
						ImportStateVerifyIgnore: []string{"pgp_key", "encrypted_default_password", "key_fingerprint", "default_password"},
						ImportStateCheck: func(states []*terraform.InstanceState) error {
							for _, name := range []string{"pgp_key", "encrypted_default_password", "key_fingerprint", "default_password"} {
								if v, ok := states[0].Attributes[name]; ok && v != "" {
									return fmt.Errorf("an imported %s has %s", k.kind, name)
								}
							}
							return nil
						},
					},
				},
			})
		})
	}
}

// before030 is one of today's resources with the schema of version 0.2.0,
// which had none of passwordAttributes. Its Create is today's, run against
// today's schema with those attributes null, and its result narrowed to the
// old schema: a state as version 0.2.0 wrote it.
type before030 struct{ inner fwresource.Resource }

func (r before030) Metadata(ctx context.Context, req fwresource.MetadataRequest, resp *fwresource.MetadataResponse) {
	r.inner.Metadata(ctx, req, resp)
}

func (r before030) Schema(ctx context.Context, req fwresource.SchemaRequest, resp *fwresource.SchemaResponse) {
	r.inner.Schema(ctx, req, resp)
	for name := range passwordAttributes("") {
		delete(resp.Schema.Attributes, name)
	}
}

func (r before030) Configure(ctx context.Context, req fwresource.ConfigureRequest, resp *fwresource.ConfigureResponse) {
	r.inner.(fwresource.ResourceWithConfigure).Configure(ctx, req, resp)
}

func (r before030) Create(ctx context.Context, req fwresource.CreateRequest, resp *fwresource.CreateResponse) {
	var today fwresource.SchemaResponse
	r.inner.Schema(ctx, fwresource.SchemaRequest{}, &today)
	s := today.Schema
	wide := fwresource.CreateResponse{State: tfsdk.State{Schema: s, Raw: reshape(ctx, resp.State.Raw, s)}}
	r.inner.Create(ctx, fwresource.CreateRequest{
		Config: tfsdk.Config{Schema: s, Raw: reshape(ctx, req.Config.Raw, s)},
		Plan:   tfsdk.Plan{Schema: s, Raw: reshape(ctx, req.Plan.Raw, s)},
	}, &wide)
	resp.Diagnostics.Append(wide.Diagnostics...)
	resp.State.Raw = reshape(ctx, wide.State.Raw, resp.State.Schema.(schema.Schema))
}

// Read keeps the state as it is: nothing changes the object while this version runs.
func (before030) Read(context.Context, fwresource.ReadRequest, *fwresource.ReadResponse) {}

func (before030) Update(_ context.Context, _ fwresource.UpdateRequest, resp *fwresource.UpdateResponse) {
	resp.Diagnostics.AddError("Not part of the test", "The resource of version 0.2.0 only creates.")
}

func (r before030) Delete(ctx context.Context, req fwresource.DeleteRequest, resp *fwresource.DeleteResponse) {
	r.inner.Delete(ctx, req, resp)
}

// reshape returns an object value with the attributes of a schema: those v
// has, and null for the rest.
func reshape(ctx context.Context, v tftypes.Value, s schema.Schema) tftypes.Value {
	typ := s.Type().TerraformType(ctx).(tftypes.Object)
	var attrs map[string]tftypes.Value
	if v.IsNull() || v.As(&attrs) != nil {
		return tftypes.NewValue(typ, nil)
	}
	out := map[string]tftypes.Value{}
	for name, t := range typ.AttributeTypes {
		if a, ok := attrs[name]; ok {
			out[name] = a
		} else {
			out[name] = tftypes.NewValue(t, nil)
		}
	}
	return tftypes.NewValue(typ, out)
}

// The upgrade: a state written by version 0.2.0, with a password in it, and
// the same configuration. The first plan of this version must be empty, and a
// later password change must not show the new attributes as unknown.
func TestPasswordUpgradeFrom020(t *testing.T) {
	for _, k := range passwordKinds() {
		t.Run(k.kind, func(t *testing.T) {
			f, factories, block := fakeProvider(t)
			old := withOnly(f, func() fwresource.Resource { return before030{k.new()} })
			addr := k.addr()
			config := func(password string) string { return block + k.config(fmt.Sprintf("password = %q", password)) }
			writes := 0
			unchanged := []plancheck.PlanCheck{}
			for _, name := range []string{"password_wo_version", "pgp_key", "encrypted_default_password", "key_fingerprint"} {
				unchanged = append(unchanged, plancheck.ExpectKnownValue(addr, tfjsonpath.New(name), knownvalue.Null()))
			}

			resource.UnitTest(t, resource.TestCase{
				Steps: []resource.TestStep{
					{ // version 0.2.0 creates the object; its state has none of the new attributes
						ProtoV6ProviderFactories: old,
						Config:                   config("first-Password1"),
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr(addr, "default_password", "fake-default-password"),
							resource.TestCheckNoResourceAttr(addr, "password_wo_version"),
							resource.TestCheckNoResourceAttr(addr, "key_fingerprint"),
						),
					},
					{ // this version, the same configuration: nothing to do
						PreConfig:                func() { writes = len(f.writes) },
						ProtoV6ProviderFactories: factories,
						Config:                   config("first-Password1"),
						ConfigPlanChecks:         expectEmptyPlan(),
						Check: func(*terraform.State) error {
							if n := len(f.writes) - writes; n != 0 {
								return fmt.Errorf("%d writes were sent by an upgrade", n)
							}
							return nil
						},
					},
					{ // a new password is sent alone, and the new attributes are planned null, not unknown
						ProtoV6ProviderFactories: factories,
						Config:                   config("second-Password2"),
						ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: append(unchanged,
							plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
							plancheck.ExpectKnownValue(addr, tfjsonpath.New("default_password"), knownvalue.StringExact("fake-default-password")),
						)},
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr(addr, "default_password", "fake-default-password"),
							f.sent(k.kind+"/update", []string{`"password":"second-Password2"`}, []string{"external", "autoresponder"}),
						),
					},
				},
			})
		})
	}
}
