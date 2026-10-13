package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

type certDataSource struct{ baseDataSource }

// certDataModel is the resource's model without the key and the timeouts.
type certDataModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Cert          types.String `tfsdk:"cert"`
	Intermediates types.String `tfsdk:"intermediates"`
	ExpDate       types.String `tfsdk:"exp_date"`
	SHA256        types.String `tfsdk:"sha256_fingerprint"`
}

func newCertDataSource() datasource.DataSource { return &certDataSource{} }

func (d *certDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cert"
}

func (d *certDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a TLS certificate by `id` or by `name`. The private key the API holds is never read. " +
			"The name of a certificate does not have to be unique: look it up by `id` when more than one has it.",
		Attributes: map[string]schema.Attribute{
			"id":                 lookupAttribute("UUID of the certificate."),
			"name":               lookupAttribute("Name of the certificate."),
			"cert":               computedString("The certificate, PEM encoded."),
			"intermediates":      computedString("The intermediate certificates, PEM encoded, if the certificate has any."),
			"exp_date":           computedString("When the certificate expires, as the API reports it."),
			"sha256_fingerprint": computedString("SHA-256 fingerprint of the certificate."),
		},
	}
}

func (d *certDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return idOr("name")
}

func (d *certDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m certDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cert, diags := find(ctx, d.client.Certs().ReadKind, "certificate", m.ID, func(c client.Cert) bool {
		return c.Name == m.Name.ValueString()
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The resource's model does the conversion, so the two cannot drift apart.
	// Its key stays null: no read carries one.
	var full certModel
	full.set(cert)
	m = certDataModel{
		ID: full.ID, Name: full.Name, Cert: full.Cert, Intermediates: full.Intermediates,
		ExpDate: full.ExpDate, SHA256: full.SHA256,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
