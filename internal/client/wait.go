package client

import (
	"context"
	"fmt"
)

const statePendingDelete = "PENDING_DELETE"

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
// The API's state enum has no failure value, so a create that never completes
// can only show up here as a timeout. The error therefore names the last state
// that check reported.
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
