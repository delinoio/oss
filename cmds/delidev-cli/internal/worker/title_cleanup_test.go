// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestTitleCleanupInferenceAndRemovalFailureRequiresRecovery(t *testing.T) {
	inference := domain.Fail(domain.Unavailable, "Inference failed.", "Keep the placeholder.")
	cleanup := titleRuntimeCleanup{removeRuntime: func() error { return errors.New("private runtime path") }}
	output, err := cleanup.finish(json.RawMessage(`{"title":"private title"}`), inference)
	if output != nil || domain.SafeError(err).Code != domain.RecoveryRequired || strings.Contains(err.Error(), "private runtime path") {
		t.Fatalf("cleanup did not retain safe recovery outcome: output=%s err=%v", output, err)
	}
}

func TestTitleCleanupParentSyncFailureRequiresRecovery(t *testing.T) {
	removed := false
	cleanup := titleRuntimeCleanup{
		removeRuntime: func() error { removed = true; return nil },
		syncParent: func() error {
			if !removed {
				t.Fatal("parent synchronized before runtime removal")
			}
			return errors.New("private parent path")
		},
	}
	output, err := cleanup.finish(json.RawMessage(`{"title":"private title"}`), domain.Fail(domain.InvalidArgument, "Invalid title.", "Keep the placeholder."))
	if output != nil || domain.SafeError(err).Code != domain.RecoveryRequired || strings.Contains(err.Error(), "private parent path") {
		t.Fatalf("unsynchronized removal lost recovery: output=%s err=%v", output, err)
	}
}

func TestTitleCleanupJoinedNativeFailureRequiresRecovery(t *testing.T) {
	for _, failure := range []string{"native-close", "proxy-close", "owner-reconciliation"} {
		t.Run(failure, func(t *testing.T) {
			var joined, reports []string
			action := func(stage string) func() error {
				return func() error {
					joined = append(joined, stage)
					if stage == failure {
						return errors.New("private native diagnostic")
					}
					return nil
				}
			}
			cleanup := titleRuntimeCleanup{
				closeNative: action("native-close"), closeProxy: action("proxy-close"), reconcileOwner: action("owner-reconciliation"),
				removeRuntime: func() error { t.Fatal("removed unjoined runtime evidence"); return nil },
				reportFailure: func(stage string) { reports = append(reports, stage) },
			}
			output, err := cleanup.finish(json.RawMessage(`{"title":"private title"}`), domain.Fail(domain.Unavailable, "Inference failed.", "Keep the placeholder."))
			if output != nil || domain.SafeError(err).Code != domain.RecoveryRequired || len(joined) != 3 || len(reports) != 1 || reports[0] != failure {
				t.Fatalf("original cleanup owners lost: joined=%v reports=%v output=%s err=%v", joined, reports, output, err)
			}
		})
	}
}

func TestTitleCleanupVerifiedRemovalPreservesInferenceFailure(t *testing.T) {
	inference := domain.Fail(domain.Unavailable, "Inference failed.", "Keep the placeholder.")
	var order []string
	cleanup := titleRuntimeCleanup{
		removeRuntime: func() error { order = append(order, "remove"); return nil },
		syncParent:    func() error { order = append(order, "sync"); return nil },
	}
	output, err := cleanup.finish(nil, inference)
	if output != nil || err != inference || strings.Join(order, ",") != "remove,sync" {
		t.Fatalf("verified cleanup altered inference error: order=%v output=%s err=%v", order, output, err)
	}
}

func TestTitleCleanupSuccessPreservesResult(t *testing.T) {
	calls := 0
	cleanup := titleRuntimeCleanup{
		removeRuntime: func() error { calls++; return nil },
		syncParent:    func() error { calls++; return nil },
	}
	if err := cleanup.run(); err != nil {
		t.Fatal(err)
	}
	result := json.RawMessage(`{"title":"fixture title","cleanup_verified":true}`)
	output, err := cleanup.finish(result, nil)
	if string(output) != string(result) || err != nil || calls != 2 {
		t.Fatalf("deferred cleanup changed verified result or repeated removal: calls=%d output=%s err=%v", calls, output, err)
	}
}

func TestTitleCleanupDeferredFinishCannotEraseRemovalUncertainty(t *testing.T) {
	calls := 0
	cleanup := titleRuntimeCleanup{removeRuntime: func() error { calls++; return errors.New("private path") }}
	original := cleanup.run()
	_, err := cleanup.finish(nil, domain.Fail(domain.Unavailable, "Inference failed.", "Keep the placeholder."))
	if err != original || calls != 1 || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("deferred cleanup replaced original uncertainty: calls=%d err=%v", calls, err)
	}
}
