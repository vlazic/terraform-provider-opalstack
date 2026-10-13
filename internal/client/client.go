// Package client is a thin HTTP client for the Opalstack REST API v1.
//
// It knows nothing about Terraform. The shapes it relies on are recorded, with
// their sources, in API_FINDINGS.md at the repository root.
package client

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBaseURL        = "https://my.opalstack.com/api/v1"
	DefaultRequestTimeout = 30 * time.Second

	maxAttempts = 4
	// maxRetryAfter caps how long a Retry-After header is obeyed.
	maxRetryAfter = time.Minute

	maxResponseBytes = 16 << 20
)

// LogFunc receives one debug record per request. Fields are already redacted.
type LogFunc func(ctx context.Context, msg string, fields map[string]any)

// Config configures a Client. Only Token is required.
type Config struct {
	Token     string
	BaseURL   string
	UserAgent string

	// SafetyPrefix, when set, makes the client refuse to create, update or
	// delete any object whose name does not start with it. A DNS record has no
	// name of its own and is judged by the name of its domain.
	SafetyPrefix string

	// SafetyOSUserID is the one shell user that an app may be created under
	// although its name lacks the safety prefix. Every other object a write
	// refers to, such as the domains and apps of a site, must carry the prefix.
	// The user itself is still never updated or deleted.
	SafetyOSUserID string

	// ReadOnly makes the client refuse every request that is not a GET, before
	// it is sent. Reading, planning and importing work; nothing can be changed.
	ReadOnly bool

	// RequestTimeout bounds a single HTTP round trip.
	RequestTimeout time.Duration

	// RequestInterval is the spacing between requests and PollInterval the first
	// delay between the polls of a wait. The defaults, about 3 requests per
	// second and 2 seconds, suit the live API; a fake one needs neither.
	RequestInterval time.Duration
	PollInterval    time.Duration

	HTTPClient *http.Client
	Log        LogFunc
}

// Client talks to one Opalstack account.
type Client struct {
	cfg Config

	// Delays. They are fields so the tests can shrink them.
	minInterval time.Duration // spacing between requests
	retryBase   time.Duration // first retry delay, doubled each attempt
	pollMin     time.Duration // first delay between polls of a wait
	pollMax     time.Duration // cap on the delay between polls

	mu   sync.Mutex
	next time.Time // earliest moment the next request may start
}

// New returns a Client, filling in defaults for anything left zero.
func New(cfg Config) (*Client, error) {
	cfg.Token = strings.TrimSpace(cfg.Token)
	if cfg.Token == "" {
		return nil, fmt.Errorf("opalstack: a token is required")
	}
	cfg.BaseURL = strings.TrimRight(cmp.Or(cfg.BaseURL, DefaultBaseURL), "/")
	// A URL the client cannot use would otherwise fail on every request and be
	// retried as if it were a network fault.
	if u, err := url.Parse(cfg.BaseURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("opalstack: base URL %q is not an http(s) URL with a host", cfg.BaseURL)
	}
	cfg.UserAgent = cmp.Or(cfg.UserAgent, "terraform-provider-opalstack")
	cfg.RequestTimeout = cmp.Or(cfg.RequestTimeout, DefaultRequestTimeout)
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	return &Client{
		cfg:         cfg,
		minInterval: cmp.Or(cfg.RequestInterval, 350*time.Millisecond), // just under 3 requests per second
		retryBase:   time.Second,
		pollMin:     cmp.Or(cfg.PollInterval, 2*time.Second),
		pollMax:     15 * time.Second,
	}, nil
}

// get performs a GET and decodes the response into out.
func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// post performs a POST with a JSON body and decodes the response into out.
func (c *Client) post(ctx context.Context, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("opalstack: encoding the request for %s: %w", path, err)
	}
	return c.do(ctx, http.MethodPost, path, raw, out)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	// Checked here, where every request passes, so no write path can miss it.
	if c.cfg.ReadOnly && method != http.MethodGet {
		return fmt.Errorf("%w: %s %s was not sent", ErrReadOnly, method, path)
	}
	for attempt := 1; ; attempt++ {
		status, respBody, retryAfter, err := c.roundTrip(ctx, method, path, body)
		if err == nil && status >= 200 && status < 300 {
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("opalstack: decoding the response of %s %s: %w", method, path, err)
			}
			return nil
		}
		if err == nil {
			err = newAPIError(method, path, status, respBody, c.secretsIn(path, body))
		}
		if attempt == maxAttempts || !retryable(method, status) || ctx.Err() != nil {
			return err
		}
		delay := max(c.retryBase<<(attempt-1), min(retryAfter, maxRetryAfter))
		if status == http.StatusTooManyRequests {
			// A rate limit applies to the account, not to this call: hold back
			// every other request for as long as this one waits.
			c.holdUntil(time.Now().Add(delay))
		}
		if sleep(ctx, delay) != nil {
			return err
		}
	}
}

// retryable is the whole retry policy. status is 0 for a transport error.
//
// A GET may be sent again after a transport error or any 5xx. A POST may be
// repeated only on 429, which means the request was not processed: after a
// server or transport error the server may have acted on it, and a second
// create would make a second object.
func retryable(method string, status int) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	return method == http.MethodGet && (status == 0 || status >= 500)
}

func (c *Client) roundTrip(ctx context.Context, method, path string, body []byte) (int, []byte, time.Duration, error) {
	if err := c.throttle(ctx); err != nil {
		return 0, nil, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, reader)
	if err != nil {
		return 0, nil, 0, fmt.Errorf("opalstack: building %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Token "+c.cfg.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		c.log(ctx, "opalstack request failed", map[string]any{"method": method, "path": path, "error": err.Error()})
		return 0, nil, 0, fmt.Errorf("opalstack: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, 0, fmt.Errorf("opalstack: reading the response of %s %s: %w", method, path, err)
	}

	secret := secretKeysOf(path)
	c.log(ctx, "opalstack request", map[string]any{
		"method":   method,
		"path":     path,
		"status":   resp.StatusCode,
		"request":  redactJSON(body, secret),
		"response": redactJSON(respBody, secret),
	})

	var retryAfter time.Duration
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
		retryAfter = time.Duration(s) * time.Second
	}
	return resp.StatusCode, respBody, retryAfter, nil
}

// throttle spaces requests at least minInterval apart across all goroutines.
func (c *Client) throttle(ctx context.Context) error {
	// A request that is already cancelled must not take a slot others wait behind.
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	now := time.Now()
	start := c.next
	if start.Before(now) {
		start = now
	}
	c.next = start.Add(c.minInterval)
	c.mu.Unlock()
	return sleep(ctx, start.Sub(now))
}

// holdUntil keeps every request from starting before t.
func (c *Client) holdUntil(t time.Time) {
	c.mu.Lock()
	if c.next.Before(t) {
		c.next = t
	}
	c.mu.Unlock()
}

func (c *Client) log(ctx context.Context, msg string, fields map[string]any) {
	if c.cfg.Log != nil {
		c.cfg.Log(ctx, msg, fields)
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
