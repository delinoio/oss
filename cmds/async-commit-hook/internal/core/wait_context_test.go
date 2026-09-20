package core

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func requireWaitExpired(t *testing.T, err error) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "wait-expired" || typed.Exit != 4 {
		t.Fatalf("expected wait expiration: %v", err)
	}
}

func TestExpiredWaitContextTakesPrecedenceOverEveryRunState(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	receipt, err := s.Submit(context.Background(), repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Store.Run(receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []State{Queued, Preparing, Running, Collecting, Passed, Failed, Blocked, Cancelled, Replaced, Interrupted, Skipped, Expired} {
		t.Run(string(state), func(t *testing.T) {
			r.State = state
			if err := s.Store.SaveRun(r); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 0)
			defer cancel()
			_, err := s.Wait(ctx, r.ID)
			requireWaitExpired(t, err)
			stored, err := s.Store.Run(r.ID)
			if err != nil || stored.State != state || stored.AcknowledgedAt != nil {
				t.Fatal("wait changed execution or acknowledgement", stored.State, err)
			}
			if state.Terminal() {
				if result, err := s.Wait(context.Background(), r.ID); err != nil || result.State != state {
					t.Fatal("live query did not return completed run", err)
				}
			}
		})
	}
	if err := s.Store.DB.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.Wait(ctx, r.ID)
	requireWaitExpired(t, err) // The expired query must not even access the closed store.
}

func TestWaitCancellationDuringReadCannotReturnTerminalSuccess(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	r := runFixture(t, s, repo)
	conn, err := s.Store.DB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	before := s.Store.DB.Stats().WaitCount
	done := make(chan error, 1)
	go func() { _, err := s.Wait(ctx, r.ID); done <- err }()
	watchdog := time.After(10 * time.Second)
	// With the sole connection held, WaitCount proves the query passed the
	// initial context check and is waiting for its read, without timing guesses.
	for s.Store.DB.Stats().WaitCount == before {
		select {
		case <-watchdog:
			t.Fatal("query did not reach the connection barrier")
		default:
			runtime.Gosched()
		}
	}
	cancel()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		requireWaitExpired(t, err)
	case <-watchdog:
		t.Fatal("query did not leave the connection barrier")
	}
}
