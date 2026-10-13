package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type dnsRecordDataSource struct{ baseDataSource }

// dnsRecordDataModel is the resource's model without the timeouts.
type dnsRecordDataModel struct {
	ID       types.String `tfsdk:"id"`
	DomainID types.String `tfsdk:"domain_id"`
	Type     types.String `tfsdk:"type"`
	Content  types.String `tfsdk:"content"`
	Priority types.Int64  `tfsdk:"priority"`
	TTL      types.Int64  `tfsdk:"ttl"`
}

func newDNSRecordDataSource() datasource.DataSource { return &dnsRecordDataSource{} }

func (d *dnsRecordDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dnsrecord"
}

// A record has no name, so it is looked up by UUID only.
func (d *dnsRecordDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a DNS record by `id`. A record has no name to look it up by.",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{MarkdownDescription: "UUID of the record.", Required: true},
			"domain_id": computedString("UUID of the domain the record belongs to."),
			"type":      computedString("Record type, such as `A`, `MX` or `TXT`."),
			"content":   computedString("Value of the record."),
			"priority":  schema.Int64Attribute{MarkdownDescription: "Priority, for the types that use one.", Computed: true},
			"ttl":       schema.Int64Attribute{MarkdownDescription: "Time to live in seconds.", Computed: true},
		},
	}
}

func (d *dnsRecordDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m dnsRecordDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rec, err := d.client.DNSRecords().Read(ctx, m.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the DNS record", err.Error())
		return
	}
	// The resource's model does the conversion, so the two cannot drift apart.
	var full dnsRecordModel
	full.set(rec)
	m = dnsRecordDataModel{
		ID: full.ID, DomainID: full.DomainID, Type: full.Type, Content: full.Content, Priority: full.Priority, TTL: full.TTL,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
