// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRevertPreSendFailureIsClosedAndCannotGrantCheckpointAuthority(t *testing.T) {
	original := SessionRevertPreSendFailure{Version: 1, Outcome: RevertFailedBeforeClaim, JobID: NewID(), ActionID: NewID(), ExecutionID: NewID(), InputDigest: strings.Repeat("a", 64), NativeThreadID: NativeIdentity(NewID()), ClaimNotInvoked: true, NativeSendNotStarted: true, CleanupVerified: true, FailureCode: Conflict}
	if original.Validate() != nil {
		t.Fatal("closed original failure rejected")
	}
	for _, change := range []func(*SessionRevertPreSendFailure){func(r *SessionRevertPreSendFailure) { r.Version = 4 }, func(r *SessionRevertPreSendFailure) { r.Outcome = "unknown" }, func(r *SessionRevertPreSendFailure) { r.JobID = r.ActionID }, func(r *SessionRevertPreSendFailure) { r.InputDigest = "unknown" }, func(r *SessionRevertPreSendFailure) { r.NativeThreadID = "" }, func(r *SessionRevertPreSendFailure) { r.ClaimNotInvoked = false }, func(r *SessionRevertPreSendFailure) { r.NativeSendNotStarted = false }, func(r *SessionRevertPreSendFailure) { r.CleanupVerified = false }, func(r *SessionRevertPreSendFailure) { r.FailureCode = "unknown" }} {
		changed := original
		change(&changed)
		if changed.Validate() == nil {
			t.Fatal("open/missing failure proof accepted")
		}
	}
	raw, _ := json.Marshal(original)
	var success SessionCompactionResult
	if Decode(raw, &success) == nil {
		t.Fatal("pre-send failure decoded as successor checkpoint")
	}
	var failure SessionRevertPreSendFailure
	withExtra := append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"checkpoint":{}}`)...)
	if Decode(withExtra, &failure) == nil {
		t.Fatal("failure acquired checkpoint authority")
	}
}
