package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

// baseDataSource is what every data source shares: the configured client.
type baseDataSource struct {
	client *client.Client
}

func (d *baseDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return // the provider is not configured yet
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T.", req.ProviderData))
		return
	}
	d.client = c
}

// lookupAttribute is an attribute an object can be looked up by. It is also
// filled in from the object found, whichever attribute the lookup used.
func lookupAttribute(description string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: description, Optional: true, Computed: true}
}

func computedString(description string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: description, Computed: true}
}

func computedBool(description string) schema.BoolAttribute {
	return schema.BoolAttribute{MarkdownDescription: description, Computed: true}
}

// idOr requires a lookup by id or by the other attribute, and not by both.
func idOr(other string) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot(other)),
	}
}

// find returns the object with the given id when one is set, and otherwise the
// one object of the kind that match accepts.
func find[T any](ctx context.Context, k client.ReadKind[T], what string, id types.String, match func(T) bool) (*T, diag.Diagnostics) {
	var diags diag.Diagnostics
	if v := id.ValueString(); v != "" {
		obj, err := k.Read(ctx, v)
		if err != nil {
			diags.AddError("Cannot read the "+what, err.Error())
		}
		return obj, diags
	}
	all, err := k.List(ctx)
	if err != nil {
		diags.AddError("Cannot list the "+what+"s", err.Error())
		return nil, diags
	}
	return only(all, what, match)
}

// only returns the single item that match accepts. Names are not known to be
// unique, so more than one match is an error and never a pick.
func only[T any](items []T, what string, match func(T) bool) (*T, diag.Diagnostics) {
	var diags diag.Diagnostics
	var found *T
	n := 0
	for i := range items {
		if match(items[i]) {
			found = &items[i]
			n++
		}
	}
	switch n {
	case 1:
		return found, diags
	case 0:
		diags.AddError("No "+what+" matches", "No "+what+" visible to this token matches the given attributes.")
	default:
		diags.AddError("More than one "+what+" matches",
			fmt.Sprintf("%d %ss match the given attributes. Narrow the lookup, or look it up by id.", n, what))
	}
	return nil, diags
}
