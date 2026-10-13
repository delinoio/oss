// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"errors"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestNativeReviewTerminalProofSettlesOnlyJoinedStaleAndNoSendOutcomes(t *testing.T) {
	action := domain.NewID()
	drift := domain.Fail(domain.Conflict, "The original review target changed.", "Refresh the selected target.")
	for name, tc := range map[string]struct {
		failure                          error
		sent, completed, joined          bool
		wantKind                         nativeReviewProofKind
		wantStale, wantRejected, wantAny bool
	}{
		"drift after completed review":   {failure: drift, sent: true, completed: true, joined: true, wantKind: nativeReviewStaleProofKind, wantStale: true, wantAny: true},
		"drift with cleanup uncertain":   {failure: drift, sent: true, completed: true},
		"conflict before completion":     {failure: drift, sent: true, joined: true},
		"other failure after complete":   {failure: domain.Fail(domain.RecoveryRequired, "Cleanup is uncertain.", "Reconcile."), sent: true, completed: true, joined: true},
		"unclassified failure":           {failure: errors.New("opaque"), sent: true, completed: true, joined: true},
		"no failure":                     {sent: true, completed: true, joined: true},
		"no send and joined":             {failure: domain.NativeCodeReviewUnavailable(), joined: true, wantKind: nativeReviewRejectedProofKind, wantRejected: true, wantAny: true},
		"no send with cleanup uncertain": {failure: domain.NativeCodeReviewUnavailable()},
	} {
		t.Run(name, func(t *testing.T) {
			raw, kind, ok := nativeReviewTerminalProof(action, tc.failure, tc.sent, tc.completed, tc.joined)
			if ok != tc.wantAny || kind != tc.wantKind {
				t.Fatalf("unexpected terminal proof decision: ok=%v kind=%v", ok, kind)
			}
			if !ok {
				if len(raw) != 0 {
					t.Fatal("undecided outcome produced proof bytes")
				}
				return
			}
			var stale domain.NativeCodeReviewStaleProof
			staleOK := domain.Decode(raw, &stale) == nil && stale.Validate() == nil && stale.ActionID == action
			var rejected domain.NativeCodeReviewRejectedProof
			rejectedOK := domain.Decode(raw, &rejected) == nil && rejected.Validate() == nil && rejected.ActionID == action
			if staleOK != tc.wantStale || rejectedOK != tc.wantRejected {
				t.Fatalf("proof shape mismatch: stale=%v rejected=%v", staleOK, rejectedOK)
			}
		})
	}
}
