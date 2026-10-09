// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestManualPRFixDispatchRetryBoundsAndIndependentCandidates(t *testing.T) {
	retries := prFixDispatchRetries{}
	now := time.Now()
	record := store.Record{ID: domain.NewID(), Revision: 1}
	other := store.Record{ID: domain.NewID(), Revision: 1}
	for _, want := range []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		if got := retries.failed(record, now); got != want {
			t.Fatalf("retry delay %s, want %s", got, want)
		}
		for second := time.Duration(0); second < want; second += time.Second {
			retries.expire(now.Add(second))
			if retries.ready(record, now.Add(second)) || !retries.ready(other, now.Add(second)) {
				t.Fatal("unchanged failed candidate retried early or delayed independent work")
			}
		}
		now = now.Add(want)
		if !retries.ready(record, now) {
			t.Fatal("failed candidate cannot retry after its deadline")
		}
	}
	retries.expire(now.Add(prFixDispatchRetryMaximum))
	if len(retries) != 0 {
		t.Fatal("abandoned session retained retry state indefinitely")
	}
}

func TestManualPRFixDispatchRetryPreservesBlockAndExplicitResume(t *testing.T) {
	retries := prFixDispatchRetries{}
	now := time.Now()
	record := store.Record{ID: domain.NewID(), Revision: 1}
	retries.failed(record, now)
	// The failed-dispatch transaction itself increments the revision. That
	// retained block, or an unrelated edit, must not start another provider read.
	record.Revision++
	record.Data, _ = json.Marshal(domain.Session{Dispatch: domain.DispatchBlocked, Problem: domain.Fail(domain.Unavailable, "Fixture unavailable.", "Retry later.")})
	if retries.ready(record, now) {
		t.Fatal("publishing a block reset its own retry deadline")
	}
	record.Revision++
	if retries.ready(record, now) {
		t.Fatal("editing a blocked session reset its retry deadline")
	}
	// Resume only clears scheduling delay. The ordinary dispatcher still owns
	// original input, current remote/configuration gates and atomic assignment.
	record.Revision++
	record.Data, _ = json.Marshal(domain.Session{Dispatch: domain.DispatchReady})
	if !retries.ready(record, now) || len(retries) != 0 {
		t.Fatal("explicit unpause did not release the scheduling backoff")
	}
	if delay := retries.failed(record, now); delay != prFixDispatchRetryInitial {
		t.Fatal("fresh explicit retry inherited the old exponential count")
	}
}

func assertManualPRFixDispatchBackoff(t *testing.T, f *firstDispatchFixture, id domain.ID) {
	t.Helper()
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	_, err := f.service.Store.Mutate(owner, domain.NewID(), "fixture.fix-dispatch-readiness", nil, func(tx *store.Tx) (any, error) {
		r, session, err := sessionRecord(tx, id)
		if err != nil {
			return nil, err
		}
		jr, err := tx.Get(domain.JobKind, session.Preparation.JobID)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](jr)
		if err != nil {
			return nil, err
		}
		// Simulate scheduler readiness only. No workspace or native proof is
		// fabricated: this deliberately incomplete manifest cannot dispatch
		// an execution if the unavailable provider gate is accidentally skipped.
		finished := time.Now().UTC()
		job.State, job.Output, job.FinishedAt = domain.JobSucceeded, json.RawMessage(`{}`), &finished
		if _, err := tx.PutJob(jr.ID, jr.Revision, jr.SessionID, jr.ProjectID, job); err != nil {
			return nil, err
		}
		session.Preparation.State, session.Dispatch, session.Problem = domain.PreparationReady, domain.DispatchReady, nil
		return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, session)
	})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var stall atomic.Bool
	joined := make(chan struct{})
	secondReleased := make(chan struct{})
	f.service.githubQueries = queryFunc(func(ctx context.Context, _ []byte, _, _ string, _ domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		if calls.Add(1) == 2 {
			// Callback entry is not completion. Keep the original second query
			// pending through the last explicit tick and Resume operation.
			select {
			case <-secondReleased:
			case <-ctx.Done():
				return gh.RepositoryQueryObservation{}, ctx.Err()
			}
		}
		if stall.Load() {
			defer close(joined)
			<-ctx.Done()
			return gh.RepositoryQueryObservation{}, ctx.Err()
		}
		return gh.RepositoryQueryObservation{}, domain.Fail(domain.Unavailable, "Fixture provider unavailable.", "Retry later.")
	})
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.service.runExecutionDispatchTicks(ctx, ticks) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("dispatch shutdown did not join provider reads")
		}
		if stall.Load() {
			select {
			case <-joined:
			default:
				t.Error("dispatch shutdown returned before its active provider read")
			}
		}
	}()
	start := time.Now()
	current := start
	tick := func(at time.Time) {
		t.Helper()
		select {
		case ticks <- at:
			current = at
		case <-time.After(5 * time.Second):
			t.Fatal("dispatch scan stalled")
		}
	}
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !ready() {
			if time.Now().After(deadline) {
				t.Fatal("dispatch observation did not settle")
			}
			// Reconcile asynchronous completions at the latest supplied clock.
			// Observation must not advance the automatic backoff deadline.
			tick(current)
			time.Sleep(time.Millisecond)
		}
	}
	read := func() domain.Session {
		t.Helper()
		r, err := f.service.Store.Get(owner, domain.SessionKind, id)
		if err != nil {
			t.Fatal(err)
		}
		session, err := store.Decode[domain.Session](r)
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	tick(start)
	wait(func() bool { return calls.Load() == 1 && read().Problem != nil })
	// A blocked preflight cannot occupy the ordinary lane or its queued input.
	wait(func() bool {
		session, err := store.Decode[domain.Session](f.refresh(t))
		return err == nil && session.ActiveExecutionID != ""
	})
	for second := 1; second < 30; second++ {
		tick(start.Add(time.Duration(second) * time.Second))
	}
	if calls.Load() != 1 || read().PendingInputs != 1 || read().ActiveExecutionID != "" {
		t.Fatal("unchanged preflight spent quota or consumed its original input")
	}
	tick(start.Add(31 * time.Second))
	wait(func() bool { return calls.Load() == 2 })
	// The held second query cannot complete merely because its entry was observed.
	tick(start.Add(32 * time.Second))
	tick(start.Add(33 * time.Second))
	if calls.Load() != 2 {
		t.Fatal("second failure restarted on the next one-second tick")
	}
	resume := func() {
		t.Helper()
		for _, action := range []pb.SessionAction{pb.SessionAction_SESSION_ACTION_STOP, pb.SessionAction_SESSION_ACTION_RESUME} {
			r, err := f.service.Store.Get(owner, domain.SessionKind, id)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.service.ControlSession(owner, connect.NewRequest(&pb.ControlSessionRequest{Mutation: &pb.Mutation{Id: string(id), ExpectedRevision: r.Revision, RequestId: string(domain.NewID())}, Action: action}))
			if err != nil {
				t.Fatal("explicit manual-fix control", err)
			}
		}
	}
	resume()
	tick(start.Add(34 * time.Second))
	close(secondReleased)
	wait(func() bool { return calls.Load() == 3 && read().Problem != nil })
	if read().PendingInputs != 1 || read().ActiveExecutionID != "" {
		t.Fatal("explicit retry bypassed the remote gate or consumed input")
	}
	stall.Store(true)
	resume()
	tick(start.Add(35 * time.Second))
	tick(start.Add(36 * time.Second))
	wait(func() bool { return calls.Load() == 4 })
	// Deferred cancellation must join this deliberately stalled provider read.
}
