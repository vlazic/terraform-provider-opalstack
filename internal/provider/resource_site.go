package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

var (
	_ resource.ResourceWithConfigure        = (*siteResource)(nil)
	_ resource.ResourceWithImportState      = (*siteResource)(nil)
	_ resource.ResourceWithConfigValidators = (*siteResource)(nil)
)

type siteResource struct{ baseResource }

type siteModel struct {
	ID              types.String   `tfsdk:"id"`
	Name            types.String   `tfsdk:"name"`
	ServerID        types.String   `tfsdk:"server_id"`
	DomainIDs       types.Set      `tfsdk:"domain_ids"`
	Routes          types.Set      `tfsdk:"routes"`
	IP4ID           types.String   `tfsdk:"ip4_id"`
	IP6ID           types.String   `tfsdk:"ip6_id"`
	CertID          types.String   `tfsdk:"cert_id"`
	PrimaryDomainID types.String   `tfsdk:"primary_domain_id"`
	Redirect        types.Bool     `tfsdk:"redirect"`
	GenerateLE      types.Bool     `tfsdk:"generate_le"`
	Disabled        types.Bool     `tfsdk:"disabled"`
	Timeouts        timeouts.Value `tfsdk:"timeouts"`
}

type routeModel struct {
	AppID types.String `tfsdk:"app_id"`
	URI   types.String `tfsdk:"uri"`
}

var uriPattern = regexp.MustCompile(`^/(.*/)?$`)

var routeType = types.ObjectType{AttrTypes: map[string]attr.Type{"app_id": types.StringType, "uri": types.StringType}}

func newSiteResource() resource.Resource { return &siteResource{} }

func (r *siteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site"
}

func (r *siteResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	// A reference or a switch the API fills in when the configuration is silent.
	// Removing it from the configuration later leaves it as it is.
	reference := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Optional: true, Computed: true, PlanModifiers: keep}
	}
	toggle := func(description string) schema.BoolAttribute {
		return schema.BoolAttribute{
			MarkdownDescription: description, Optional: true, Computed: true,
			PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A site: a set of domains whose requests are routed to apps. Everything about a site " +
			"changes in place.",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{MarkdownDescription: "UUID of the site.", Computed: true, PlanModifiers: keep},
			"name":      schema.StringAttribute{MarkdownDescription: "Name of the site: lowercase letters, digits, `_` and `-`.", Required: true},
			"server_id": schema.StringAttribute{MarkdownDescription: "UUID of the server the site is served from.", Computed: true, PlanModifiers: keep},
			"domain_ids": schema.SetAttribute{
				MarkdownDescription: "UUIDs of the domains the site answers for.",
				ElementType:         types.StringType,
				Required:            true,
				Validators:          []validator.Set{setvalidator.SizeAtLeast(1)},
			},
			"routes": schema.SetNestedAttribute{
				MarkdownDescription: "Which app answers which path. The whole set is sent on every change.",
				Required:            true,
				Validators:          []validator.Set{setvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"app_id": schema.StringAttribute{MarkdownDescription: "UUID of the app.", Required: true},
					"uri": schema.StringAttribute{
						MarkdownDescription: "Path prefix the app answers, starting and ending with `/`. Defaults to `/`.",
						Optional:            true,
						Computed:            true,
						Default:             stringdefault.StaticString("/"),
						// The API appends a missing trailing slash, which would not match the plan.
						Validators: []validator.String{stringvalidator.RegexMatches(uriPattern, "must start and end with /")},
					},
				}},
			},
			"ip4_id":            reference("UUID of the IPv4 address the site listens on. See the `opalstack_ip` data source. A site needs `ip4_id`, `ip6_id` or both."),
			"ip6_id":            reference("UUID of the IPv6 address the site listens on."),
			"cert_id":           reference("UUID of the certificate the site serves."),
			"primary_domain_id": reference("UUID of the domain the others redirect to, when `redirect` is on."),
			"redirect":          toggle("Whether the other domains redirect to the primary one."),
			"generate_le":       toggle("Whether a Let's Encrypt certificate is requested for the site."),
			"disabled":          toggle("Whether the site is switched off. A disabled site serves nothing."),
			"timeouts":          timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

// The API refuses a site without an address: "An IPv4 or IPv6 address is required."
func (r *siteResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.AtLeastOneOf(path.MatchRoot("ip4_id"), path.MatchRoot("ip6_id")),
	}
}

// request reads the two sets out of the model.
func (m *siteModel) request(ctx context.Context) (domains []string, routes []client.Route, diags diag.Diagnostics) {
	diags.Append(m.DomainIDs.ElementsAs(ctx, &domains, false)...)
	var models []routeModel
	diags.Append(m.Routes.ElementsAs(ctx, &models, false)...)
	for _, r := range models {
		routes = append(routes, client.Route{App: r.AppID.ValueString(), URI: r.URI.ValueString()})
	}
	return domains, routes, diags
}

func (m *siteModel) set(ctx context.Context, s *client.Site) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.StringValue(s.ID)
	m.Name = types.StringValue(s.Name)
	m.ServerID = types.StringValue(s.Server)
	m.IP4ID = types.StringPointerValue(s.IP4)
	m.IP6ID = types.StringPointerValue(s.IP6)
	m.CertID = types.StringPointerValue(s.Cert)
	m.PrimaryDomainID = types.StringPointerValue(s.PrimaryDomain)
	m.Redirect = types.BoolValue(s.Redirect)
	m.GenerateLE = types.BoolValue(s.GenerateLE)
	m.Disabled = types.BoolValue(s.Disabled)

	var d diag.Diagnostics
	m.DomainIDs, d = types.SetValueFrom(ctx, types.StringType, s.Domains)
	diags.Append(d...)
	routes := make([]routeModel, 0, len(s.Routes))
	for _, r := range s.Routes {
		routes = append(routes, routeModel{AppID: types.StringValue(r.App), URI: types.StringValue(r.URI)})
	}
	m.Routes, d = types.SetValueFrom(ctx, routeType, routes)
	diags.Append(d...)
	return diags
}

func (r *siteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m siteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domains, routes, diags := m.request(ctx)
	resp.Diagnostics.Append(diags...)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Create, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	sites := r.client.Sites()
	created, err := sites.Create(ctx, client.SiteCreate{
		Name:          m.Name.ValueString(),
		Domains:       domains,
		Routes:        routes,
		IP4:           knownString(m.IP4ID),
		IP6:           knownString(m.IP6ID),
		Cert:          knownString(m.CertID),
		PrimaryDomain: knownString(m.PrimaryDomainID),
		Redirect:      knownBool(m.Redirect),
		GenerateLE:    knownBool(m.GenerateLE),
		Disabled:      knownBool(m.Disabled),
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the site", err.Error())
		return
	}
	// In state before the wait, so a wait that fails does not leave an orphan.
	resp.Diagnostics.Append(m.set(ctx, created)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)

	ready, err := sites.WaitReady(ctx, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("The site did not become ready", err.Error())
		return
	}
	resp.Diagnostics.Append(m.set(ctx, ready)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *siteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m siteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	site := readForState(ctx, r.client.Sites().ReadKind, "site", m.ID.ValueString(), resp)
	if site == nil {
		return
	}
	resp.Diagnostics.Append(m.set(ctx, site)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *siteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m siteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domains, routes, diags := m.request(ctx)
	resp.Diagnostics.Append(diags...)
	ctx, cancel := withTimeout(ctx, m.Timeouts.Update, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	sites := r.client.Sites()
	if _, err := sites.Update(ctx, client.SiteUpdate{
		ID:            m.ID.ValueString(),
		Name:          m.Name.ValueString(),
		Domains:       domains,
		Routes:        routes,
		IP4:           nullableString(m.IP4ID),
		IP6:           nullableString(m.IP6ID),
		Cert:          nullableString(m.CertID),
		PrimaryDomain: nullableString(m.PrimaryDomainID),
		Redirect:      knownBool(m.Redirect),
		GenerateLE:    knownBool(m.GenerateLE),
		Disabled:      knownBool(m.Disabled),
	}); err != nil {
		resp.Diagnostics.AddError("Cannot update the site", err.Error())
		return
	}
	ready, err := sites.WaitReady(ctx, m.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("The site did not become ready", err.Error())
		return
	}
	resp.Diagnostics.Append(m.set(ctx, ready)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *siteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	deleteResource(ctx, req, resp, r.client.Sites(), "site")
}

func (r *siteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importByIDOrName(ctx, r.client.Sites().ReadKind, "site",
		func(s client.Site) (string, string) { return s.Name, s.ID }, req, resp)
}
