package client

import (
	"context"
	"fmt"
	"strings"
)

// Failed reports a state the API does not leave by itself, such as
// INSTALL_FAILED for an app whose installer did not succeed.
func (s Status) Failed() bool { return strings.HasSuffix(s.State, "_FAILED") }

// pending reports that the API is still working on the object.
func (s Status) pending() bool { return strings.HasPrefix(s.State, "PENDING_") }

// Status is the pair every managed object reports. The response types embed it.
type Status struct {
	State string `json:"state"`
	Ready bool   `json:"ready"`
}

func (s Status) status() Status { return s }

type statused interface{ status() Status }

// poll calls check until it reports done, sleeping first and backing off from
// pollMin to pollMax. It sleeps first because it always follows a POST whose
// answer already said the object was pending. The caller bounds the wait with
// the context deadline.
//
// A create that neither completes nor reaches a failed state can only show up
// here as a timeout. The error therefore names the last state that check
// reported.
func (c *Client) poll(ctx context.Context, what string, check func() (done bool, state string, err error)) error {
	last := "unknown"
	for delay := c.pollMin; ; delay = min(delay*2, c.pollMax) {
		if err := sleep(ctx, delay); err != nil {
			return fmt.Errorf("opalstack: gave up waiting for %s (last state %s): %w", what, last, err)
		}
		done, state, err := check()
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("opalstack: gave up waiting for %s (last state %s): %w", what, last, ctx.Err())
			}
			return err
		}
		if done {
			return nil
		}
		last = state
	}
}
