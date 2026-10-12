package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ReadKind is the read side of a kind of object. T is the response type.
type ReadKind[T any] struct {
	c    *Client
	name string

	// parentField and parentKind are set for a kind whose objects have no name
	// of their own. The safety prefix is then checked on the name of the parent
	// that parentField points at.
	parentField string
	parentKind  string
}

// List returns every object of the kind. The API does not paginate.
func (k ReadKind[T]) List(ctx context.Context) ([]T, error) {
	var out []T
	if err := k.c.get(ctx, "/"+k.name+"/list/", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Read returns one object, or an error matching ErrNotFound.
func (k ReadKind[T]) Read(ctx context.Context, id string) (*T, error) {
	return read[T](ctx, k.c, k.name, id)
}

func read[T any](ctx context.Context, c *Client, kind, id string) (*T, error) {
	// An id that is not a UUID is answered with an HTML 404, which would read as
	// "deleted". Refuse anything that could also change the path.
	if id == "" || strings.ContainsAny(id, "/?#") {
		return nil, fmt.Errorf("opalstack: %q is not a valid %s id", id, kind)
	}
	var out T
	if err := c.get(ctx, "/"+kind+"/read/"+id, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Kind adds the write side. C is the create request and U the update request.
type Kind[T statused, C, U any] struct {
	ReadKind[T]
}

// Create creates one object and returns it as the API first reports it, which
// is usually not yet ready. Anything present only in the create response, such
// as a shell user's default_password, is in this value and nowhere else.
func (k Kind[T, C, U]) Create(ctx context.Context, req C) (*T, error) {
	return k.postOne(ctx, "create", req)
}

// Update sends the fields set on req and leaves every other field as it is.
func (k Kind[T, C, U]) Update(ctx context.Context, req U) (*T, error) {
	return k.postOne(ctx, "update", req)
}

// Delete queues the object for deletion. Use WaitDeleted to wait for it to go.
func (k Kind[T, C, U]) Delete(ctx context.Context, id string) error {
	req := map[string]string{"id": id}
	if err := k.guard(ctx, "delete", req); err != nil {
		return err
	}
	// The answer is an object such as {"ok":"Queued for deletion."}, not an array.
	return k.c.post(ctx, "/"+k.name+"/delete/", []any{req}, nil)
}

// WaitReady polls until the object reports ready, and returns it as last read.
func (k Kind[T, C, U]) WaitReady(ctx context.Context, id string) (*T, error) {
	var obj *T
	err := k.c.poll(ctx, k.name+" "+id+" to become ready", func() (bool, string, error) {
		var err error
		if obj, err = k.Read(ctx, id); err != nil {
			return false, "", err
		}
		st := (*obj).status()
		return st.Ready, st.State, nil
	})
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// WaitDeleted polls until a read of the object answers not found, which is how
// the official Python wrapper detects that a queued deletion has finished.
func (k Kind[T, C, U]) WaitDeleted(ctx context.Context, id string) error {
	return k.c.poll(ctx, k.name+" "+id+" to be deleted", func() (bool, string, error) {
		obj, err := k.Read(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return true, "", nil
		}
		if err != nil {
			return false, "", err
		}
		return false, (*obj).status().State, nil
	})
}

// postOne sends a one-element array and returns the one element that comes back.
// Requests are never batched: one object per call keeps errors attributable.
func (k Kind[T, C, U]) postOne(ctx context.Context, verb string, req any) (*T, error) {
	if err := k.guard(ctx, verb, req); err != nil {
		return nil, err
	}
	path := "/" + k.name + "/" + verb + "/"
	var out []T
	if err := k.c.post(ctx, path, []any{req}, &out); err != nil {
		return nil, err
	}
	if len(out) != 1 {
		return nil, fmt.Errorf("opalstack: POST %s returned %d objects, expected 1", path, len(out))
	}
	return &out[0], nil
}

// guard enforces the safety prefix on a write, and does nothing when no prefix
// is configured. It works on the request as it will be sent, so it needs no
// knowledge of the request types:
//
//   - a request with an "id" targets an existing object, whose current name is
//     looked up, because the prefix is a property of the name and not of the id;
//   - a request with a "name" creates or renames, and the new name is checked;
//   - a request with the kind's parent field is checked on that parent's name.
//
// Anything it cannot establish is a refusal.
func (k Kind[T, C, U]) guard(ctx context.Context, verb string, req any) error {
	prefix := k.c.cfg.SafetyPrefix
	if prefix == "" {
		return nil
	}
	refuse := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s %s: "+format, append([]any{ErrSafetyPrefix, k.name, verb}, args...)...)
	}

	raw, err := json.Marshal(req)
	if err != nil {
		return refuse("cannot inspect the request: %w", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return refuse("cannot inspect the request: %w", err)
	}
	field := func(name string) string { s, _ := fields[name].(string); return s }

	// Each entry is a name that must carry the prefix, with how it was found.
	type named struct{ what, name string }
	var names []named

	if id := field("id"); id != "" {
		name, err := k.nameOf(ctx, id)
		if err != nil {
			return refuse("cannot read %s to check its name: %w", id, err)
		}
		names = append(names, named{"its current name", name})
	}
	if name := field("name"); name != "" {
		names = append(names, named{"the requested name", name})
	}
	if parentID := field(k.parentField); k.parentField != "" && parentID != "" {
		name, err := k.parent().nameOf(ctx, parentID)
		if err != nil {
			return refuse("cannot read %s %s to check its name: %w", k.parentKind, parentID, err)
		}
		names = append(names, named{"the name of its " + k.parentKind, name})
	}

	if len(names) == 0 {
		return refuse("the request names nothing the prefix %q could be checked against", prefix)
	}
	for _, n := range names {
		if !strings.HasPrefix(n.name, prefix) {
			return refuse("%s, %q, does not start with %q", n.what, n.name, prefix)
		}
	}
	return nil
}

func (k ReadKind[T]) parent() ReadKind[map[string]any] {
	return ReadKind[map[string]any]{c: k.c, name: k.parentKind}
}

// nameOf returns the name the safety prefix applies to for an existing object:
// its own, or its parent's when the kind has a parent field.
func (k ReadKind[T]) nameOf(ctx context.Context, id string) (string, error) {
	obj, err := read[map[string]any](ctx, k.c, k.name, id)
	if err != nil {
		return "", err
	}
	if k.parentField != "" {
		parentID, _ := (*obj)[k.parentField].(string)
		if parentID == "" {
			return "", fmt.Errorf("opalstack: %s %s has no %s", k.name, id, k.parentField)
		}
		return k.parent().nameOf(ctx, parentID)
	}
	name, _ := (*obj)["name"].(string)
	if name == "" {
		return "", fmt.Errorf("opalstack: %s %s has no name", k.name, id)
	}
	return name, nil
}

func kind[T statused, C, U any](c *Client, name string) Kind[T, C, U] {
	return Kind[T, C, U]{ReadKind[T]{c: c, name: name}}
}
