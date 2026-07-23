package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"
)

func TestRunFailsAtConfigurationBeforeStartingDependencies(t *testing.T) {
	t.Parallel()
	err := run(
		context.Background(),
		func(string) (string, bool) { return "", false },
		slog.New(slog.DiscardHandler),
	)
	var failure *startupError
	if !errors.As(err, &failure) {
		t.Fatalf("run() error = %T", err)
	}
	if failure.stage != stageConfiguration {
		t.Fatalf("startup stage = %q", failure.stage)
	}
	if failure.safeDetail != "config: DELIBASE_API_ORIGIN is required" {
		t.Fatalf("safe startup detail = %q", failure.safeDetail)
	}
}
