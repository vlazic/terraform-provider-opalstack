package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

type domainDataSource struct{ baseDataSource }

type domainDataModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	DelegatedDNSZone types.Bool   `tfsdk:"delegated_dns_zone"`
	IsValidHostname  types.Bool   `tfsdk:"is_valid_hostname"`
	DKIMRecord       types.String `tfsdk:"dkim_record"`
	DKIMPubkey       types.String `tfsdk:"dkim_pubkey"`
}

func newDomainDataSource() datasource.DataSource { return &domainDataSource{} }

func (d *domainDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain"
}

func (d *domainDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a domain by `id` or by `name`. The DKIM private key the API holds is never read.",
		Attributes: map[string]schema.Attribute{
			"id":                 lookupAttribute("UUID of the domain."),
			"name":               lookupAttribute("Fully qualified name of the domain."),
			"delegated_dns_zone": computedBool("Whether the domain is a DNS zone of its own, delegated from its parent."),
			"is_valid_hostname":  computedBool("Whether the name can be used as a hostname."),
			"dkim_record":        computedString("The DKIM TXT record for the domain, when one exists."),
			"dkim_pubkey":        computedString("The DKIM public key of the domain, when one exists."),
		},
	}
}

func (d *domainDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return idOr("name")
}

func (d *domainDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m domainDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domain, diags := find(ctx, d.client.Domains().ReadKind, "domain", m.ID, func(dom client.Domain) bool {
		return dom.Name == m.Name.ValueString()
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	m.ID = types.StringValue(domain.ID)
	m.Name = types.StringValue(domain.Name)
	m.DelegatedDNSZone = types.BoolValue(domain.DelegatedDNSZone)
	m.IsValidHostname = types.BoolValue(domain.IsValidHostname)
	m.DKIMRecord = types.StringPointerValue(domain.DKIMRecord)
	m.DKIMPubkey = types.StringPointerValue(domain.DKIMPubkey)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
