package provider

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

type appDataSource struct{ baseDataSource }

type appDataModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	OSUserID     types.String `tfsdk:"osuser_id"`
	OSUserName   types.String `tfsdk:"osuser_name"`
	ServerID     types.String `tfsdk:"server_id"`
	Type         types.String `tfsdk:"type"`
	Port         types.Int64  `tfsdk:"port"`
	InstallerURL types.String `tfsdk:"installer_url"`
	JSON         types.String `tfsdk:"json"`
}

func newAppDataSource() datasource.DataSource { return &appDataSource{} }

func (d *appDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app"
}

func (d *appDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up an app by `id` or by `name`. " +
			"Set `osuser_id` with `name` when the same name is used under more than one shell user.",
		Attributes: map[string]schema.Attribute{
			"id":            lookupAttribute("UUID of the app."),
			"name":          lookupAttribute("Name of the app."),
			"osuser_id":     lookupAttribute("UUID of the shell user the app belongs to. Narrows a lookup by `name`."),
			"osuser_name":   computedString("Name of the shell user the app belongs to."),
			"server_id":     computedString("UUID of the server the app runs on."),
			"type":          computedString("Type of the app, such as `STA` or `CUS`."),
			"port":          schema.Int64Attribute{MarkdownDescription: "Port assigned to the app, for the types that listen on one.", Computed: true},
			"installer_url": computedString("URL of the installer script the app was created with, if any."),
			"json": schema.StringAttribute{
				MarkdownDescription: "The per-type settings of the app, as a JSON object. Decode it with `jsondecode`. " +
					"Sensitive, because an installer can leave credentials among them.",
				Computed:  true,
				Sensitive: true,
			},
		},
	}
}

func (d *appDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return idOr("name")
}

func (d *appDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m appDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	app, diags := find(ctx, d.client.Apps().ReadKind, "app", m.ID, func(a client.App) bool {
		return a.Name == m.Name.ValueString() && (m.OSUserID.IsNull() || a.OSUser == m.OSUserID.ValueString())
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if id := m.OSUserID.ValueString(); id != "" && id != app.OSUser {
		resp.Diagnostics.AddError("The app belongs to another shell user",
			"The app with this id does not belong to the shell user given as osuser_id.")
		return
	}
	settings, err := json.Marshal(app.JSON)
	if err != nil {
		resp.Diagnostics.AddError("Cannot encode the app's settings", err.Error())
		return
	}

	m.ID = types.StringValue(app.ID)
	m.Name = types.StringValue(app.Name)
	m.OSUserID = types.StringValue(app.OSUser)
	m.OSUserName = types.StringValue(app.OSUserName)
	m.ServerID = types.StringValue(app.Server)
	m.Type = types.StringValue(app.Type)
	m.Port = types.Int64PointerValue(app.Port)
	m.InstallerURL = types.StringPointerValue(app.InstallerURL)
	m.JSON = types.StringValue(string(settings))
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
