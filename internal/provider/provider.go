// Package provider is the Terraform side: schema, configuration and, later,
// the resources and data sources. All API access goes through internal/client.
package provider

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

const tokenEnvVar = "OPALSTACK_TOKEN"

var _ provider.Provider = (*opalstackProvider)(nil)

type opalstackProvider struct {
	version string
	// httpClient is nil outside the tests, which use it to reach a fake API.
	httpClient *http.Client
}

type providerModel struct {
	Token          types.String `tfsdk:"token"`
	BaseURL        types.String `tfsdk:"base_url"`
	SafetyPrefix   types.String `tfsdk:"safety_prefix"`
	SafetyOSUserID types.String `tfsdk:"safety_osuser_id"`
	RequestTimeout types.String `tfsdk:"request_timeout"`
}

// New returns the provider factory that main and the tests serve.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &opalstackProvider{version: version}
	}
}

func (p *opalstackProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "opalstack"
	resp.Version = p.version
}

func (p *opalstackProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages resources of an [Opalstack](https://www.opalstack.com) hosting account through its REST API.",
		Attributes: map[string]schema.Attribute{
			"token": schema.StringAttribute{
				MarkdownDescription: "API token. Defaults to the `" + tokenEnvVar + "` environment variable.",
				Optional:            true,
				Sensitive:           true,
			},
			"base_url": schema.StringAttribute{
				MarkdownDescription: "API base URL. Defaults to `" + client.DefaultBaseURL + "`.",
				Optional:            true,
			},
			"safety_prefix": schema.StringAttribute{
				MarkdownDescription: "When set, the provider refuses to create, update or delete any object whose name " +
					"does not start with this prefix, or that refers to one: the domains and apps of a site and " +
					"the shell user of an app are checked too. A DNS record is judged by the name of its domain. " +
					"Meant for development and testing against an account that also holds real objects.",
				Optional: true,
			},
			"safety_osuser_id": schema.StringAttribute{
				MarkdownDescription: "Used with `safety_prefix`: the id of the one shell user that apps may be created " +
					"under although its name lacks the prefix. The user itself is still never changed or deleted.",
				Optional: true,
			},
			"request_timeout": schema.StringAttribute{
				MarkdownDescription: "Limit for a single API request, as a duration such as `45s`. Defaults to `" +
					client.DefaultRequestTimeout.String() + "`. " +
					"Waiting for an object to become ready is governed by each resource's `timeouts` instead.",
				Optional: true,
			},
		},
	}
}

func (p *opalstackProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A value that is only known after apply cannot configure a client now.
	for _, setting := range []struct {
		name  string
		value types.String
	}{
		{"token", cfg.Token}, {"base_url", cfg.BaseURL}, {"safety_prefix", cfg.SafetyPrefix},
		{"safety_osuser_id", cfg.SafetyOSUserID}, {"request_timeout", cfg.RequestTimeout},
	} {
		if setting.value.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(setting.name), "Unknown provider setting",
				"The value of "+setting.name+" is not known until apply. Set it to a value that is known at plan time.")
		}
	}

	// The token travels in a header on every request, so plain http is refused.
	if s := cfg.BaseURL.ValueString(); s != "" {
		if u, err := url.Parse(s); err != nil || u.Scheme != "https" || u.Host == "" {
			resp.Diagnostics.AddAttributeError(path.Root("base_url"), "Invalid base_url",
				"Expected an https URL such as "+client.DefaultBaseURL+", got "+s+".")
		}
	}

	token := strings.TrimSpace(cfg.Token.ValueString())
	if token == "" {
		token = strings.TrimSpace(os.Getenv(tokenEnvVar))
	}
	if token == "" && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddAttributeError(path.Root("token"), "Missing Opalstack API token",
			"Set the token attribute or the "+tokenEnvVar+" environment variable.")
	}

	var timeout time.Duration
	if s := cfg.RequestTimeout.ValueString(); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			resp.Diagnostics.AddAttributeError(path.Root("request_timeout"), "Invalid request_timeout",
				"Expected a positive duration such as 30s or 2m, got "+s+".")
		}
		timeout = d
	}
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := client.New(client.Config{
		Token:          token,
		BaseURL:        cfg.BaseURL.ValueString(),
		UserAgent:      "terraform-provider-opalstack/" + p.version,
		SafetyPrefix:   cfg.SafetyPrefix.ValueString(),
		SafetyOSUserID: cfg.SafetyOSUserID.ValueString(),
		RequestTimeout: timeout,
		HTTPClient:     p.httpClient,
		Log: func(ctx context.Context, msg string, fields map[string]any) {
			tflog.Debug(ctx, msg, fields)
		},
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the Opalstack client", err.Error())
		return
	}
	if p.httpClient != nil {
		c.SetPacing(0, time.Millisecond) // a fake API needs no pacing
	}
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *opalstackProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{newDomainResource, newDNSRecordResource, newOSUserResource, newAppResource}
}

func (p *opalstackProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newServerDataSource, newIPDataSource, newOSUserDataSource, newDomainDataSource, newAppDataSource,
	}
}
