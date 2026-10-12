// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestTitleFinalizerPreservesCleanupUncertainty(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		failures []titleCleanupStage
		steps    []titleCleanupStage
	}{
		{name: "clean", steps: []titleCleanupStage{titleCloseNative, titleCloseProxy, titleReconcile, titleRemoveHome, titleSyncParent}},
		{name: "native-close", failures: []titleCleanupStage{titleCloseNative}, steps: []titleCleanupStage{titleCloseNative, titleCloseProxy, titleReconcile}},
		{name: "proxy-close", failures: []titleCleanupStage{titleCloseProxy}, steps: []titleCleanupStage{titleCloseNative, titleCloseProxy, titleReconcile}},
		{name: "process-reconcile", failures: []titleCleanupStage{titleReconcile}, steps: []titleCleanupStage{titleCloseNative, titleCloseProxy, titleReconcile}},
		{name: "all-owners", failures: []titleCleanupStage{titleCloseNative, titleCloseProxy, titleReconcile}, steps: []titleCleanupStage{titleCloseNative, titleCloseProxy, titleReconcile}},
		{name: "runtime-remove", failures: []titleCleanupStage{titleRemoveHome}, steps: []titleCleanupStage{titleCloseNative, titleCloseProxy, titleReconcile, titleRemoveHome}},
		{name: "parent-sync", failures: []titleCleanupStage{titleSyncParent}, steps: []titleCleanupStage{titleCloseNative, titleCloseProxy, titleReconcile, titleRemoveHome, titleSyncParent}},
	} {
		for _, inferenceFailed := range []bool{false, true} {
			name := scenario.name + "/successful-inference"
			if inferenceFailed {
				name = scenario.name + "/failed-inference"
			}
			t.Run(name, func(t *testing.T) {
				home := filepath.Join(t.TempDir(), "private-runtime")
				if err := os.Mkdir(home, 0700); err != nil {
					t.Fatal(err)
				}
				const privateContent = "private prompt, title, token and native failure"
				if err := os.WriteFile(filepath.Join(home, "prompt"), []byte(privateContent), 0600); err != nil {
					t.Fatal(err)
				}
				var logs bytes.Buffer
				cleanup := newTitleFinalizer(home, domain.NewID(), slog.New(slog.NewJSONHandler(&logs, nil)))
				var steps []titleCleanupStage
				step := func(stage titleCleanupStage) error {
					steps = append(steps, stage)
					if slices.Contains(scenario.failures, stage) {
						return errors.New(privateContent + " " + home)
					}
					return nil
				}
				cleanup.closeNative = func() error { return step(titleCloseNative) }
				cleanup.closeProxy = func() error { return step(titleCloseProxy) }
				cleanup.reconcile = func() error { return step(titleReconcile) }
				cleanup.remove = func(path string) error {
					if err := step(titleRemoveHome); err != nil {
						return err
					}
					return os.RemoveAll(path)
				}
				cleanup.syncParent = func(path string) error {
					if err := step(titleSyncParent); err != nil {
						return err
					}
					return security.SyncParent(path)
				}
				var inferenceErr error
				if inferenceFailed {
					inferenceErr = domain.Fail(domain.Unavailable, "The title inference failed.", "Keep the title unchanged.")
				}
				candidate := json.RawMessage(`{"title":"private title"}`)
				output, returned := func() (output json.RawMessage, returned error) {
					defer cleanup.finish(&output, &returned)
					if !inferenceFailed {
						// The success path and deferred error path share one owner.
						if err := cleanup.run(); err != nil {
							return candidate, err
						}
					}
					return candidate, inferenceErr
				}()
				if len(scenario.failures) != 0 {
					if domain.SafeError(returned).Code != domain.RecoveryRequired || output != nil {
						t.Fatal("cleanup uncertainty did not suppress the result", returned, string(output))
					}
					if strings.Contains(returned.Error(), privateContent) || strings.Contains(returned.Error(), home) {
						t.Fatal("cleanup error exposed private runtime content")
					}
				} else if returned != inferenceErr || !bytes.Equal(output, candidate) {
					t.Fatal("verified cleanup changed the original inference result", returned, string(output))
				}
				first := cleanup.run()
				if first != cleanup.run() || !slices.Equal(steps, scenario.steps) {
					t.Fatal("finalization retried or changed its cached result", steps, scenario.steps)
				}
				_, statErr := os.Stat(home)
				removed := len(scenario.failures) == 0 || scenario.failures[0] == titleSyncParent
				if removed && !errors.Is(statErr, os.ErrNotExist) || !removed && statErr != nil {
					t.Fatal("runtime removal did not preserve uncertain ownership", statErr)
				}
				if strings.Contains(logs.String(), privateContent) || strings.Contains(logs.String(), home) {
					t.Fatal("cleanup diagnostics exposed private runtime content")
				}
				for _, stage := range scenario.failures {
					if !strings.Contains(logs.String(), string(stage)) {
						t.Fatal("cleanup diagnostics omitted the failed stage", stage)
					}
				}
			})
		}
	}
}

func TestTitleFinalizerCleansFailedPrivateSetup(t *testing.T) {
	home, err := os.MkdirTemp(t.TempDir(), "title-")
	if err != nil {
		t.Fatal(err)
	}
	cleanup := newTitleFinalizer(home, domain.NewID(), nil)
	work := filepath.Join(home, "work")
	if err := os.WriteFile(work, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	var setupErr error
	output, returned := func() (output json.RawMessage, returned error) {
		defer cleanup.finish(&output, &returned)
		setupErr = security.PrivateDir(work)
		return nil, setupErr
	}()
	if setupErr == nil || returned != setupErr || output != nil {
		t.Fatal("setup failure was not preserved after cleanup", returned)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed setup retained a private runtime after verified cleanup", err)
	}
}
