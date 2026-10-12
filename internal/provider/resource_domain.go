package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = (*domainResource)(nil)
	_ resource.ResourceWithImportState = (*domainResource)(nil)
)

type domainResource struct{ baseResource }

type domainModel struct {
	ID               types.String   `tfsdk:"id"`
	Name             types.String   `tfsdk:"name"`
	DelegatedDNSZone types.Bool     `tfsdk:"delegated_dns_zone"`
	IsValidHostname  types.Bool     `tfsdk:"is_valid_hostname"`
	DKIMRecord       types.String   `tfsdk:"dkim_record"`
	DKIMPubkey       types.String   `tfsdk:"dkim_pubkey"`
	Timeouts         timeouts.Value `tfsdk:"timeouts"`
}

func newDomainResource() resource.Resource { return &domainResource{} }

func (r *domainResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain"
}

func (r *domainResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A domain or subdomain of the account. Sites, DNS records and mail refer to it. " +
			"The DKIM private key the API holds for the domain is never read into state.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "UUID of the domain.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Fully qualified name. A domain cannot be renamed: changing this replaces it.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"delegated_dns_zone": schema.BoolAttribute{
				MarkdownDescription: "Whether the domain is a DNS zone of its own, delegated from its parent.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"is_valid_hostname": schema.BoolAttribute{
				MarkdownDescription: "Whether the name can be used as a hostname.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"dkim_record": schema.StringAttribute{
				MarkdownDescription: "The DKIM TXT record for the domain, when one exists.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"dkim_pubkey": schema.StringAttribute{
				MarkdownDescription: "The DKIM public key of the domain, when one exists.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (m *domainModel) set(d *client.Domain) {
	m.ID = types.StringValue(d.ID)
	m.Name = types.StringValue(d.Name)
	m.DelegatedDNSZone = types.BoolValue(d.DelegatedDNSZone)
	m.IsValidHostname = types.BoolValue(d.IsValidHostname)
	m.DKIMRecord = types.StringPointerValue(d.DKIMRecord)
	m.DKIMPubkey = types.StringPointerValue(d.DKIMPubkey)
}

func (r *domainResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m domainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	timeout, diags := m.Timeouts.Create(ctx, defaultTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	domains := r.client.Domains()
	created, err := domains.Create(ctx, client.DomainCreate{
		Name:             m.Name.ValueString(),
		DelegatedDNSZone: knownBool(m.DelegatedDNSZone),
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the domain", err.Error())
		return
	}
	// The domain exists from here on. It goes into state before the wait, so a
	// wait that fails leaves a resource Terraform knows about, not an orphan.
	m.set(created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)

	ready, err := domains.WaitReady(ctx, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("The domain did not become ready", err.Error())
		return
	}
	m.set(ready)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *domainResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domain := readForState(ctx, r.client.Domains().ReadKind, "domain", m.ID.ValueString(), resp)
	if domain == nil {
		return
	}
	m.set(domain)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *domainResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m domainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	timeout, diags := m.Timeouts.Update(ctx, defaultTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	domains := r.client.Domains()
	if _, err := domains.Update(ctx, client.DomainUpdate{
		ID:               m.ID.ValueString(),
		DelegatedDNSZone: knownBool(m.DelegatedDNSZone),
	}); err != nil {
		resp.Diagnostics.AddError("Cannot update the domain", err.Error())
		return
	}
	ready, err := domains.WaitReady(ctx, m.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("The domain did not become ready", err.Error())
		return
	}
	m.set(ready)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *domainResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	timeout, diags := m.Timeouts.Delete(ctx, defaultTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp.Diagnostics.Append(deleteAndWait(ctx, r.client.Domains(), "domain", m.ID.ValueString())...)
}

func (r *domainResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importByIDOrName(ctx, r.client.Domains().ReadKind, "domain",
		func(d client.Domain) (string, string) { return d.Name, d.ID }, req, resp)
}
