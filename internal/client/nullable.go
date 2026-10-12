package client

import "encoding/json"

// Nullable is an optional request field that can also be sent as an explicit
// JSON null. Its zero value is "leave unchanged" and is left out of the request
// when the field is tagged omitzero.
type Nullable[T any] struct {
	set   bool
	value *T
}

// Set sends v.
func Set[T any](v T) Nullable[T] { return Nullable[T]{set: true, value: &v} }

// Null sends null, which clears the field on the server.
func Null[T any]() Nullable[T] { return Nullable[T]{set: true} }

// FromPtr sends *p, or null when p is nil.
func FromPtr[T any](p *T) Nullable[T] { return Nullable[T]{set: true, value: p} }

func (n Nullable[T]) IsZero() bool { return !n.set }

func (n Nullable[T]) MarshalJSON() ([]byte, error) { return json.Marshal(n.value) }
