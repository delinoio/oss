// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	observability "github.com/delinoio/oss/servers/tairouter/internal/observability"
	"sync"

	"github.com/delinoio/oss/packages/tairouter/store"
)

// A completed response is proof that its admission can be released. Keep retry
// work until PostgreSQL acknowledges it; elapsed time is never such proof.
// The host drains HTTP/WebSocket requests before Wait, then calls Close. A hard
// process exit requires the explicit fenced terminated-instance store workflow.
type admissionReleaseQueue struct {
	ctx       context.Context
	cancel    context.CancelFunc
	s         *store.Store
	telemetry *observability.Telemetry
	mu        sync.Mutex
	pending   int
	idle      chan struct{}
}

func newAdmissionReleaseQueue(lifetime context.Context, s *store.Store, t *observability.Telemetry) *admissionReleaseQueue {
	ctx, cancel := context.WithCancel(lifetime)
	idle := make(chan struct{})
	close(idle)
	return &admissionReleaseQueue{ctx: ctx, cancel: cancel, s: s, telemetry: t, idle: idle}
}
func (q *admissionReleaseQueue) Submit(lease store.Lease, cleanup func(context.Context)) {
	q.mu.Lock()
	if q.pending == 0 {
		q.idle = make(chan struct{})
	}
	q.pending++
	q.mu.Unlock()
	go func() {
		defer func() {
			q.mu.Lock()
			q.pending--
			if q.pending == 0 {
				close(q.idle)
			}
			q.mu.Unlock()
		}()
		if e := q.s.ReleaseUntilRecorded(q.ctx, lease); e != nil {
			q.telemetry.Event(q.ctx, "admission_release_pending_reconciliation")
			return
		}
		if cleanup != nil {
			cleanup(q.ctx)
		}
	}()
}
func (q *admissionReleaseQueue) Wait(ctx context.Context) error {
	q.mu.Lock()
	done := q.idle
	q.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (q *admissionReleaseQueue) Close() { q.cancel() }
