// Package client is a thin HTTP client for the Opalstack REST API v1.
//
// It knows nothing about Terraform. The shapes it relies on are recorded, with
// their sources, in API_FINDINGS.md at the repository root.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBaseURL = "https://my.opalstack.com/api/v1"

	defaultRequestTimeout = 30 * time.Second
	defaultMinInterval    = 350 * time.Millisecond // just under 3 requests per second
	defaultMaxAttempts    = 4
	defaultRetryBase      = time.Second
	defaultPollMin        = 2 * time.Second
	defaultPollMax        = 15 * time.Second

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

	// RequestTimeout bounds a single HTTP round trip.
	RequestTimeout time.Duration

	HTTPClient *http.Client
	Log        LogFunc

	// The remaining fields exist so tests can run without real delays.
	MinInterval time.Duration
	MaxAttempts int
	RetryBase   time.Duration
	PollMin     time.Duration
	PollMax     time.Duration
}

// Client talks to one Opalstack account.
type Client struct {
	cfg Config

	mu   sync.Mutex
	next time.Time // earliest moment the next request may start
}

// New returns a Client, filling in defaults for anything left zero.
func New(cfg Config) (*Client, error) {
	cfg.Token = strings.TrimSpace(cfg.Token)
	if cfg.Token == "" {
		return nil, fmt.Errorf("opalstack: a token is required")
	}
	cfg.BaseURL = strings.TrimRight(orDefault(cfg.BaseURL, DefaultBaseURL), "/")
	cfg.UserAgent = orDefault(cfg.UserAgent, "terraform-provider-opalstack")
	cfg.RequestTimeout = orDefault(cfg.RequestTimeout, defaultRequestTimeout)
	cfg.MinInterval = orDefault(cfg.MinInterval, defaultMinInterval)
	cfg.MaxAttempts = orDefault(cfg.MaxAttempts, defaultMaxAttempts)
	cfg.RetryBase = orDefault(cfg.RetryBase, defaultRetryBase)
	cfg.PollMin = orDefault(cfg.PollMin, defaultPollMin)
	cfg.PollMax = orDefault(cfg.PollMax, defaultPollMax)
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	return &Client{cfg: cfg}, nil
}

func orDefault[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
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
	var lastErr error
	for attempt := 1; attempt <= c.cfg.MaxAttempts; attempt++ {
		status, respBody, retryAfter, err := c.roundTrip(ctx, method, path, body)
		switch {
		case err != nil:
			lastErr = err
			// A transport error on a POST is not retried: the server may have
			// acted on it, and a second create would make a second object.
			if method != http.MethodGet || ctx.Err() != nil {
				return lastErr
			}
		case status >= 200 && status < 300:
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("opalstack: decoding the response of %s %s: %w", method, path, err)
			}
			return nil
		default:
			lastErr = newAPIError(method, path, status, respBody)
			if !retryable(method, status) {
				return lastErr
			}
		}
		if attempt == c.cfg.MaxAttempts {
			break
		}
		delay := c.cfg.RetryBase << (attempt - 1)
		if retryAfter > delay {
			delay = retryAfter
		}
		if err := sleep(ctx, delay); err != nil {
			return lastErr
		}
	}
	return lastErr
}

// retryable reports whether a failed request may be sent again. 429 means the
// request was not processed, so it is safe for any method. A gateway error says
// nothing about whether a POST was applied, so only a GET is repeated.
func retryable(method string, status int) bool {
	switch status {
	case http.StatusTooManyRequests:
		return true
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return method == http.MethodGet
	}
	return false
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

	c.log(ctx, "opalstack request", map[string]any{
		"method":   method,
		"path":     path,
		"status":   resp.StatusCode,
		"request":  redactJSON(body),
		"response": redactJSON(respBody),
	})

	var retryAfter time.Duration
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
		retryAfter = time.Duration(s) * time.Second
	}
	return resp.StatusCode, respBody, retryAfter, nil
}

// throttle spaces requests at least MinInterval apart across all goroutines.
func (c *Client) throttle(ctx context.Context) error {
	c.mu.Lock()
	now := time.Now()
	start := c.next
	if start.Before(now) {
		start = now
	}
	c.next = start.Add(c.cfg.MinInterval)
	c.mu.Unlock()
	return sleep(ctx, start.Sub(now))
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
