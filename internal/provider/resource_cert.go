package provider

import (
	"context"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = (*certResource)(nil)
	_ resource.ResourceWithImportState = (*certResource)(nil)
)

type certResource struct{ baseResource }

type certModel struct {
	ID            types.String   `tfsdk:"id"`
	Name          types.String   `tfsdk:"name"`
	Cert          types.String   `tfsdk:"cert"`
	Intermediates types.String   `tfsdk:"intermediates"`
	Key           types.String   `tfsdk:"key"`
	ExpDate       types.String   `tfsdk:"exp_date"`
	SHA256        types.String   `tfsdk:"sha256_fingerprint"`
	Timeouts      timeouts.Value `tfsdk:"timeouts"`
}

func newCertResource() resource.Resource { return &certResource{} }

func (r *certResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cert"
}

func (r *certResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A TLS certificate of your own, for a site's `cert_id`. Certificates that Opalstack " +
			"requests from Let's Encrypt are not managed here: set `generate_le` on the site instead. " +
			"Everything about a certificate changes in place, so a renewal keeps the id the sites refer to.\n\n" +
			"Deleting a certificate that a site serves is accepted, and the API silently leaves that site " +
			"without one. The name of a certificate does not have to be unique.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "UUID of the certificate.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{MarkdownDescription: "Name of the certificate: letters, digits, `_` and `-`.", Required: true},
			"cert": schema.StringAttribute{MarkdownDescription: "The certificate, PEM encoded.", Required: true},
			"intermediates": schema.StringAttribute{
				MarkdownDescription: "The intermediate certificates, PEM encoded, if the certificate needs any. " +
					"Left out, a new certificate has none and an existing one keeps what the API has, so adopting " +
					"a certificate never removes its chain.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				// The API reports none as null: an empty text would not survive the round trip.
				Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`\S`), "must not be blank; leave it out instead")},
			},
			"key": schema.StringAttribute{
				MarkdownDescription: "The private key, PEM encoded. It is stored in the Terraform state. The provider " +
					"never reads it back from the API, so it is null after an import until the configuration supplies it.",
				Required:  true,
				Sensitive: true,
			},
			"exp_date":           schema.StringAttribute{MarkdownDescription: "When the certificate expires, as the API reports it.", Computed: true},
			"sha256_fingerprint": schema.StringAttribute{MarkdownDescription: "SHA-256 fingerprint of the certificate.", Computed: true},
			"timeouts":           timeouts.Attributes(ctx, timeouts.Opts{Delete: true}),
		},
	}
}

// set copies what a read returns. The key is not part of that. The two PEM
// texts are kept as written when they differ from the API's only in the white
// space around them, which would otherwise show as a change.
func (m *certModel) set(c *client.Cert) {
	m.ID = types.StringValue(c.ID)
	m.Name = types.StringValue(c.Name)
	if strings.TrimSpace(m.Cert.ValueString()) != strings.TrimSpace(c.Cert) {
		m.Cert = types.StringValue(c.Cert)
	}
	switch got := strings.TrimSpace(types.StringPointerValue(c.Intermediates).ValueString()); {
	case got == "":
		m.Intermediates = types.StringNull()
	case strings.TrimSpace(m.Intermediates.ValueString()) != got:
		m.Intermediates = types.StringValue(*c.Intermediates)
	}
	m.ExpDate = types.StringValue(c.ExpDate)
	m.SHA256 = types.StringValue(c.SHA256)
}

// A certificate has no pending state: the API answers with the finished object.
func (r *certResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m certModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.client.Certs().Create(ctx, client.CertCreate{
		Name: m.Name.ValueString(), Cert: m.Cert.ValueString(), Key: m.Key.ValueString(),
		Intermediates: knownString(m.Intermediates),
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the certificate", err.Error())
		return
	}
	m.set(created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *certResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m certModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cert := readForState(ctx, r.client.Certs().ReadKind, "certificate", m.ID.ValueString(), resp)
	if cert == nil {
		return
	}
	m.set(cert)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update sends the certificate and its key together: one without the other
// would be refused whenever either changed.
func (r *certResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, state certModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Nothing is sent when only the timeouts changed.
	if m.Name.Equal(state.Name) && m.Cert.Equal(state.Cert) && m.Key.Equal(state.Key) && m.Intermediates.Equal(state.Intermediates) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		return
	}
	updated, err := r.client.Certs().Update(ctx, client.CertUpdate{
		ID: m.ID.ValueString(), Name: m.Name.ValueString(), Cert: m.Cert.ValueString(), Key: m.Key.ValueString(),
		Intermediates: client.FromPtr(knownString(m.Intermediates)),
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot update the certificate", err.Error())
		return
	}
	m.set(updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *certResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	deleteResource(ctx, req, resp, r.client.Certs(), "certificate")
}

func (r *certResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importByIDOrName(ctx, r.client.Certs().ReadKind, "certificate",
		func(c client.Cert) (string, string) { return c.Name, c.ID }, req, resp)
}
