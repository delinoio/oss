// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestTitleCleanupUncertaintyOverridesInferenceErrors(t *testing.T) {
	for _, failedStage := range []titleCleanupStage{titleCleanupClient, titleCleanupProxy, titleCleanupOwner, titleCleanupRemove, titleCleanupSync} {
		for _, inferenceFailed := range []bool{false, true} {
			name := string(failedStage)
			if inferenceFailed {
				name += "/inference-failed"
			} else {
				name += "/inference-succeeded"
			}
			t.Run(name, func(t *testing.T) {
				var calls []titleCleanupStage
				var logs bytes.Buffer
				operation := func(stage titleCleanupStage) func() error {
					return func() error {
						calls = append(calls, stage)
						if stage == failedStage {
							return errors.New("private prompt and runtime path must not enter logs")
						}
						return nil
					}
				}
				cleanup := titleRuntimeCleanup{
					closeClient: operation(titleCleanupClient), closeProxy: operation(titleCleanupProxy),
					reconcileOwner: operation(titleCleanupOwner), removeHome: operation(titleCleanupRemove),
					syncParent: operation(titleCleanupSync), logger: slog.New(slog.NewJSONHandler(&logs, nil)), jobID: domain.NewID(),
				}
				output := json.RawMessage(`{"title":"valid fixture title","cleanup_verified":true}`)
				var returned error
				if inferenceFailed {
					returned = domain.Fail(domain.InvalidArgument, "Invalid inference output.", "")
				}
				func() { defer cleanup.finish(&output, &returned) }()
				if output != nil || domain.SafeError(returned).Code != domain.RecoveryRequired {
					t.Fatalf("cleanup uncertainty did not suppress terminal result: output=%s error=%v", output, returned)
				}
				wantCalls := []titleCleanupStage{titleCleanupClient, titleCleanupProxy, titleCleanupOwner}
				if failedStage == titleCleanupRemove || failedStage == titleCleanupSync {
					wantCalls = append(wantCalls, titleCleanupRemove)
					if failedStage == titleCleanupSync {
						wantCalls = append(wantCalls, titleCleanupSync)
					}
				}
				if len(calls) != len(wantCalls) {
					t.Fatalf("cleanup erased uncertain process intent or synchronized failed removal: calls=%v", calls)
				}
				for i := range wantCalls {
					if calls[i] != wantCalls[i] {
						t.Fatalf("cleanup order changed: got=%v want=%v", calls, wantCalls)
					}
				}
				if !strings.Contains(logs.String(), string(failedStage)) || strings.Contains(logs.String(), "private prompt") {
					t.Fatalf("cleanup logging lost the safe stage or exposed private error content: %s", logs.String())
				}
				before := len(calls)
				cleanup.finish(&output, &returned)
				if len(calls) != before {
					t.Fatal("cleanup uncertainty triggered a second cleanup attempt")
				}
			})
		}
	}
}

func TestTitleCleanupPreservesResultOnlyAfterAllCleanupSteps(t *testing.T) {
	for _, inferenceFailed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "inference-error"}[inferenceFailed], func(t *testing.T) {
			var calls []titleCleanupStage
			operation := func(stage titleCleanupStage) func() error {
				return func() error { calls = append(calls, stage); return nil }
			}
			cleanup := titleRuntimeCleanup{
				closeClient: operation(titleCleanupClient), closeProxy: operation(titleCleanupProxy),
				reconcileOwner: operation(titleCleanupOwner), removeHome: operation(titleCleanupRemove), syncParent: operation(titleCleanupSync),
			}
			output := json.RawMessage(`{"title":"valid fixture title"}`)
			originalOutput := string(output)
			var returned error
			if inferenceFailed {
				returned = domain.Fail(domain.InvalidArgument, "Invalid inference output.", "")
				output = nil
				originalOutput = ""
			}
			originalError := returned
			cleanup.finish(&output, &returned)
			if string(output) != originalOutput || returned != originalError || len(calls) != 5 {
				t.Fatalf("verified cleanup changed the original result or skipped a step: calls=%v output=%s error=%v", calls, output, returned)
			}
			cleanup.finish(&output, &returned)
			if len(calls) != 5 {
				t.Fatal("successful explicit cleanup ran again during deferred cleanup")
			}
		})
	}
}
