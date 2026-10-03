// SPDX-License-Identifier: Apache-2.0
package domain

import "slices"

type NativeCompactionTrigger string
type NativeCompactionStage string

const (
	NativeAutomaticCompaction NativeCompactionTrigger = "automatic"
	NativeManualCompaction    NativeCompactionTrigger = "manual"
	NativeCompactionStarted   NativeCompactionStage   = "started"
	NativeCompactionCompleted NativeCompactionStage   = "completed"
)

// Native compaction progress is context metadata, not input acceptance, billed
// usage, a canonical assistant message or a successor checkpoint.
type NativeCompactionObservation struct {
	Harness      Harness                 `json:"harness"`
	Trigger      NativeCompactionTrigger `json:"trigger"`
	Stage        NativeCompactionStage   `json:"stage"`
	NativeItemID string                  `json:"native_item_id"`
}

func (v NativeCompactionObservation) Validate() error {
	if (v.Harness != Codex && v.Harness != OpenCode) || v.Trigger != NativeAutomaticCompaction || !slices.Contains([]NativeCompactionStage{NativeCompactionStarted, NativeCompactionCompleted}, v.Stage) || Text(v.NativeItemID, "native compaction item", 1024, true) != nil {
		return CompactionUncertain()
	}
	if v.Harness == OpenCode && NativeIdentity(v.NativeItemID).Validate(OpenCode, NativePartIdentity) != nil {
		return CompactionUncertain()
	}
	return nil
}

type NativeCompactionState map[string]NativeCompactionStage

func (s NativeCompactionState) Closed() bool {
	for _, stage := range s {
		if stage != NativeCompactionCompleted {
			return false
		}
	}
	return true
}

func ApplyNativeCompaction(prior NativeCompactionState, v NativeCompactionObservation) (NativeCompactionState, error) {
	if v.Validate() != nil || len(prior) > MaxExecutionEvents {
		return nil, CompactionUncertain()
	}
	next := NativeCompactionState{}
	for item, stage := range prior {
		if Text(item, "native compaction item", 1024, true) != nil || !slices.Contains([]NativeCompactionStage{NativeCompactionStarted, NativeCompactionCompleted}, stage) {
			return nil, CompactionUncertain()
		}
		next[item] = stage
	}
	stage, exists := next[v.NativeItemID]
	if v.Stage == NativeCompactionStarted && exists || v.Stage == NativeCompactionCompleted && (!exists || stage != NativeCompactionStarted) || !exists && len(next) >= MaxExecutionEvents {
		return nil, CompactionUncertain()
	}
	next[v.NativeItemID] = v.Stage
	return next, nil
}
