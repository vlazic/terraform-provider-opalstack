package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
)

var (
	// ErrNotFound means the API answered 404: the object does not exist.
	ErrNotFound = errors.New("opalstack: not found")
	// ErrForbidden means the API answered 403. It is deliberately distinct from
	// ErrNotFound: an object hidden by a token ACL has not been deleted.
	ErrForbidden = errors.New("opalstack: forbidden")
	// ErrUnauthorized means the API rejected the token.
	ErrUnauthorized = errors.New("opalstack: unauthorized")
	// ErrSafetyPrefix means the client refused a write because the target's name
	// lacks the configured safety prefix. No request was sent for that write.
	ErrSafetyPrefix = errors.New("opalstack: refused by the safety prefix")
)

const maxErrorBody = 2000

// APIError is any non-2xx answer from the API.
type APIError struct {
	Method string
	Path   string
	Status int
	// Messages are the human-readable parts of the body, when it was JSON in one
	// of the shapes the API is known to use.
	Messages []string
	// Body is the redacted body, truncated. It is kept because the API uses
	// several error shapes and answers some requests with HTML.
	Body string
}

func newAPIError(method, path string, status int, body []byte) *APIError {
	// Messages are taken from the redacted body, so a secret echoed back in an
	// error cannot reach a diagnostic through them.
	e := &APIError{Method: method, Path: path, Status: status, Body: redactJSON(body)}
	e.Messages = errorMessages([]byte(e.Body))
	if len(e.Body) > maxErrorBody {
		e.Body = e.Body[:maxErrorBody] + "..."
	}
	return e
}

func (e *APIError) Error() string {
	detail := strings.Join(e.Messages, "; ")
	if detail == "" {
		detail = e.Body
	}
	return fmt.Sprintf("opalstack: %s %s: HTTP %d: %s", e.Method, e.Path, e.Status, detail)
}

// Is maps the status to the sentinel errors, so callers use errors.Is.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	case ErrForbidden:
		return e.Status == http.StatusForbidden
	case ErrUnauthorized:
		return e.Status == http.StatusUnauthorized
	}
	return false
}

// errorMessages flattens the known error shapes into "field: message" strings:
// {"id": ["Not found."]}, {"errors": ["..."]}, {"detail": "..."}, and an array
// of such objects, one per element of the request. Anything else yields nil.
func errorMessages(body []byte) []string {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return nil
	}
	var out []string
	collectMessages("", v, &out)
	return out
}

func collectMessages(field string, v any, out *[]string) {
	switch t := v.(type) {
	case string:
		if field == "" || field == "detail" || field == "errors" || field == "non_field_errors" {
			*out = append(*out, t)
		} else {
			*out = append(*out, field+": "+t)
		}
	case []any:
		for _, item := range t {
			collectMessages(field, item, out)
		}
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(t)) {
			collectMessages(k, t[k], out)
		}
	}
}

var secretKey = regexp.MustCompile(`(?i)password|secret|key|token`)

const redacted = "REDACTED"

// redactJSON returns body with the value of every secret-looking key replaced.
// The pattern is broad on purpose: it also covers dkim_privkey, which the API
// returns in every domain response. A body that is not JSON is returned as is.
func redactJSON(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var v any
	if json.Unmarshal(body, &v) != nil {
		return string(body)
	}
	out, err := json.Marshal(redactValue(v))
	if err != nil {
		return redacted
	}
	return string(out)
}

func redactValue(v any) any {
	switch t := v.(type) {
	case []any:
		for i := range t {
			t[i] = redactValue(t[i])
		}
	case map[string]any:
		for k, val := range t {
			if secretKey.MatchString(k) && val != nil {
				t[k] = redacted
			} else {
				t[k] = redactValue(val)
			}
		}
	}
	return v
}
