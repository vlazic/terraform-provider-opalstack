package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
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
	_ resource.ResourceWithConfigure   = (*dnsRecordResource)(nil)
	_ resource.ResourceWithImportState = (*dnsRecordResource)(nil)
)

type dnsRecordResource struct{ baseResource }

type dnsRecordModel struct {
	ID       types.String   `tfsdk:"id"`
	DomainID types.String   `tfsdk:"domain_id"`
	Type     types.String   `tfsdk:"type"`
	Content  types.String   `tfsdk:"content"`
	Priority types.Int64    `tfsdk:"priority"`
	TTL      types.Int64    `tfsdk:"ttl"`
	Timeouts timeouts.Value `tfsdk:"timeouts"`
}

func newDNSRecordResource() resource.Resource { return &dnsRecordResource{} }

func (r *dnsRecordResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dnsrecord"
}

func (r *dnsRecordResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A DNS record of a domain. Records that Opalstack creates by itself, for a site or for mail, " +
			"are not managed by this resource unless they are imported.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "UUID of the record.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"domain_id": schema.StringAttribute{
				MarkdownDescription: "UUID of the domain the record belongs to. Changing it replaces the record.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "Record type: `A`, `AAAA`, `CNAME`, `MX`, `TXT`, `SRV` or `CAA`. Changing it replaces the record.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("A", "AAAA", "CNAME", "MX", "TXT", "SRV", "CAA")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"content": schema.StringAttribute{
				MarkdownDescription: "Value of the record.",
				Required:            true,
			},
			"priority": schema.Int64Attribute{
				MarkdownDescription: "Priority, for the types that use one. The API chooses a default when it is not set.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"ttl": schema.Int64Attribute{
				MarkdownDescription: "Time to live in seconds. The API chooses a default when it is not set.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (m *dnsRecordModel) set(rec *client.DNSRecord) {
	m.ID = types.StringValue(rec.ID)
	m.DomainID = types.StringValue(rec.Domain)
	m.Type = types.StringValue(rec.Type)
	m.Content = types.StringValue(rec.Content)
	m.Priority = types.Int64Value(rec.Priority)
	m.TTL = types.Int64Value(rec.TTL)
}

func (r *dnsRecordResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m dnsRecordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Create, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	records := r.client.DNSRecords()
	created, err := records.Create(ctx, client.DNSRecordCreate{
		Domain:   m.DomainID.ValueString(),
		Type:     m.Type.ValueString(),
		Content:  m.Content.ValueString(),
		Priority: knownInt64(m.Priority),
		TTL:      knownInt64(m.TTL),
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the DNS record", err.Error())
		return
	}
	// In state before the wait, so a wait that fails does not leave an orphan.
	m.set(created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)

	ready, err := records.WaitReady(ctx, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("The DNS record did not become ready", err.Error())
		return
	}
	m.set(ready)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dnsRecordResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m dnsRecordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rec := readForState(ctx, r.client.DNSRecords().ReadKind, "DNS record", m.ID.ValueString(), resp)
	if rec == nil {
		return
	}
	m.set(rec)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dnsRecordResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m dnsRecordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Update, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	records := r.client.DNSRecords()
	if _, err := records.Update(ctx, client.DNSRecordUpdate{
		ID:       m.ID.ValueString(),
		Content:  m.Content.ValueString(),
		Priority: knownInt64(m.Priority),
		TTL:      knownInt64(m.TTL),
	}); err != nil {
		resp.Diagnostics.AddError("Cannot update the DNS record", err.Error())
		return
	}
	ready, err := records.WaitReady(ctx, m.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("The DNS record did not become ready", err.Error())
		return
	}
	m.set(ready)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dnsRecordResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m dnsRecordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Delete, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(deleteAndWait(ctx, r.client.DNSRecords(), "DNS record", m.ID.ValueString())...)
}

// A record has no name, so it is imported by UUID only.
func (r *dnsRecordResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !uuidPattern.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid import id", "A DNS record is imported by its UUID.")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
