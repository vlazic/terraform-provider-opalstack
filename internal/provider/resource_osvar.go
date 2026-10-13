package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = (*osVarResource)(nil)
	_ resource.ResourceWithImportState = (*osVarResource)(nil)
)

type osVarResource struct{ baseResource }

type osVarModel struct {
	ID        types.String   `tfsdk:"id"`
	Name      types.String   `tfsdk:"name"`
	Content   types.String   `tfsdk:"content"`
	OSUserIDs types.Set      `tfsdk:"osuser_ids"`
	Global    types.Bool     `tfsdk:"global"`
	Timeouts  timeouts.Value `tfsdk:"timeouts"`
}

func newOSVarResource() resource.Resource { return &osVarResource{} }

func (r *osVarResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_osvar"
}

var osVarNamePattern = regexp.MustCompile(`^[A-Za-z0-9_+-]*$`)

func (r *osVarResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An environment variable of shell users. It is given to the shell users in " +
			"`osuser_ids`, or to every shell user of the account when `global` is set.\n\n" +
			"The name of a variable does not have to be unique: the API accepts a second variable of the same " +
			"name for the same shell user. The API returns the content on every read, and it is stored in the " +
			"Terraform state.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "UUID of the variable.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Name of the variable: letters, digits, `_`, `+` and `-`, at most 128 characters. " +
					"Changing it replaces the variable.",
				Required: true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 128),
					stringvalidator.RegexMatches(osVarNamePattern, "may hold only letters, digits, _, + and -"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"content": schema.StringAttribute{
				MarkdownDescription: "Value of the variable, at most 1024 characters. Left out, a new variable is " +
					"empty and an existing one keeps the value the API has.",
				Optional:      true,
				Computed:      true,
				Sensitive:     true,
				Validators:    []validator.String{stringvalidator.LengthAtMost(1024)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"osuser_ids": idSetAttribute("UUIDs of the shell users the variable is given to. It may be empty: " +
				"the variable then belongs to nobody."),
			"global": schema.BoolAttribute{
				MarkdownDescription: "Whether the variable is given to every shell user of the account, present and " +
					"future. Left out, a new variable is not global and an existing one stays as the API has it. " +
					"This has not been verified against the live API, and the provider refuses it while " +
					"`safety_prefix` is set.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (m *osVarModel) set(ctx context.Context, v *client.OSVar) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.StringValue(v.ID)
	m.Name = types.StringValue(v.Name)
	m.Content = types.StringValue(v.Content)
	m.OSUserIDs = stringSet(ctx, v.OSUsers, &diags)
	m.Global = types.BoolValue(v.Global)
	return diags
}

func (r *osVarResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m osVarModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	osusers := stringsOf(ctx, m.OSUserIDs, &resp.Diagnostics)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Create, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	create := client.OSVarCreate{Name: m.Name.ValueString(), Content: m.Content.ValueString(), OSUsers: osusers}
	// Sent only when asked for: what the API makes of the field is not verified.
	if m.Global.ValueBool() {
		create.Global = m.Global.ValueBoolPointer()
	}
	vars := r.client.OSVars()
	created, err := vars.Create(ctx, create)
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the environment variable", err.Error())
		return
	}
	// In state before the wait, so a wait that fails does not leave an orphan.
	resp.Diagnostics.Append(m.set(ctx, created)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)

	ready, err := vars.WaitReady(ctx, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("The environment variable did not become ready", err.Error())
		return
	}
	resp.Diagnostics.Append(m.set(ctx, ready)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *osVarResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m osVarModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v := readForState(ctx, r.client.OSVars().ReadKind, "environment variable", m.ID.ValueString(), resp)
	if v == nil {
		return
	}
	resp.Diagnostics.Append(m.set(ctx, v)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update sends only what the plan changes: the API keeps a field that is left out.
func (r *osVarResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, state osVarModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	update := client.OSVarUpdate{ID: m.ID.ValueString()}
	if !m.Content.Equal(state.Content) {
		update.Content = knownString(m.Content)
	}
	if !m.OSUserIDs.Equal(state.OSUserIDs) {
		update.OSUsers = knownStrings(ctx, m.OSUserIDs, &resp.Diagnostics)
	}
	if !m.Global.Equal(state.Global) {
		update.Global = knownBool(m.Global)
	}
	ctx, cancel := withTimeout(ctx, m.Timeouts.Update, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}
	// Nothing is sent when only the timeouts changed.
	if update == (client.OSVarUpdate{ID: update.ID}) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		return
	}

	vars := r.client.OSVars()
	if _, err := vars.Update(ctx, update); err != nil {
		resp.Diagnostics.AddError("Cannot update the environment variable", err.Error())
		return
	}
	ready, err := vars.WaitReady(ctx, m.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("The environment variable did not become ready", err.Error())
		return
	}
	resp.Diagnostics.Append(m.set(ctx, ready)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *osVarResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	deleteResource(ctx, req, resp, r.client.OSVars(), "environment variable")
}

// A variable is imported by its UUID or by its name. A name is not unique, and
// one that several variables carry is an error, never a pick.
func (r *osVarResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importByIDOrName(ctx, r.client.OSVars().ReadKind, "environment variable",
		func(v client.OSVar) (string, string) { return v.Name, v.ID }, req, resp)
}
