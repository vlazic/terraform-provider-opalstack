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
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBaseURL        = "https://my.opalstack.com/api/v1"
	DefaultRequestTimeout = 30 * time.Second

	maxAttempts = 4

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
	cfg.UserAgent = cmp.Or(cfg.UserAgent, "terraform-provider-opalstack")
	cfg.RequestTimeout = cmp.Or(cfg.RequestTimeout, DefaultRequestTimeout)
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	return &Client{
		cfg:         cfg,
		minInterval: 350 * time.Millisecond, // just under 3 requests per second
		retryBase:   time.Second,
		pollMin:     2 * time.Second,
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
			err = newAPIError(method, path, status, respBody)
		}
		if attempt == maxAttempts || !retryable(method, status) || ctx.Err() != nil {
			return err
		}
		if sleep(ctx, max(c.retryBase<<(attempt-1), retryAfter)) != nil {
			return err
		}
	}
}

// retryable is the whole retry policy. status is 0 for a transport error.
//
// A GET may always be sent again. A POST may be repeated only on 429, which
// means the request was not processed: after a gateway or transport error the
// server may have acted on it, and a second create would make a second object.
func retryable(method string, status int) bool {
	switch status {
	case http.StatusTooManyRequests:
		return true
	case 0, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
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

// throttle spaces requests at least minInterval apart across all goroutines.
func (c *Client) throttle(ctx context.Context) error {
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
