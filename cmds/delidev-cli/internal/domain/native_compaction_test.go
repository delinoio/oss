// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestNativeCompactionRequiresOriginalOrderedContextItem(t *testing.T) {
	v := NativeCompactionObservation{Harness: Codex, Trigger: NativeAutomaticCompaction, Stage: NativeCompactionStarted, NativeItemID: "original-context-item"}
	state, err := ApplyNativeCompaction(nil, v)
	if err != nil || state.Closed() {
		t.Fatal("started context granted settlement", err)
	}
	v.Stage = NativeCompactionCompleted
	done, err := ApplyNativeCompaction(state, v)
	if err != nil || !done.Closed() || state.Closed() {
		t.Fatal("completion lost ordering or mutated the predecessor", err)
	}
	if _, err = ApplyNativeCompaction(nil, v); err == nil {
		t.Fatal("foreign completion accepted")
	}
	if _, err = ApplyNativeCompaction(done, v); err == nil {
		t.Fatal("completed native item was reused")
	}
	for _, change := range []func(*NativeCompactionObservation){func(v *NativeCompactionObservation) { v.Harness = ClaudeCode }, func(v *NativeCompactionObservation) { v.Trigger = NativeManualCompaction }, func(v *NativeCompactionObservation) { v.Stage = "idle" }, func(v *NativeCompactionObservation) { v.NativeItemID = "" }} {
		invalid := v
		change(&invalid)
		if invalid.Validate() == nil {
			t.Fatal("unknown context profile accepted")
		}
	}
}

func TestNativeCompactionProgressCannotBorrowAnotherPayload(t *testing.T) {
	v := &NativeCompactionObservation{Harness: Codex, Trigger: NativeAutomaticCompaction, Stage: NativeCompactionStarted, NativeItemID: "original-context-item"}
	update := ExecutionProgressUpdate{ID: NewID(), Progress: NativeProgress{Kind: NativeCompactionProgress, Compaction: v}}
	if update.Validate() != nil {
		t.Fatal("valid context metadata rejected")
	}
	text := "unrelated"
	update.Progress.Diff = &text
	if update.Validate() == nil {
		t.Fatal("context metadata acquired diff content")
	}
	update.Progress.Kind = DiffProgress
	if update.Validate() == nil {
		t.Fatal("diff acquired hidden compaction metadata")
	}
}
