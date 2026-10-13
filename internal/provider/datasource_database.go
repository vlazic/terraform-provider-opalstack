package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

// As for the resources, one pair of data sources serves both engines.

// A database user.

type dbUserDataSource struct {
	baseDataSource
	engine dbEngine
}

// dbUserDataModel is the resource's model without the passwords and the timeouts.
type dbUserDataModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	ServerID types.String `tfsdk:"server_id"`
	External types.Bool   `tfsdk:"external"`
}

func newMariaUserDataSource() datasource.DataSource { return &dbUserDataSource{engine: maria} }
func newPsqlUserDataSource() datasource.DataSource  { return &dbUserDataSource{engine: psql} }

func (d *dbUserDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + d.engine.userType
}

func (d *dbUserDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a " + d.engine.name + " user by `id` or by `name`. Its password is never read.",
		Attributes: map[string]schema.Attribute{
			"id":        lookupAttribute("UUID of the user."),
			"name":      lookupAttribute("Name of the user."),
			"server_id": computedString("UUID of the server the user lives on."),
			"external":  computedBool("Whether the user may connect from outside the server."),
		},
	}
}

func (d *dbUserDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return idOr("name")
}

func (d *dbUserDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m dbUserDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	user, diags := find(ctx, d.engine.users(d.client).ReadKind, d.engine.name+" user", m.ID, func(u client.DBUser) bool {
		return u.Name == m.Name.ValueString()
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The resource's model does the conversion, so the two cannot drift apart.
	var full dbUserModel
	full.set(user)
	m = dbUserDataModel{ID: full.ID, Name: full.Name, ServerID: full.ServerID, External: full.External}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// A database.

type dbDataSource struct {
	baseDataSource
	engine dbEngine
}

// dbDataModel is the resource's model without the timeouts.
type dbDataModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	ServerID        types.String `tfsdk:"server_id"`
	Charset         types.String `tfsdk:"charset"`
	UserIDs         types.Set    `tfsdk:"user_ids"`
	ReadOnlyUserIDs types.Set    `tfsdk:"readonly_user_ids"`
}

func newMariaDBDataSource() datasource.DataSource { return &dbDataSource{engine: maria} }
func newPsqlDBDataSource() datasource.DataSource  { return &dbDataSource{engine: psql} }

func (d *dbDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + d.engine.dbType
}

func (d *dbDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a " + d.engine.name + " database by `id` or by `name`.",
		Attributes: map[string]schema.Attribute{
			"id":                lookupAttribute("UUID of the database."),
			"name":              lookupAttribute("Name of the database."),
			"server_id":         computedString("UUID of the server the database lives on."),
			"charset":           computedString("Character set of the database."),
			"user_ids":          computedStringSet("UUIDs of the " + d.engine.name + " users that may read and write."),
			"readonly_user_ids": computedStringSet("UUIDs of the " + d.engine.name + " users that may only read."),
		},
	}
}

func (d *dbDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return idOr("name")
}

func (d *dbDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m dbDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	db, diags := find(ctx, d.engine.dbs(d.client).ReadKind, d.engine.name+" database", m.ID, func(db client.DB) bool {
		return db.Name == m.Name.ValueString()
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The resource's model does the conversion, so the two cannot drift apart.
	var full dbModel
	resp.Diagnostics.Append(full.set(ctx, db)...)
	m = dbDataModel{
		ID: full.ID, Name: full.Name, ServerID: full.ServerID, Charset: full.Charset,
		UserIDs: full.UserIDs, ReadOnlyUserIDs: full.ReadOnlyUserIDs,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
