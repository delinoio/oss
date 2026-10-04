// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
)

func TestCodexCompactionResultKeepsIndependentClosedProfile(t *testing.T) {
	action, execution := NewID(), NewID()
	proof := CodexCompactionResult{NativeThreadID: NativeIdentity(NewID()), SourceNativeTurnID: NativeIdentity(NewID()), NativeTurnID: NativeIdentity(NewID()), LiveItemID: "original-live-item", HistoryItemID: "item-2", HistoryDigest: strings.Repeat("0", 64), Actions: 2, Acknowledged: true, LifecycleCompleted: true, ResponseUsages: []NativeResponseUsage{}}
	result := SessionCompactionResult{Version: 2, Harness: Codex, ActionID: action, ExecutionID: execution, Outcome: CompactionSucceeded, Codex: &proof, CleanupVerified: true, Checkpoint: SessionCompactionRef{JobID: NewID(), ActionID: action, ExecutionID: execution, CheckpointDigest: strings.Repeat("1", 64), NativeDigest: strings.Repeat("2", 64)}}
	if result.Validate() != nil {
		t.Fatal("valid bounded Codex profile rejected")
	}
	for _, change := range []func(*SessionCompactionResult){
		func(r *SessionCompactionResult) { r.Version = 1 }, func(r *SessionCompactionResult) { r.Harness = OpenCode }, func(r *SessionCompactionResult) { r.OuterKind = ClaudeResultSuccess }, func(r *SessionCompactionResult) { r.Outcome = CompactionFailed }, func(r *SessionCompactionResult) { r.CleanupVerified = false }, func(r *SessionCompactionResult) { r.Checkpoint.RequiresResume = true }, func(r *SessionCompactionResult) { r.Codex.Acknowledged = false }, func(r *SessionCompactionResult) { r.Codex.LifecycleCompleted = false }, func(r *SessionCompactionResult) { r.Codex.NativeTurnID = r.Codex.SourceNativeTurnID }, func(r *SessionCompactionResult) { r.Codex.ResponseUsages = nil }, func(r *SessionCompactionResult) { r.Codex.HistoryDigest = "unverified" },
	} {
		copy := result
		native := proof
		copy.Codex = &native
		change(&copy)
		if copy.Validate() == nil {
			t.Fatal("incomplete, mixed or unsupported native result accepted")
		}
	}
}

func TestOpenCodeCompactionResultKeepsOriginalClosedProfile(t *testing.T) {
	action, execution := NewID(), NewID()
	proof := OpenCodeCompactionResult{NativeSessionID: "ses_01960dcbe1faabcdefghijklmn", SourceNativeInputID: "msg_01960dcbe1faABCDEFGHIJKLMN", UserID: "msg_01960dcbe1fbABCDEFGHIJKLMN", PartID: "prt_01960dcbe1fbABCDEFGHIJKLMN", SummaryID: "msg_01960dcbe1fcABCDEFGHIJKLMN", CompletedEventID: "evt_01960dcbe1fcABCDEFGHIJKLMN", HistoryDigest: strings.Repeat("a", 64), Actions: 1, Acknowledged: true, LifecycleCompleted: true, Usages: []OpenCodeUsageObservation{}}
	result := SessionCompactionResult{Version: 3, Harness: OpenCode, ActionID: action, ExecutionID: execution, Outcome: CompactionSucceeded, OpenCode: &proof, CleanupVerified: true, Checkpoint: SessionCompactionRef{JobID: NewID(), ActionID: action, ExecutionID: execution, CheckpointDigest: strings.Repeat("b", 64), NativeDigest: strings.Repeat("c", 64)}}
	if result.Validate() != nil {
		t.Fatal("original OpenCode result refused")
	}
	for _, change := range []func(*SessionCompactionResult){
		func(r *SessionCompactionResult) { r.Version = 2 }, func(r *SessionCompactionResult) { r.Harness = Codex }, func(r *SessionCompactionResult) { r.Codex = &CodexCompactionResult{} }, func(r *SessionCompactionResult) { r.OuterKind = ClaudeResultSuccess }, func(r *SessionCompactionResult) { r.Outcome = CompactionFailed }, func(r *SessionCompactionResult) { r.CleanupVerified = false }, func(r *SessionCompactionResult) { r.Checkpoint.RequiresResume = true }, func(r *SessionCompactionResult) { r.OpenCode.Acknowledged = false }, func(r *SessionCompactionResult) { r.OpenCode.LifecycleCompleted = false }, func(r *SessionCompactionResult) { r.OpenCode.SourceNativeInputID = r.OpenCode.UserID }, func(r *SessionCompactionResult) { r.OpenCode.SummaryID = r.OpenCode.UserID }, func(r *SessionCompactionResult) { r.OpenCode.CompletedEventID = r.OpenCode.PartID }, func(r *SessionCompactionResult) { r.OpenCode.Usages = nil }, func(r *SessionCompactionResult) { r.OpenCode.HistoryDigest = "unknown" },
	} {
		copy := result
		native := proof
		copy.OpenCode = &native
		change(&copy)
		if copy.Validate() == nil {
			t.Fatal("foreign/incomplete/mixed profile accepted")
		}
	}
}
