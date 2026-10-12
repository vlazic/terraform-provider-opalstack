package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

type ipDataSource struct{ baseDataSource }

type ipModel struct {
	ID       types.String `tfsdk:"id"`
	ServerID types.String `tfsdk:"server_id"`
	Type     types.String `tfsdk:"type"`
	Primary  types.Bool   `tfsdk:"primary"`
	IP       types.String `tfsdk:"ip"`
}

func newIPDataSource() datasource.DataSource { return &ipDataSource{} }

func (d *ipDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ip"
}

func (d *ipDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up an IP address of a server, for use as the `ip4_id` or `ip6_id` of a site.",
		Attributes: map[string]schema.Attribute{
			"id":        computedString("UUID of the address."),
			"server_id": schema.StringAttribute{MarkdownDescription: "UUID of the server the address belongs to.", Required: true},
			"type": schema.StringAttribute{
				MarkdownDescription: "Address family, `4` or `6`. Defaults to `4`.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{stringvalidator.OneOf("4", "6")},
			},
			"primary": schema.BoolAttribute{
				MarkdownDescription: "Whether to look for the server's primary address. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
			},
			"ip": computedString("The address."),
		},
	}
}

func (d *ipDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m ipModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	family, primary := "4", true
	if !m.Type.IsNull() {
		family = m.Type.ValueString()
	}
	if !m.Primary.IsNull() {
		primary = m.Primary.ValueBool()
	}

	ip, diags := find(ctx, d.client.IPs(), "IP address", types.StringNull(), func(ip client.IP) bool {
		return ip.Server == m.ServerID.ValueString() && ip.Type == family && ip.Primary == primary
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	m.ID = types.StringValue(ip.ID)
	m.Type = types.StringValue(ip.Type)
	m.Primary = types.BoolValue(ip.Primary)
	m.IP = types.StringValue(ip.IP)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
