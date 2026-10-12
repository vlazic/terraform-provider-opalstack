package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vlazic/terraform-provider-opalstack/internal/client"
)

// defaultTimeout bounds each wait for the API to finish a queued change, unless
// the resource's timeouts block says otherwise.
const defaultTimeout = 10 * time.Minute

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

// deleteResource is the whole Delete of a resource: it queues the deletion and
// waits, within the delete timeout, for the object to be gone. An object that
// is already gone is not an error.
func deleteResource(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse, k interface {
	Delete(context.Context, string) error
	WaitDeleted(context.Context, string) error
}, what string) {
	var id types.String
	var configured timeouts.Value
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("timeouts"), &configured)...)
	ctx, cancel := withTimeout(ctx, configured.Delete, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}
	if err := k.Delete(ctx, id.ValueString()); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Cannot delete the "+what, err.Error())
		}
		return
	}
	if err := k.WaitDeleted(ctx, id.ValueString()); err != nil {
		resp.Diagnostics.AddError("The "+what+" was not deleted in time", err.Error())
	}
}

// withTimeout bounds ctx by one of a resource's configured timeouts. The caller
// checks diags afterwards, which also covers reading the plan or state before.
func withTimeout(ctx context.Context, configured func(context.Context, time.Duration) (time.Duration, diag.Diagnostics), diags *diag.Diagnostics) (context.Context, context.CancelFunc) {
	timeout, d := configured(ctx, defaultTimeout)
	diags.Append(d...)
	return context.WithTimeout(ctx, timeout)
}

// importByIDOrName sets the id attribute from an import id that is either the
// UUID of the object or its name.
func importByIDOrName[T any](ctx context.Context, k client.ReadKind[T], what string, nameAndID func(T) (name, id string), req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if !client.IsUUID(id) {
		obj, diags := findOne(ctx, k, what, func(o T) bool {
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

// knownBool, knownInt64 and knownString return nil unless the value is set. The framework's
// own pointer getters return a pointer to the zero value for an unknown one,
// which would send false or 0 where the API is meant to choose.
func knownBool(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	return v.ValueBoolPointer()
}

func knownInt64(v types.Int64) *int64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	return v.ValueInt64Pointer()
}

func knownString(v types.String) *string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	return v.ValueStringPointer()
}

// changedString is for a reference an update can set or clear. It is sent only
// when the plan differs from the state: sending an unchanged value back would
// overwrite whatever the API attached in the meantime, a certificate for one.
func changedString(plan, state types.String) client.Nullable[string] {
	if plan.IsUnknown() || plan.Equal(state) {
		return client.Nullable[string]{}
	}
	return client.FromPtr(plan.ValueStringPointer())
}
