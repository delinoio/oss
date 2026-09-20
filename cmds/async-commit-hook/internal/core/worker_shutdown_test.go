package core

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerStorageFailureJoinsMoreRunsThanCompletionBuffer(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	run, err := s.Plan(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	// Empty check graphs keep this a scheduler test, without launching thousands
	// of OS shells. Every accepted run still comes from the real SQLite queue.
	const count = 2050
	for range count {
		run.ID = ID()
		if _, err := s.Store.InsertRun(&run, ""); err != nil {
			t.Fatal(err)
		}
	}
	started := make(chan string, count)
	var reaped atomic.Int64
	returned := make(chan error, 1)
	go func() {
		returned <- s.work(context.Background(), false, "", func(ctx context.Context, id string) error {
			started <- id
			<-ctx.Done()
			reaped.Add(1)
			return ctx.Err()
		})
	}()
	watchdog := time.NewTimer(30 * time.Second)
	defer watchdog.Stop()
	seen := map[string]bool{}
	for range count {
		select {
		case id := <-started:
			if seen[id] {
				t.Fatal("run launched more than once")
			}
			seen[id] = true
		case <-watchdog.C:
			t.Fatal("worker did not launch all accepted runs")
		}
	}
	// No run completes until Work sees this storage failure and cancels its
	// ownership context. Its receiver is then gone when all runs finish together.
	if err := s.Store.DB.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-returned:
		if err == nil {
			t.Fatal("worker lost the storage failure")
		}
		if reaped.Load() != count {
			t.Fatalf("worker returned before joining runs: %d", reaped.Load())
		}
	case <-watchdog.C:
		t.Fatal("worker deadlocked while joining completed runs")
	}
}
