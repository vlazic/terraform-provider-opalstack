package provider

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = (*appResource)(nil)
	_ resource.ResourceWithImportState = (*appResource)(nil)
)

// portType is the one app type that listens on a port of its own.
const portType = "CUS"

type appResource struct{ baseResource }

type appModel struct {
	ID           types.String         `tfsdk:"id"`
	Name         types.String         `tfsdk:"name"`
	OSUserID     types.String         `tfsdk:"osuser_id"`
	OSUserName   types.String         `tfsdk:"osuser_name"`
	ServerID     types.String         `tfsdk:"server_id"`
	Type         types.String         `tfsdk:"type"`
	Port         types.Int64          `tfsdk:"port"`
	InstallerURL types.String         `tfsdk:"installer_url"`
	JSON         jsontypes.Normalized `tfsdk:"json"`
	Timeouts     timeouts.Value       `tfsdk:"timeouts"`
}

func newAppResource() resource.Resource { return &appResource{} }

func (r *appResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app"
}

func (r *appResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An app of a shell user: a directory, and for most types a process or a PHP pool, " +
			"that a site routes requests to. Deleting an app deletes its directory.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{MarkdownDescription: "UUID of the app.", Computed: true, PlanModifiers: keep},
			"name": schema.StringAttribute{
				MarkdownDescription: "Name of the app, which is also the name of its directory. Changing it replaces the app.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"osuser_id": schema.StringAttribute{
				MarkdownDescription: "UUID of the shell user the app belongs to. An app cannot be moved: changing this replaces it.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"osuser_name": schema.StringAttribute{MarkdownDescription: "Name of that shell user.", Computed: true, PlanModifiers: keep},
			"server_id":   schema.StringAttribute{MarkdownDescription: "UUID of the server the app runs on.", Computed: true, PlanModifiers: keep},
			"type": schema.StringAttribute{
				MarkdownDescription: "Type of the app: `STA` static, `NPF` Nginx with PHP-FPM, `APA` Apache with PHP-FPM, " +
					"`CUS` proxied port, `SLS`, `SLP` and `SLN` symbolic links, `SVN` or `DAV`. It changes in place, " +
					"except to or from `CUS`, which replaces the app: the API assigns a port only when it creates one.",
				Required:   true,
				Validators: []validator.String{stringvalidator.OneOf("STA", "NPF", "APA", "CUS", "SLS", "SLP", "SLN", "SVN", "DAV")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
						resp.RequiresReplace = req.StateValue.ValueString() == portType || req.PlanValue.ValueString() == portType
					},
					"Changing the type to or from CUS replaces the app.", "Changing the type to or from `CUS` replaces the app.",
				)},
			},
			"port": schema.Int64Attribute{
				MarkdownDescription: "Port the API assigned to the app when it was created as a `CUS` app.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"installer_url": schema.StringAttribute{
				MarkdownDescription: "URL of an installer script to run when the app is created. Changing it replaces the app.",
				Optional:            true,
				PlanModifiers:       replace,
			},
			"json": schema.StringAttribute{
				CustomType: jsontypes.NormalizedType{},
				MarkdownDescription: "Per-type settings as a JSON object, for example `jsonencode({ php_version = 83 })`. " +
					"Only the keys given here are managed: the API adds settings of its own, and those are left alone " +
					"and not shown. The `opalstack_app` data source shows all of them.",
				Optional:      true,
				PlanModifiers: []planmodifier.String{sameJSON{}},
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

// sameJSON keeps the state's value when the configuration says the same thing
// written another way, so that formatting is not planned as a change.
type sameJSON struct{}

func (sameJSON) Description(context.Context) string {
	return "Equal JSON written differently is not a change."
}
func (m sameJSON) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

func (sameJSON) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	same, diags := jsontypes.NewNormalizedValue(req.PlanValue.ValueString()).
		StringSemanticEquals(ctx, jsontypes.NewNormalizedValue(req.StateValue.ValueString()))
	resp.Diagnostics.Append(diags...)
	if same {
		resp.PlanValue = req.StateValue
	}
}

// settings decodes the json attribute. It is nil when the attribute is not set.
func (m *appModel) settings() (map[string]any, diag.Diagnostics) {
	if m.JSON.IsNull() || m.JSON.IsUnknown() {
		return nil, nil
	}
	var out map[string]any
	diags := m.JSON.Unmarshal(&out)
	if !diags.HasError() && out == nil {
		diags.AddError("Invalid json", "The json attribute must be a JSON object.")
	}
	return out, diags
}

// set copies what the API reports, except for json: see refreshJSON.
func (m *appModel) set(a *client.App) {
	m.ID = types.StringValue(a.ID)
	m.Name = types.StringValue(a.Name)
	m.OSUserID = types.StringValue(a.OSUser)
	m.OSUserName = types.StringValue(a.OSUserName)
	m.ServerID = types.StringValue(a.Server)
	m.Type = types.StringValue(a.Type)
	m.Port = types.Int64PointerValue(a.Port)
	m.InstallerURL = types.StringPointerValue(a.InstallerURL)
}

// refreshJSON is for Read only. The json attribute keeps its value as long as
// the API agrees with it on every key it names; otherwise it becomes what the
// API has for those keys, which shows as drift. Create and Update leave json
// alone, because Terraform requires an apply to return what was in the plan.
func (m *appModel) refreshJSON(ctx context.Context, a *client.App) diag.Diagnostics {
	managed, diags := m.settings()
	if managed == nil || diags.HasError() {
		return diags
	}
	actual := make(map[string]any, len(managed))
	for key := range managed {
		if v, ok := a.JSON[key]; ok {
			actual[key] = v
		}
	}
	raw, err := json.Marshal(actual)
	if err != nil {
		diags.AddError("Cannot encode the app's settings", err.Error())
		return diags
	}
	reported := jsontypes.NewNormalizedValue(string(raw))
	if equal, d := m.JSON.StringSemanticEquals(ctx, reported); !equal {
		diags.Append(d...)
		m.JSON = reported
	}
	return diags
}

func (r *appResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m appModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	settings, diags := m.settings()
	resp.Diagnostics.Append(diags...)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Create, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	apps := r.client.Apps()
	created, err := apps.Create(ctx, client.AppCreate{
		Name:         m.Name.ValueString(),
		OSUser:       m.OSUserID.ValueString(),
		Type:         m.Type.ValueString(),
		InstallerURL: m.InstallerURL.ValueStringPointer(),
		JSON:         settings,
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the app", err.Error())
		return
	}
	// In state before the wait, so a wait that fails does not leave an orphan.
	m.set(created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)

	ready, err := apps.WaitReady(ctx, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("The app did not become ready", err.Error())
		return
	}
	m.set(ready)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *appResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m appModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	app := readForState(ctx, r.client.Apps().ReadKind, "app", m.ID.ValueString(), resp)
	if app == nil {
		return
	}
	m.set(app)
	resp.Diagnostics.Append(m.refreshJSON(ctx, app)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *appResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m appModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	settings, diags := m.settings()
	resp.Diagnostics.Append(diags...)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Update, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	apps := r.client.Apps()
	if _, err := apps.Update(ctx, client.AppUpdate{ID: m.ID.ValueString(), Type: m.Type.ValueString(), JSON: settings}); err != nil {
		resp.Diagnostics.AddError("Cannot update the app", err.Error())
		return
	}
	ready, err := apps.WaitReady(ctx, m.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("The app did not become ready", err.Error())
		return
	}
	m.set(ready)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *appResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	deleteResource(ctx, req, resp, r.client.Apps(), "app")
}

func (r *appResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importByIDOrName(ctx, r.client.Apps().ReadKind, "app",
		func(a client.App) (string, string) { return a.Name, a.ID }, req, resp)
}
