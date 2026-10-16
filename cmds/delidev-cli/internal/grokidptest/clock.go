// SPDX-License-Identifier: Apache-2.0
package grokidptest

import (
	"context"
	"sync"
	"time"
)

// Clock is a virtual clock for the device poll loop. Wait advances virtual time
// instead of sleeping, so a fifteen-minute device login runs instantly while the
// recorded waits still prove the request spacing.
type Clock struct {
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
	// Gate runs at the start of every Wait with the wait number (from 1). It may
	// block, for example to hold the loop between two polls; it must return ctx's
	// error when the context ends.
	Gate func(ctx context.Context, wait int, d time.Duration) error
}

// NewClock returns a clock that starts at start.
func NewClock(start time.Time) *Clock { return &Clock{now: start} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Wait records d, runs the gate and advances virtual time by d.
func (c *Clock) Wait(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.waits = append(c.waits, d)
	number, gate := len(c.waits), c.Gate
	c.mu.Unlock()
	if gate != nil {
		if err := gate(ctx, number, d); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return nil
}

// Waits returns a copy of every requested wait.
func (c *Clock) Waits() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}
