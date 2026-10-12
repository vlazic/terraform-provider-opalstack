package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

type osuserDataSource struct{ baseDataSource }

type osuserDataModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	ServerID types.String `tfsdk:"server_id"`
}

func newOSUserDataSource() datasource.DataSource { return &osuserDataSource{} }

func (d *osuserDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_osuser"
}

func (d *osuserDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a shell user by `id` or by `name`.",
		Attributes: map[string]schema.Attribute{
			"id":        lookupAttribute("UUID of the shell user."),
			"name":      lookupAttribute("Name of the shell user."),
			"server_id": computedString("UUID of the server the shell user lives on."),
		},
	}
}

func (d *osuserDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return idOr("name")
}

func (d *osuserDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m osuserDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	user, diags := find(ctx, d.client.OSUsers().ReadKind, "shell user", m.ID, func(u client.OSUser) bool {
		return u.Name == m.Name.ValueString()
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	m.ID = types.StringValue(user.ID)
	m.Name = types.StringValue(user.Name)
	m.ServerID = types.StringValue(user.Server)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
