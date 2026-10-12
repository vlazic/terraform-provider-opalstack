package provider

import (
	"cmp"
	"context"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

type serverDataSource struct{ baseDataSource }

type serverModel struct {
	ID       types.String `tfsdk:"id"`
	Hostname types.String `tfsdk:"hostname"`
	Type     types.String `tfsdk:"type"`
}

func newServerDataSource() datasource.DataSource { return &serverDataSource{} }

func (d *serverDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server"
}

func (d *serverDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a server of the account by `id` or by `hostname`.",
		Attributes: map[string]schema.Attribute{
			"id":       lookupAttribute("UUID of the server."),
			"hostname": lookupAttribute("Hostname of the server, such as `opal1.opalstack.com`."),
			"type":     computedString("Type of the server, as the API reports it."),
		},
	}
}

func (d *serverDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return idOr("hostname")
}

func (d *serverDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m serverModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var server *client.Server
	if id := m.ID.ValueString(); id != "" {
		var err error
		if server, err = d.client.ReadServer(ctx, id); err != nil {
			resp.Diagnostics.AddError("Cannot read the server", err.Error())
			return
		}
	} else {
		list, err := d.client.ListServers(ctx)
		if err != nil {
			resp.Diagnostics.AddError("Cannot list the servers", err.Error())
			return
		}
		// One machine can be listed under more than one role.
		all := slices.Concat(list.Web, list.IMAP, list.SMTP)
		slices.SortFunc(all, func(a, b client.Server) int { return cmp.Compare(a.ID, b.ID) })
		all = slices.CompactFunc(all, func(a, b client.Server) bool { return a.ID == b.ID })
		found, diags := only(all, "server", func(s client.Server) bool { return s.Hostname == m.Hostname.ValueString() })
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		server = found
	}

	m.ID = types.StringValue(server.ID)
	m.Hostname = types.StringValue(server.Hostname)
	m.Type = types.StringValue(server.Type)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
