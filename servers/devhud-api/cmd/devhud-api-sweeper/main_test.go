package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/adminassets"
	"github.com/delinoio/oss/servers/devhud-api/internal/sweeper"
)

func TestValidateEmbeddedAdministratorAssets(t *testing.T) {
	if err := validateEmbeddedAdministratorAssets(adminassets.Embedded()); err != nil {
		t.Fatal(err)
	}
	for name, assets := range map[string]fstest.MapFS{
		"missing": {},
		"empty":   {"dist/index.html": &fstest.MapFile{}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateEmbeddedAdministratorAssets(assets); err == nil {
				t.Fatal("asset validation succeeded")
			}
		})
	}
}

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

func TestStartupContextBoundsDatabaseInitialization(t *testing.T) {
	ctx, cancel := newStartupContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	remaining := time.Until(deadline)
	if !ok || remaining < sweepStartupTimeout-time.Second || remaining > sweepStartupTimeout {
		t.Fatalf("startup deadline remaining = %v", remaining)
	}
}

func TestStartupContextPreservesEarlierParentDeadline(t *testing.T) {
	parentDeadline := time.Now().Add(time.Second)
	parent, cancelParent := context.WithDeadline(context.Background(), parentDeadline)
	defer cancelParent()
	ctx, cancel := newStartupContext(parent)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.Equal(parentDeadline) {
		t.Fatalf("startup deadline = %v, want parent deadline %v", deadline, parentDeadline)
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

func TestSweepLogsCrashReportPruningCount(t *testing.T) {
	var logs bytes.Buffer
	runner := &recordingSweepRunner{result: sweeper.Result{CrashReportsDeleted: 7}}
	if err := sweep(context.Background(), runner, slog.New(slog.NewJSONHandler(&logs, nil))); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), `"crash_reports_deleted":7`) {
		t.Fatalf("crash-report pruning count missing from completion log: %s", logs.String())
	}
}

type recordingSweepRunner struct {
	deadline time.Time
	result   sweeper.Result
}

func (runner *recordingSweepRunner) RunOnce(ctx context.Context) (sweeper.Result, error) {
	runner.deadline, _ = ctx.Deadline()
	return runner.result, nil
}
