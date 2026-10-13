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
	// ErrFailed means the API gave up on an object: it reports a state such as
	// INSTALL_FAILED, which an app reaches when its installer does not succeed.
	// The object still exists and can be deleted.
	ErrFailed = errors.New("opalstack: the object failed")
	// ErrReadOnly means the client is configured read-only and refused to send
	// anything but a GET.
	ErrReadOnly = errors.New("opalstack: refused because the client is read-only")
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

// newAPIError builds the error for a non-2xx answer. secrets are values that
// must not appear in it: see secretsIn.
//
// Messages come from the unredacted body, because redaction works on key names
// and would blank the text of every validation error on a field such as
// password, which is exactly the error a user needs to read. errorMessages
// redacts bare values under such keys, and a message that echoes one of the
// secrets is dropped here.
func newAPIError(method, path string, status int, body []byte, secrets []string) *APIError {
	secret := secretKeysOf(path)
	e := &APIError{Method: method, Path: path, Status: status, Body: redactJSON(body, secret)}
	for _, msg := range errorMessages(body, secret) {
		// A value too short to be told from ordinary words is not looked for: an
		// empty or one-letter content would blank every message.
		if slices.ContainsFunc(secrets, func(s string) bool { return len(s) >= minEchoedSecret && strings.Contains(msg, s) }) {
			msg = redacted
		}
		e.Messages = append(e.Messages, msg)
	}
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
//
// Under a secret-looking key, a string inside a list is taken as a validation
// message, which is how the API reports field errors, and a bare string is taken
// as a value and redacted. secret is the rule for such keys: see secretKeysOf.
func errorMessages(body []byte, secret func(string) bool) []string {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return nil
	}
	var out []string
	collectMessages("", v, false, secret, &out)
	return out
}

func collectMessages(field string, v any, inList bool, secret func(string) bool, out *[]string) {
	switch t := v.(type) {
	case string:
		switch {
		case field == "" || field == "detail" || field == "errors" || field == "non_field_errors":
			*out = append(*out, t)
		case secret(field) && !inList:
			*out = append(*out, field+": "+redacted)
		default:
			*out = append(*out, field+": "+t)
		}
	case []any:
		for _, item := range t {
			collectMessages(field, item, field != "", secret, out)
		}
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(t)) {
			collectMessages(k, t[k], false, secret, out)
		}
	}
}

// secretsIn returns the token and every string a request to path carries under
// a secret-looking key, which is what an error body could echo back.
func (c *Client) secretsIn(path string, request []byte) []string {
	secrets := []string{c.cfg.Token}
	var v any
	if json.Unmarshal(request, &v) == nil {
		collectSecrets(v, false, secretKeysOf(path), &secrets)
	}
	return secrets
}

func collectSecrets(v any, underSecretKey bool, secret func(string) bool, out *[]string) {
	switch t := v.(type) {
	case string:
		if underSecretKey && t != "" {
			*out = append(*out, t)
		}
	case []any:
		for _, item := range t {
			collectSecrets(item, underSecretKey, secret, out)
		}
	case map[string]any:
		for k, val := range t {
			collectSecrets(val, underSecretKey || secret(k), secret, out)
		}
	}
}

var secretKey = regexp.MustCompile(`(?i)password|secret|key|token`)

// IsSecretKey reports whether a JSON key looks like it holds a secret, by the
// same rule the client redacts its own logs and errors with.
func IsSecretKey(key string) bool { return secretKey.MatchString(key) }

// secretKeysOf returns the rule for the keys of a body sent to path or received
// from it. A name says nothing about the content of an environment variable,
// which is as likely a secret as not, so under /osvar/ it is one. Elsewhere
// content stays readable: it is also the value of a DNS record.
func secretKeysOf(path string) func(string) bool {
	if strings.HasPrefix(path, "/osvar/") {
		return func(key string) bool { return key == "content" || secretKey.MatchString(key) }
	}
	return secretKey.MatchString
}

const redacted = "REDACTED"

// minEchoedSecret is the shortest secret value that an error message is
// searched for.
const minEchoedSecret = 6

// redactJSON returns body with the value of every key that secret matches
// replaced. The pattern is broad on purpose: it also covers dkim_privkey, which
// the API returns in every domain response. A body that is not JSON is
// returned as is.
func redactJSON(body []byte, secret func(string) bool) string {
	if len(body) == 0 {
		return ""
	}
	var v any
	if json.Unmarshal(body, &v) != nil {
		return string(body)
	}
	out, err := json.Marshal(redactValue(v, secret))
	if err != nil {
		return redacted
	}
	return string(out)
}

func redactValue(v any, secret func(string) bool) any {
	switch t := v.(type) {
	case []any:
		for i := range t {
			t[i] = redactValue(t[i], secret)
		}
	case map[string]any:
		for k, val := range t {
			if secret(k) && val != nil {
				t[k] = redacted
			} else {
				t[k] = redactValue(val, secret)
			}
		}
	}
	return v
}
