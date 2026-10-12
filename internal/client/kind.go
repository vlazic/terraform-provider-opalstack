package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ReadKind is the read side of a kind of object. T is the response type.
type ReadKind[T any] struct {
	c    *Client
	name string

	// refs are the request fields that point at other objects. The safety
	// prefix is checked on each object a write refers to.
	refs []ref
	// nameless marks a kind whose objects have no name of their own. Such an
	// object is judged by the name of the object its first ref points at.
	nameless bool
}

// ref is a field holding the id of an object of another kind. path is dotted:
// "routes.app" is the app of each element of routes. The field may hold one id
// or a list of them.
//
// exempt, when set, is the one id the field may hold although that object's
// name lacks the safety prefix.
type ref struct{ path, kind, exempt string }

func (r ref) ids(fields map[string]any) []string {
	return refIDs(fields, strings.Split(r.path, "."))
}

func refIDs(v any, path []string) []string {
	switch t := v.(type) {
	case string:
		if len(path) == 0 && t != "" {
			return []string{t}
		}
	case []any:
		var out []string
		for _, item := range t {
			out = append(out, refIDs(item, path)...)
		}
		return out
	case map[string]any:
		if len(path) > 0 {
			return refIDs(t[path[0]], path[1:])
		}
	}
	return nil
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
	// The API answers 404 for an id that is not a UUID, which would read as
	// "deleted". Such an id is refused here, which also keeps it out of the path.
	if !IsUUID(id) {
		return nil, fmt.Errorf("opalstack: %q is not a valid %s id: expected a UUID", id, kind)
	}
	var out T
	if err := c.get(ctx, "/"+kind+"/read/"+id, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`)

// IsUUID reports whether s has the form of an object id.
func IsUUID(s string) bool { return uuidPattern.MatchString(s) }

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
//   - every object the request refers to through one of the kind's refs, such
//     as the domains and apps of a site, is checked on its name too. The one
//     exception is a ref's exempt id.
//
// Anything it cannot establish is a refusal. A refusal never matches
// ErrNotFound, even when a lookup failed that way: the object the caller asked
// about may well exist, and "not found" would let a resource drop it from state.
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

	// Each name is checked as soon as it is known, so a request that fails on
	// its own name costs no lookup. checked counts them: a request that names
	// nothing at all cannot be judged and is refused.
	checked := 0
	check := func(what, name string) error {
		checked++
		if !strings.HasPrefix(name, prefix) {
			return refuse("%s, %q, does not start with %q", what, name, prefix)
		}
		return nil
	}

	id := field("id")
	if id == "" && verb != "create" {
		return refuse("the request has no id")
	}
	if name := field("name"); name != "" {
		if err := check("the requested name", name); err != nil {
			return err
		}
	}
	if id != "" {
		name, err := k.nameOf(ctx, id)
		if err != nil {
			return refuse("cannot read %s to check its name: %v", id, err)
		}
		if err := check("its current name", name); err != nil {
			return err
		}
	}
	seen := map[ref]bool{} // a site may route several paths to one app
	for _, r := range k.refs {
		for _, refID := range r.ids(fields) {
			target := ref{kind: r.kind, path: refID}
			if (r.exempt != "" && refID == r.exempt) || seen[target] {
				continue
			}
			seen[target] = true
			name, err := k.other(r.kind).nameOf(ctx, refID)
			if err != nil {
				return refuse("cannot read %s %s to check its name: %v", r.kind, refID, err)
			}
			if err := check("the name of the "+r.kind+" it refers to", name); err != nil {
				return err
			}
		}
	}
	if checked == 0 {
		return refuse("the request names nothing the prefix %q could be checked against", prefix)
	}
	return nil
}

func (k ReadKind[T]) other(kind string) ReadKind[map[string]any] {
	return ReadKind[map[string]any]{c: k.c, name: kind}
}

// nameOf returns the name the safety prefix applies to for an existing object:
// its own, or for a nameless kind that of the object its first ref points at.
func (k ReadKind[T]) nameOf(ctx context.Context, id string) (string, error) {
	obj, err := read[map[string]any](ctx, k.c, k.name, id)
	if err != nil {
		return "", err
	}
	if k.nameless {
		parent := k.refs[0]
		ids := parent.ids(*obj)
		if len(ids) != 1 {
			return "", fmt.Errorf("opalstack: %s %s has no %s", k.name, id, parent.path)
		}
		return k.other(parent.kind).nameOf(ctx, ids[0])
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
