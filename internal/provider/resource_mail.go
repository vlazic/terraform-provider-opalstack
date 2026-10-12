package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = (*mailUserResource)(nil)
	_ resource.ResourceWithImportState = (*mailUserResource)(nil)
	_ resource.ResourceWithConfigure   = (*addressResource)(nil)
	_ resource.ResourceWithImportState = (*addressResource)(nil)
)

// A mailbox.

type mailUserResource struct{ baseResource }

type mailUserModel struct {
	ID              types.String   `tfsdk:"id"`
	Name            types.String   `tfsdk:"name"`
	IMAPServerID    types.String   `tfsdk:"imap_server_id"`
	Password        types.String   `tfsdk:"password"`
	DefaultPassword types.String   `tfsdk:"default_password"`
	Timeouts        timeouts.Value `tfsdk:"timeouts"`
}

func newMailUserResource() resource.Resource { return &mailUserResource{} }

func (r *mailUserResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mailuser"
}

func (r *mailUserResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A mailbox. Mail reaches it through an `opalstack_address` that names it. " +
			"Deleting a mailbox deletes its mail, and the API silently takes it out of every address that " +
			"delivered to it.\n\n" +
			"The mailbox's filter rules and autoresponder are not managed by this resource and are left as they are.",
		Attributes: map[string]schema.Attribute{
			"id":   schema.StringAttribute{MarkdownDescription: "UUID of the mailbox.", Computed: true, PlanModifiers: keep},
			"name": schema.StringAttribute{MarkdownDescription: "Login name of the mailbox. Changing it replaces the mailbox.", Required: true, PlanModifiers: replace},
			"imap_server_id": schema.StringAttribute{
				MarkdownDescription: "UUID of the IMAP server that holds the mailbox. Look it up by hostname with the " +
					"`opalstack_server` data source. Changing it replaces the mailbox.",
				Required:      true,
				PlanModifiers: replace,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Password of the mailbox. Left out, the API chooses one and reports it in " +
					"`default_password`. It can be changed in place. The API never returns it, so a password " +
					"changed outside Terraform is not noticed.",
				Optional:  true,
				Sensitive: true,
			},
			"default_password": schema.StringAttribute{
				MarkdownDescription: "The password the API reported when it created the mailbox. The API reports it " +
					"only then, so it is null for a mailbox that was imported.",
				Computed:      true,
				Sensitive:     true,
				PlanModifiers: keep,
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

// set copies what a read returns. Neither password is part of that.
func (m *mailUserModel) set(u *client.MailUser) {
	m.ID = types.StringValue(u.ID)
	m.Name = types.StringValue(u.Name)
	m.IMAPServerID = types.StringValue(u.IMAPServer)
}

func (r *mailUserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m mailUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Create, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	boxes := r.client.MailUsers()
	created, err := boxes.Create(ctx, client.MailUserCreate{
		IMAPServer: m.IMAPServerID.ValueString(), Name: m.Name.ValueString(), Password: m.Password.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the mailbox", err.Error())
		return
	}
	// In state before the wait, so a wait that fails does not leave an orphan,
	// and with the password, which no later read can supply.
	m.set(created)
	m.DefaultPassword = types.StringNull()
	if created.DefaultPassword != "" {
		m.DefaultPassword = types.StringValue(created.DefaultPassword)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)

	ready, err := boxes.WaitReady(ctx, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("The mailbox did not become ready", err.Error())
		return
	}
	m.set(ready)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *mailUserResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m mailUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	box := readForState(ctx, r.client.MailUsers().ReadKind, "mailbox", m.ID.ValueString(), resp)
	if box == nil {
		return
	}
	m.set(box)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *mailUserResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, state mailUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Update, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	// A password removed from the configuration is left as it is: the API has
	// no way to unset one.
	if password := m.Password.ValueString(); password != "" && !m.Password.Equal(state.Password) {
		boxes := r.client.MailUsers()
		if _, err := boxes.Update(ctx, client.MailUserUpdate{ID: m.ID.ValueString(), Password: password}); err != nil {
			resp.Diagnostics.AddError("Cannot update the mailbox", err.Error())
			return
		}
		ready, err := boxes.WaitReady(ctx, m.ID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("The mailbox did not become ready", err.Error())
			return
		}
		m.set(ready)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *mailUserResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	deleteResource(ctx, req, resp, r.client.MailUsers(), "mailbox")
}

func (r *mailUserResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importByIDOrName(ctx, r.client.MailUsers().ReadKind, "mailbox",
		func(u client.MailUser) (string, string) { return u.Name, u.ID }, req, resp)
}

// A mail address.

type addressResource struct{ baseResource }

type addressModel struct {
	ID          types.String   `tfsdk:"id"`
	Source      types.String   `tfsdk:"source"`
	MailUserIDs types.Set      `tfsdk:"mailuser_ids"`
	Forwards    types.Set      `tfsdk:"forwards"`
	Timeouts    timeouts.Value `tfsdk:"timeouts"`
}

func newAddressResource() resource.Resource { return &addressResource{} }

func (r *addressResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_address"
}

func (r *addressResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	list := func(description string) schema.SetAttribute {
		return schema.SetAttribute{
			MarkdownDescription: description,
			ElementType:         types.StringType,
			Optional:            true,
			Computed:            true,
			Default:             setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A mail address. Mail sent to it is delivered to mailboxes, forwarded to other " +
			"addresses, or both. The API accepts an address with neither, which accepts mail and keeps none of it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "UUID of the address.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"source": schema.StringAttribute{
				MarkdownDescription: "The address itself, such as `info@example.com`. Its domain must be a domain of " +
					"the account. Changing it replaces the address.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"mailuser_ids": list("UUIDs of the mailboxes the mail is delivered to. Defaults to none."),
			"forwards":     list("Addresses the mail is sent on to. Defaults to none."),
			"timeouts":     timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (m *addressModel) set(ctx context.Context, a *client.Address) diag.Diagnostics {
	var diags, d diag.Diagnostics
	m.ID = types.StringValue(a.ID)
	m.Source = types.StringValue(a.Source)
	m.MailUserIDs, d = types.SetValueFrom(ctx, types.StringType, a.Destinations)
	diags.Append(d...)
	m.Forwards, d = types.SetValueFrom(ctx, types.StringType, a.Forwards)
	diags.Append(d...)
	return diags
}

// lists reads the two sets out of the model. Neither is ever nil: the API
// requires forwards, and an update replaces both.
func (m *addressModel) lists(ctx context.Context) (boxes, forwards []string, diags diag.Diagnostics) {
	boxes, forwards = []string{}, []string{}
	diags.Append(m.MailUserIDs.ElementsAs(ctx, &boxes, false)...)
	diags.Append(m.Forwards.ElementsAs(ctx, &forwards, false)...)
	return boxes, forwards, diags
}

func (r *addressResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m addressModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	boxes, forwards, diags := m.lists(ctx)
	resp.Diagnostics.Append(diags...)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Create, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	addresses := r.client.Addresses()
	created, err := addresses.Create(ctx, client.AddressCreate{Source: m.Source.ValueString(), Destinations: boxes, Forwards: forwards})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the address", err.Error())
		return
	}
	// In state before the wait, so a wait that fails does not leave an orphan.
	resp.Diagnostics.Append(m.set(ctx, created)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)

	ready, err := addresses.WaitReady(ctx, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("The address did not become ready", err.Error())
		return
	}
	resp.Diagnostics.Append(m.set(ctx, ready)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *addressResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m addressModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	address := readForState(ctx, r.client.Addresses().ReadKind, "address", m.ID.ValueString(), resp)
	if address == nil {
		return
	}
	resp.Diagnostics.Append(m.set(ctx, address)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *addressResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m addressModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	boxes, forwards, diags := m.lists(ctx)
	resp.Diagnostics.Append(diags...)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Update, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	addresses := r.client.Addresses()
	if _, err := addresses.Update(ctx, client.AddressUpdate{ID: m.ID.ValueString(), Destinations: boxes, Forwards: forwards}); err != nil {
		resp.Diagnostics.AddError("Cannot update the address", err.Error())
		return
	}
	ready, err := addresses.WaitReady(ctx, m.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("The address did not become ready", err.Error())
		return
	}
	resp.Diagnostics.Append(m.set(ctx, ready)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *addressResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	deleteResource(ctx, req, resp, r.client.Addresses(), "address")
}

// An address is imported by its UUID or by the address itself.
func (r *addressResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importByIDOrName(ctx, r.client.Addresses().ReadKind, "address",
		func(a client.Address) (string, string) { return a.Source, a.ID }, req, resp)
}
