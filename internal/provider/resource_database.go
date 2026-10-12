package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

// MariaDB and PostgreSQL have the same API shape, so one pair of resources
// serves both. An engine says which one a resource instance is.
type dbEngine struct {
	name     string // "MariaDB"
	userType string // resource type suffix of the user, "_mariauser"
	dbType   string // resource type suffix of the database, "_mariadb"
	users    func(*client.Client) client.DBUserKind
	dbs      func(*client.Client) client.DBKind
}

var (
	maria = dbEngine{"MariaDB", "_mariauser", "_mariadb", (*client.Client).MariaUsers, (*client.Client).MariaDBs}
	psql  = dbEngine{"PostgreSQL", "_psqluser", "_psqldb", (*client.Client).PsqlUsers, (*client.Client).PsqlDBs}
)

var (
	_ resource.ResourceWithConfigure   = (*dbUserResource)(nil)
	_ resource.ResourceWithImportState = (*dbUserResource)(nil)
	_ resource.ResourceWithConfigure   = (*dbResource)(nil)
	_ resource.ResourceWithImportState = (*dbResource)(nil)
)

// A database user.

type dbUserResource struct {
	baseResource
	engine dbEngine
}

type dbUserModel struct {
	ID              types.String   `tfsdk:"id"`
	Name            types.String   `tfsdk:"name"`
	ServerID        types.String   `tfsdk:"server_id"`
	Password        types.String   `tfsdk:"password"`
	External        types.Bool     `tfsdk:"external"`
	DefaultPassword types.String   `tfsdk:"default_password"`
	Timeouts        timeouts.Value `tfsdk:"timeouts"`
}

func newMariaUserResource() resource.Resource { return &dbUserResource{engine: maria} }
func newPsqlUserResource() resource.Resource  { return &dbUserResource{engine: psql} }

func (r *dbUserResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + r.engine.userType
}

func (r *dbUserResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A " + r.engine.name + " user. Give it access to a database through the database's " +
			"`user_ids` or `readonly_user_ids`.",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{MarkdownDescription: "UUID of the user.", Computed: true, PlanModifiers: keep},
			"name":      schema.StringAttribute{MarkdownDescription: "Name of the user. Changing it replaces the user.", Required: true, PlanModifiers: replace},
			"server_id": schema.StringAttribute{MarkdownDescription: "UUID of the server the user lives on. Changing it replaces the user.", Required: true, PlanModifiers: replace},
			"password": schema.StringAttribute{
				MarkdownDescription: "Password of the user. Left out, the API chooses one and reports it in " +
					"`default_password`. It can be changed in place. The API never returns it, so a password " +
					"changed outside Terraform is not noticed.",
				Optional:  true,
				Sensitive: true,
			},
			"external": schema.BoolAttribute{
				MarkdownDescription: "Whether the user may connect from outside the server.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"default_password": schema.StringAttribute{
				MarkdownDescription: "The password the API reported when it created the user. The API reports it only " +
					"then, so it is null for a user that was imported.",
				Computed:      true,
				Sensitive:     true,
				PlanModifiers: keep,
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

// set copies what a read returns. Neither password is part of that.
func (m *dbUserModel) set(u *client.DBUser) {
	m.ID = types.StringValue(u.ID)
	m.Name = types.StringValue(u.Name)
	m.ServerID = types.StringValue(u.Server)
	m.External = types.BoolValue(u.External)
}

func (r *dbUserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m dbUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Create, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	users := r.engine.users(r.client)
	created, err := users.Create(ctx, client.DBUserCreate{
		Server:   m.ServerID.ValueString(),
		Name:     m.Name.ValueString(),
		Password: m.Password.ValueString(),
		External: knownBool(m.External),
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the "+r.engine.name+" user", err.Error())
		return
	}
	// In state before the wait, so a wait that fails does not leave an orphan,
	// and with the password, which no later read can supply.
	m.set(created)
	m.DefaultPassword = stringOrNull(created.DefaultPassword)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)

	ready, err := users.WaitReady(ctx, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("The "+r.engine.name+" user did not become ready", err.Error())
		return
	}
	m.set(ready)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dbUserResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m dbUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	user := readForState(ctx, r.engine.users(r.client).ReadKind, r.engine.name+" user", m.ID.ValueString(), resp)
	if user == nil {
		return
	}
	m.set(user)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dbUserResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, state dbUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Update, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	// Only what changed is sent. A password removed from the configuration is
	// left as it is: the API has no way to unset one.
	update := client.DBUserUpdate{ID: m.ID.ValueString()}
	if !m.Password.Equal(state.Password) {
		update.Password = m.Password.ValueString()
	}
	if !m.External.Equal(state.External) {
		update.External = knownBool(m.External)
	}
	if update.Password != "" || update.External != nil {
		users := r.engine.users(r.client)
		if _, err := users.Update(ctx, update); err != nil {
			resp.Diagnostics.AddError("Cannot update the "+r.engine.name+" user", err.Error())
			return
		}
		ready, err := users.WaitReady(ctx, update.ID)
		if err != nil {
			resp.Diagnostics.AddError("The "+r.engine.name+" user did not become ready", err.Error())
			return
		}
		m.set(ready)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dbUserResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	deleteResource(ctx, req, resp, r.engine.users(r.client), r.engine.name+" user")
}

func (r *dbUserResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importByIDOrName(ctx, r.engine.users(r.client).ReadKind, r.engine.name+" user",
		func(u client.DBUser) (string, string) { return u.Name, u.ID }, req, resp)
}

// A database.

type dbResource struct {
	baseResource
	engine dbEngine
}

type dbModel struct {
	ID              types.String   `tfsdk:"id"`
	Name            types.String   `tfsdk:"name"`
	ServerID        types.String   `tfsdk:"server_id"`
	Charset         types.String   `tfsdk:"charset"`
	UserIDs         types.Set      `tfsdk:"user_ids"`
	ReadOnlyUserIDs types.Set      `tfsdk:"readonly_user_ids"`
	Timeouts        timeouts.Value `tfsdk:"timeouts"`
}

func newMariaDBResource() resource.Resource { return &dbResource{engine: maria} }
func newPsqlDBResource() resource.Resource  { return &dbResource{engine: psql} }

func (r *dbResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + r.engine.dbType
}

func (r *dbResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A " + r.engine.name + " database. Only the lists of users change in place. " +
			"Deleting the database deletes its data.",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{MarkdownDescription: "UUID of the database.", Computed: true, PlanModifiers: keep},
			"name":      schema.StringAttribute{MarkdownDescription: "Name of the database. Changing it replaces the database.", Required: true, PlanModifiers: replace},
			"server_id": schema.StringAttribute{MarkdownDescription: "UUID of the server the database lives on. Changing it replaces the database.", Required: true, PlanModifiers: replace},
			"charset": schema.StringAttribute{
				MarkdownDescription: "Character set, such as `utf8mb4` for MariaDB or `utf8` for PostgreSQL. " +
					"Left out, the API chooses. Changing it replaces the database.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"user_ids":          idSetAttribute("UUIDs of the " + r.engine.name + " users that may read and write. Defaults to none."),
			"readonly_user_ids": idSetAttribute("UUIDs of the " + r.engine.name + " users that may only read. Defaults to none."),
			"timeouts":          timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (m *dbModel) set(ctx context.Context, db *client.DB) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.StringValue(db.ID)
	m.Name = types.StringValue(db.Name)
	m.ServerID = types.StringValue(db.Server)
	m.Charset = types.StringValue(db.Charset)
	m.UserIDs = stringSet(ctx, db.Users, &diags)
	m.ReadOnlyUserIDs = stringSet(ctx, db.ReadOnlyUsers, &diags)
	return diags
}

func (r *dbResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m dbModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	readWrite := stringsOf(ctx, m.UserIDs, &resp.Diagnostics)
	readOnly := stringsOf(ctx, m.ReadOnlyUserIDs, &resp.Diagnostics)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Create, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	dbs := r.engine.dbs(r.client)
	created, err := dbs.Create(ctx, client.DBCreate{
		Name: m.Name.ValueString(), Server: m.ServerID.ValueString(), Charset: m.Charset.ValueString(),
		Users: readWrite, ReadOnlyUsers: readOnly,
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the "+r.engine.name+" database", err.Error())
		return
	}
	// In state before the wait, so a wait that fails does not leave an orphan.
	resp.Diagnostics.Append(m.set(ctx, created)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)

	ready, err := dbs.WaitReady(ctx, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("The "+r.engine.name+" database did not become ready", err.Error())
		return
	}
	resp.Diagnostics.Append(m.set(ctx, ready)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dbResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m dbModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	db := readForState(ctx, r.engine.dbs(r.client).ReadKind, r.engine.name+" database", m.ID.ValueString(), resp)
	if db == nil {
		return
	}
	resp.Diagnostics.Append(m.set(ctx, db)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dbResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m dbModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	readWrite := stringsOf(ctx, m.UserIDs, &resp.Diagnostics)
	readOnly := stringsOf(ctx, m.ReadOnlyUserIDs, &resp.Diagnostics)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Update, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	dbs := r.engine.dbs(r.client)
	if _, err := dbs.Update(ctx, client.DBUpdate{ID: m.ID.ValueString(), Users: readWrite, ReadOnlyUsers: readOnly}); err != nil {
		resp.Diagnostics.AddError("Cannot update the "+r.engine.name+" database", err.Error())
		return
	}
	ready, err := dbs.WaitReady(ctx, m.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("The "+r.engine.name+" database did not become ready", err.Error())
		return
	}
	resp.Diagnostics.Append(m.set(ctx, ready)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dbResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	deleteResource(ctx, req, resp, r.engine.dbs(r.client), r.engine.name+" database")
}

func (r *dbResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importByIDOrName(ctx, r.engine.dbs(r.client).ReadKind, r.engine.name+" database",
		func(db client.DB) (string, string) { return db.Name, db.ID }, req, resp)
}
