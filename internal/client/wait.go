package client

import (
	"context"
	"errors"
	"fmt"
)

type readiness struct {
	State string `json:"state"`
	Ready bool   `json:"ready"`
}

// waitReady polls GET /<kind>/read/<id> until ready is true. The caller bounds
// the wait with the context deadline.
//
// The API's state enum has no failure value, so a create that never completes
// can only show up here as a timeout. The error therefore names the last state.
func (c *Client) waitReady(ctx context.Context, kind, id string) error {
	last := "unknown"
	return c.poll(ctx, func() (bool, error) {
		obj, err := read[readiness](ctx, c, kind, id)
		if err != nil {
			return false, err
		}
		last = obj.State
		return obj.Ready, nil
	}, func() string {
		return fmt.Sprintf("%s %s to become ready (last state %s)", kind, id, last)
	})
}

// waitDeleted polls the same path until it answers 404, which is how the
// official Python wrapper detects that a queued deletion has finished.
func (c *Client) waitDeleted(ctx context.Context, kind, id string) error {
	last := "unknown"
	return c.poll(ctx, func() (bool, error) {
		obj, err := read[readiness](ctx, c, kind, id)
		if errors.Is(err, ErrNotFound) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		last = obj.State
		return false, nil
	}, func() string {
		return fmt.Sprintf("%s %s to be deleted (last state %s)", kind, id, last)
	})
}

// poll calls check until it reports done, backing off from PollMin to PollMax.
func (c *Client) poll(ctx context.Context, check func() (bool, error), what func() string) error {
	delay := c.cfg.PollMin
	for {
		done, err := check()
		if done {
			return nil
		}
		if err == nil {
			err = sleep(ctx, delay)
		}
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("opalstack: gave up waiting for %s: %w", what(), ctx.Err())
			}
			return err
		}
		delay = min(delay*2, c.cfg.PollMax)
	}
}
