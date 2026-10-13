package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

type siteDataSource struct{ baseDataSource }

// siteDataModel is the resource's model without the timeouts.
type siteDataModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	ServerID        types.String `tfsdk:"server_id"`
	DomainIDs       types.Set    `tfsdk:"domain_ids"`
	Routes          types.Set    `tfsdk:"routes"`
	IP4ID           types.String `tfsdk:"ip4_id"`
	IP6ID           types.String `tfsdk:"ip6_id"`
	CertID          types.String `tfsdk:"cert_id"`
	PrimaryDomainID types.String `tfsdk:"primary_domain_id"`
	Redirect        types.Bool   `tfsdk:"redirect"`
	GenerateLE      types.Bool   `tfsdk:"generate_le"`
	Disabled        types.Bool   `tfsdk:"disabled"`
}

func newSiteDataSource() datasource.DataSource { return &siteDataSource{} }

func (d *siteDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site"
}

func (d *siteDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a site by `id` or by `name`.",
		Attributes: map[string]schema.Attribute{
			"id":         lookupAttribute("UUID of the site."),
			"name":       lookupAttribute("Name of the site."),
			"server_id":  computedString("UUID of the server the site is served from."),
			"domain_ids": computedStringSet("UUIDs of the domains the site answers for."),
			"routes": schema.SetNestedAttribute{
				MarkdownDescription: "Which app answers which path.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"app_id": computedString("UUID of the app."),
					"uri":    computedString("Path prefix the app answers."),
				}},
			},
			"ip4_id":            computedString("UUID of the IPv4 address the site listens on, if any."),
			"ip6_id":            computedString("UUID of the IPv6 address the site listens on, if any."),
			"cert_id":           computedString("UUID of the certificate the site serves, if any."),
			"primary_domain_id": computedString("UUID of the domain the others redirect to, if one is set."),
			"redirect":          computedBool("Whether the other domains redirect to the primary one."),
			"generate_le":       computedBool("Whether a Let's Encrypt certificate is requested for the site."),
			"disabled":          computedBool("Whether the site is switched off."),
		},
	}
}

func (d *siteDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return idOr("name")
}

func (d *siteDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m siteDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	site, diags := find(ctx, d.client.Sites().ReadKind, "site", m.ID, func(s client.Site) bool {
		return s.Name == m.Name.ValueString()
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The resource's model does the conversion, so the two cannot drift apart.
	var full siteModel
	resp.Diagnostics.Append(full.set(ctx, site)...)
	m = siteDataModel{
		ID: full.ID, Name: full.Name, ServerID: full.ServerID, DomainIDs: full.DomainIDs, Routes: full.Routes,
		IP4ID: full.IP4ID, IP6ID: full.IP6ID, CertID: full.CertID, PrimaryDomainID: full.PrimaryDomainID,
		Redirect: full.Redirect, GenerateLE: full.GenerateLE, Disabled: full.Disabled,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
