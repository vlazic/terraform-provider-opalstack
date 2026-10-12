package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

// defaultTimeout bounds each wait for the API to finish a queued change, unless
// the resource's timeouts block says otherwise.
const defaultTimeout = 10 * time.Minute

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`)

// baseResource is what every resource shares: the configured client.
type baseResource struct {
	client *client.Client
}

func (r *baseResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return // the provider is not configured yet
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T.", req.ProviderData))
		return
	}
	r.client = c
}

// readForState reads an object for a resource's Read. An object that is gone is
// removed from state and nil is returned without an error. Any other failure,
// a 403 in particular, is an error: an object hidden by a token ACL still exists.
func readForState[T any](ctx context.Context, k client.ReadKind[T], what, id string, resp *resource.ReadResponse) *T {
	obj, err := k.Read(ctx, id)
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return nil
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the "+what, err.Error())
		return nil
	}
	return obj
}

// deleteAndWait queues the deletion and waits for the object to be gone. An
// object that is already gone is not an error.
func deleteAndWait[K interface {
	Delete(context.Context, string) error
	WaitDeleted(context.Context, string) error
}](ctx context.Context, k K, what, id string) diag.Diagnostics {
	var diags diag.Diagnostics
	if err := k.Delete(ctx, id); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			diags.AddError("Cannot delete the "+what, err.Error())
		}
		return diags
	}
	if err := k.WaitDeleted(ctx, id); err != nil {
		diags.AddError("The "+what+" was not deleted in time", err.Error())
	}
	return diags
}

// importByIDOrName sets the id attribute from an import id that is either the
// UUID of the object or its name.
func importByIDOrName[T any](ctx context.Context, k client.ReadKind[T], what string, nameAndID func(T) (name, id string), req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if !uuidPattern.MatchString(id) {
		obj, diags := find(ctx, k, what, types.StringNull(), func(o T) bool {
			name, _ := nameAndID(o)
			return name == req.ID
		})
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		_, id = nameAndID(*obj)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}
