// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrdinaryGhWorkerContextUsesOriginalUserWithoutLoggingPaths(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "original-gh")
	t.Setenv("GH_CONFIG_DIR", directory)
	t.Setenv("GH_TOKEN", "foreign-ambient-token")
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	context := ordinaryExecutionTools(logger)
	env := context.Apply([]string{"HOME=private", "CODEX_HOME=private/codex"})
	if !strings.Contains(strings.Join(env, "\n"), "GH_CONFIG_DIR="+directory) || strings.Contains(strings.Join(env, "\n"), "foreign-ambient-token") {
		t.Fatal("Worker context lost its selector or inherited credentials")
	}
	if strings.Contains(logs.String(), directory) || strings.Contains(logs.String(), "foreign-ambient-token") || !strings.Contains(logs.String(), "gh_selector_available") {
		t.Fatal("diagnostics exposed user context or lost safe classification")
	}
}
