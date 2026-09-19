package core

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestWorkerRetryBackoffHasBoundedExplicitDeadlines(t *testing.T) {
	now := time.Unix(1000, 0)
	r := workerRetry{}
	if !r.ready(now) {
		t.Fatal("new work delayed")
	}
	for _, delay := range []time.Duration{1, 2, 4, 8, 16, 32, 60, 60} {
		r.fail(now)
		if r.delay != delay*time.Second || r.ready(now) || r.ready(now.Add(r.delay-time.Nanosecond)) || !r.ready(now.Add(r.delay)) {
			t.Fatalf("invalid retry deadline: %+v", r)
		}
		now = now.Add(r.delay)
	}
}

type cancelWorkerOnFailure struct {
	cancel context.CancelFunc
	output strings.Builder
}

func (w *cancelWorkerOnFailure) Write(p []byte) (int, error) {
	n, err := w.output.Write(p)
	if strings.Contains(string(p), "run.worker_failed") {
		w.cancel()
	}
	return n, err
}

func TestWorkerSchedulesBackoffWithoutReleasingPendingWork(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"echo run\"\n")
	receipt, err := s.Submit(context.Background(), repo, "HEAD", false)
	if err != nil {
		t.Fatal(err)
	}
	// A persistent write failure must remain pending rather than being
	// resubmitted or reported complete; stop through a log synchronization barrier.
	if _, err = s.Store.DB.Exec("CREATE TRIGGER reject_start BEFORE UPDATE ON runs BEGIN SELECT RAISE(ABORT, 'injected failure'); END"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &cancelWorkerOnFailure{cancel: cancel}
	s.Log = slog.New(slog.NewJSONHandler(io.Writer(w), nil))
	if err = s.Work(ctx, false, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Count(w.output.String(), "run.worker_failed") != 1 || !strings.Contains(w.output.String(), `"retry_after_ms":1000`) {
		t.Fatal(w.output.String())
	}
	r, err := s.Store.Run(receipt.RunID)
	if err != nil || r.State != Queued || r.Checks[0].State != Queued {
		t.Fatalf("pending work changed: %+v %v", r, err)
	}
}
