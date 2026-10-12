package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// guardTarget says what the safety prefix is checked against for a create: the
// new object's own name, or, for an object without one, the name of its parent.
type guardTarget struct {
	name       string
	parentKind string
	parentID   string
}

type creatable interface{ guardTarget() guardTarget }

type updatable interface{ objectID() string }

// Kind is the set of operations shared by every managed kind of object. T is the
// response type, C the create request and U the update request.
type Kind[T any, C creatable, U updatable] struct {
	c    *Client
	name string
}

// List returns every object of the kind. The API does not paginate.
func (k Kind[T, C, U]) List(ctx context.Context) ([]T, error) {
	return list[T](ctx, k.c, k.name)
}

// Read returns one object, or an error matching ErrNotFound.
func (k Kind[T, C, U]) Read(ctx context.Context, id string) (*T, error) {
	return read[T](ctx, k.c, k.name, id)
}

// Create creates one object and returns it as the API first reports it, which
// is usually not yet ready. Anything present only in the create response, such
// as a shell user's default_password, is in this value and nowhere else.
func (k Kind[T, C, U]) Create(ctx context.Context, req C) (*T, error) {
	if err := k.c.guardCreate(ctx, k.name, req.guardTarget()); err != nil {
		return nil, err
	}
	return k.postOne(ctx, "create", req)
}

// Update sends the fields set on req and leaves every other field as it is.
func (k Kind[T, C, U]) Update(ctx context.Context, req U) (*T, error) {
	if err := k.c.guardExisting(ctx, k.name, req.objectID()); err != nil {
		return nil, err
	}
	return k.postOne(ctx, "update", req)
}

// Delete queues the object for deletion. Use WaitDeleted to wait for it to go.
func (k Kind[T, C, U]) Delete(ctx context.Context, id string) error {
	if err := k.c.guardExisting(ctx, k.name, id); err != nil {
		return err
	}
	// The answer is an object such as {"ok":"Queued for deletion."}, not an array.
	return k.c.post(ctx, "/"+k.name+"/delete/", []map[string]string{{"id": id}}, nil)
}

// WaitReady polls until the object reports ready, and returns it.
func (k Kind[T, C, U]) WaitReady(ctx context.Context, id string) (*T, error) {
	if err := k.c.waitReady(ctx, k.name, id); err != nil {
		return nil, err
	}
	return k.Read(ctx, id)
}

// WaitDeleted polls until a read of the object answers not found.
func (k Kind[T, C, U]) WaitDeleted(ctx context.Context, id string) error {
	return k.c.waitDeleted(ctx, k.name, id)
}

// postOne sends a one-element array and returns the one element that comes back.
// Requests are never batched: one object per call keeps errors attributable.
func (k Kind[T, C, U]) postOne(ctx context.Context, verb string, req any) (*T, error) {
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

func list[T any](ctx context.Context, c *Client, kind string) ([]T, error) {
	var out []T
	if err := c.get(ctx, "/"+kind+"/list/", &out); err != nil {
		return nil, err
	}
	return out, nil
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

func (c *Client) guardCreate(ctx context.Context, kind string, t guardTarget) error {
	if c.cfg.SafetyPrefix == "" {
		return nil
	}
	if t.parentKind != "" {
		name, err := c.nameOf(ctx, t.parentKind, t.parentID)
		if err != nil {
			return fmt.Errorf("%w: cannot check the %s of the new %s: %w", ErrSafetyPrefix, t.parentKind, kind, err)
		}
		return c.checkPrefix(t.parentKind, name)
	}
	return c.checkPrefix(kind, t.name)
}

// guardExisting looks the object up before a write, because an update or delete
// request carries an id and the prefix is a property of the name.
func (c *Client) guardExisting(ctx context.Context, kind, id string) error {
	if c.cfg.SafetyPrefix == "" {
		return nil
	}
	name, err := c.nameOf(ctx, kind, id)
	if err != nil {
		return fmt.Errorf("%w: cannot read %s %s to check its name: %w", ErrSafetyPrefix, kind, id, err)
	}
	return c.checkPrefix(kind, name)
}

func (c *Client) checkPrefix(kind, name string) error {
	if !strings.HasPrefix(name, c.cfg.SafetyPrefix) {
		return fmt.Errorf("%w: %s %q does not start with %q", ErrSafetyPrefix, kind, name, c.cfg.SafetyPrefix)
	}
	return nil
}

// nameOf returns the name the safety prefix applies to: the object's own, or
// its domain's when the object is a DNS record.
func (c *Client) nameOf(ctx context.Context, kind, id string) (string, error) {
	obj, err := read[map[string]json.RawMessage](ctx, c, kind, id)
	if err != nil {
		return "", err
	}
	field := "name"
	if kind == kindDNSRecord {
		field = "domain"
	}
	var value string
	if err := json.Unmarshal((*obj)[field], &value); err != nil || value == "" {
		return "", fmt.Errorf("opalstack: %s %s has no %s", kind, id, field)
	}
	if kind == kindDNSRecord {
		return c.nameOf(ctx, kindDomain, value)
	}
	return value, nil
}
