package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/sweeper"
)

func TestSweepBoundsEachIteration(t *testing.T) {
	runner := &recordingSweepRunner{}
	if err := sweep(context.Background(), runner, slog.New(slog.NewJSONHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	remaining := time.Until(runner.deadline)
	if runner.deadline.IsZero() || remaining < sweepIterationTimeout-time.Second || remaining > sweepIterationTimeout {
		t.Fatalf("iteration deadline remaining = %v", remaining)
	}
}

func TestSweepPreservesEarlierParentDeadline(t *testing.T) {
	parentDeadline := time.Now().Add(time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), parentDeadline)
	defer cancel()
	runner := &recordingSweepRunner{}
	if err := sweep(ctx, runner, slog.New(slog.NewJSONHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	if !runner.deadline.Equal(parentDeadline) {
		t.Fatalf("iteration deadline = %v, want parent deadline %v", runner.deadline, parentDeadline)
	}
}

type recordingSweepRunner struct {
	deadline time.Time
}

func (runner *recordingSweepRunner) RunOnce(ctx context.Context) (sweeper.Result, error) {
	runner.deadline, _ = ctx.Deadline()
	return sweeper.Result{}, nil
}
