package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = (*osuserResource)(nil)
	_ resource.ResourceWithImportState = (*osuserResource)(nil)
)

type osuserResource struct{ baseResource }

type osuserModel struct {
	ID              types.String   `tfsdk:"id"`
	Name            types.String   `tfsdk:"name"`
	ServerID        types.String   `tfsdk:"server_id"`
	Password        types.String   `tfsdk:"password"`
	DefaultPassword types.String   `tfsdk:"default_password"`
	Timeouts        timeouts.Value `tfsdk:"timeouts"`
}

func newOSUserResource() resource.Resource { return &osuserResource{} }

func (r *osuserResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_osuser"
}

func (r *osuserResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A shell user. Apps belong to one and run as it. Only its password can be changed in " +
			"place, and deleting one deletes its home directory **and every app that belongs to it**.\n\n" +
			"The API chooses a password and reports it once, in `default_password`, whether or not the " +
			"configuration gives a `password` of its own. The API has no way to manage SSH keys.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "UUID of the shell user.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Login name. Changing it replaces the shell user.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"server_id": schema.StringAttribute{
				MarkdownDescription: "UUID of the server the shell user lives on. Changing it replaces the shell user.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Password of the shell user, for SSH and SFTP. Left out, the only password is " +
					"the one the API chooses and reports in `default_password`. It can be changed in place. The " +
					"API never returns it, so a password changed outside Terraform is not noticed, and taking " +
					"the attribute out of the configuration changes nothing.",
				Optional:  true,
				Sensitive: true,
			},
			"default_password": schema.StringAttribute{
				MarkdownDescription: "The password the API chose when it created the shell user. The API reports it only " +
					"then, so it is null for a shell user that was imported. Once a `password` is set by an " +
					"update, the API refuses this one. A shell user created with a `password` still gets one " +
					"here, and whether it works for login as well was not tried: treat it as a second working " +
					"credential held in the state.",
				Computed:      true,
				Sensitive:     true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

// set copies what a read returns. default_password is not part of that.
func (m *osuserModel) set(u *client.OSUser) {
	m.ID = types.StringValue(u.ID)
	m.Name = types.StringValue(u.Name)
	m.ServerID = types.StringValue(u.Server)
}

func (r *osuserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m osuserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Create, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	users := r.client.OSUsers()
	created, err := users.Create(ctx, client.OSUserCreate{
		Server: m.ServerID.ValueString(), Name: m.Name.ValueString(), Password: m.Password.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the shell user", err.Error())
		return
	}
	// In state before the wait, so a wait that fails does not leave an orphan,
	// and with the password, which no later read can supply.
	m.set(created)
	m.DefaultPassword = stringOrNull(created.DefaultPassword)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)

	ready, err := users.WaitReady(ctx, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("The shell user did not become ready", err.Error())
		return
	}
	m.set(ready)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *osuserResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m osuserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	user := readForState(ctx, r.client.OSUsers().ReadKind, "shell user", m.ID.ValueString(), resp)
	if user == nil {
		return
	}
	m.set(user)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update changes the password, the one thing the API changes in place. A
// password taken out of the configuration is not sent: the API has no way to
// go back to one of its own choosing.
func (r *osuserResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, state osuserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Update, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}
	if password := m.Password.ValueString(); password != "" && !m.Password.Equal(state.Password) {
		users := r.client.OSUsers()
		if _, err := users.Update(ctx, client.OSUserUpdate{ID: m.ID.ValueString(), Password: password}); err != nil {
			resp.Diagnostics.AddError("Cannot change the shell user's password", err.Error())
			return
		}
		if _, err := users.WaitReady(ctx, m.ID.ValueString()); err != nil {
			resp.Diagnostics.AddError("The shell user did not become ready", err.Error())
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *osuserResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	deleteResource(ctx, req, resp, r.client.OSUsers(), "shell user")
}

func (r *osuserResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importByIDOrName(ctx, r.client.OSUsers().ReadKind, "shell user",
		func(u client.OSUser) (string, string) { return u.Name, u.ID }, req, resp)
}
