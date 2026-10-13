package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

// A mailbox.

type mailUserDataSource struct{ baseDataSource }

// mailUserDataModel is the resource's model without the passwords and the timeouts.
type mailUserDataModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	IMAPServerID types.String `tfsdk:"imap_server_id"`
	mailFilterModel
}

func newMailUserDataSource() datasource.DataSource { return &mailUserDataSource{} }

func (d *mailUserDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mailuser"
}

func (d *mailUserDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a mailbox by `id` or by `name`. Its password is never read.",
		Attributes: map[string]schema.Attribute{
			"id":             lookupAttribute("UUID of the mailbox."),
			"name":           lookupAttribute("Login name of the mailbox."),
			"imap_server_id": computedString("UUID of the IMAP server that holds the mailbox."),
			"procmailrc": computedString("The mailbox's filter rules, in procmail's format, as the API stores them: " +
				"without a trailing newline."),
			"autoresponder_enable":  computedBool("Whether the mailbox answers mail by itself."),
			"autoresponder_subject": computedString("Subject of the automatic answer."),
			"autoresponder_message": computedString("Text of the automatic answer."),
			"autoresponder_noreply": computedStringSet("Addresses the autoresponder never answers."),
		},
	}
}

func (d *mailUserDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return idOr("name")
}

func (d *mailUserDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m mailUserDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	box, diags := find(ctx, d.client.MailUsers().ReadKind, "mailbox", m.ID, func(u client.MailUser) bool {
		return u.Name == m.Name.ValueString()
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	m.ID = types.StringValue(box.ID)
	m.Name = types.StringValue(box.Name)
	m.IMAPServerID = types.StringValue(box.IMAPServer)
	// The resource's model does the conversion, so the two cannot drift apart.
	resp.Diagnostics.Append(m.set(ctx, box.MailFilter)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// A mail address.

type addressDataSource struct{ baseDataSource }

// addressDataModel is the resource's model without the timeouts.
type addressDataModel struct {
	ID          types.String `tfsdk:"id"`
	Source      types.String `tfsdk:"source"`
	MailUserIDs types.Set    `tfsdk:"mailuser_ids"`
	Forwards    types.Set    `tfsdk:"forwards"`
}

func newAddressDataSource() datasource.DataSource { return &addressDataSource{} }

func (d *addressDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_address"
}

func (d *addressDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a mail address by `id` or by `source`.",
		Attributes: map[string]schema.Attribute{
			"id":           lookupAttribute("UUID of the address."),
			"source":       lookupAttribute("The address itself, such as `info@example.com`."),
			"mailuser_ids": computedStringSet("UUIDs of the mailboxes the mail is delivered to."),
			"forwards":     computedStringSet("Addresses the mail is sent on to."),
		},
	}
}

func (d *addressDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return idOr("source")
}

func (d *addressDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m addressDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	address, diags := find(ctx, d.client.Addresses().ReadKind, "address", m.ID, func(a client.Address) bool {
		return a.Source == m.Source.ValueString()
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The resource's model does the conversion, so the two cannot drift apart.
	var full addressModel
	resp.Diagnostics.Append(full.set(ctx, address)...)
	m = addressDataModel{ID: full.ID, Source: full.Source, MailUserIDs: full.MailUserIDs, Forwards: full.Forwards}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
