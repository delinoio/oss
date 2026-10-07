// SPDX-License-Identifier: Apache-2.0
package domain

// This projection contains original native identities and bounded accounting
// observations. Native summary text and private runtime paths stay on the Worker.
type OpenCodeCompactionResult struct {
	NativeSessionID     NativeIdentity             `json:"native_session_id"`
	SourceNativeInputID NativeIdentity             `json:"source_native_input_id"`
	UserID              NativeIdentity             `json:"user_id"`
	PartID              NativeIdentity             `json:"part_id"`
	SummaryID           NativeIdentity             `json:"summary_id"`
	CompletedEventID    NativeIdentity             `json:"completed_event_id"`
	HistoryDigest       string                     `json:"history_digest"`
	Actions             uint32                     `json:"actions"`
	Acknowledged        bool                       `json:"acknowledged"`
	LifecycleCompleted  bool                       `json:"lifecycle_completed"`
	Usages              []OpenCodeUsageObservation `json:"usages"`
}

func (r SessionCompactionResult) validateOpenCode() error {
	p := r.OpenCode
	if r.Harness != OpenCode || p == nil || r.Codex != nil || r.Outcome != CompactionSucceeded || r.OuterKind != "" || r.OuterError || r.CommandEchoID != "" || r.ResultID != "" || r.CommandCompletedID != "" || r.IdleID != "" || r.BoundaryID != "" || r.SummaryID != "" || r.Boundary != nil || r.Summary != nil || r.Checkpoint.Validate() != nil || r.Checkpoint.ActionID != r.ActionID || r.Checkpoint.ExecutionID != r.ExecutionID || r.Checkpoint.RequiresResume {
		return CompactionUncertain()
	}
	if p.NativeSessionID.Validate(OpenCode, NativeThreadIdentity) != nil || p.SourceNativeInputID.Validate(OpenCode, NativeTurnIdentity) != nil || p.UserID.Validate(OpenCode, NativeMessageIdentity) != nil || p.PartID.Validate(OpenCode, NativePartIdentity) != nil || p.SummaryID.Validate(OpenCode, NativeMessageIdentity) != nil || p.CompletedEventID.Validate(OpenCode, NativeEventIdentity) != nil || p.UserID == p.SourceNativeInputID || p.SummaryID == p.UserID || p.SummaryID == p.SourceNativeInputID || !validCompactionDigest(p.HistoryDigest) || p.Actions == 0 || p.Actions > 128 || !p.Acknowledged || !p.LifecycleCompleted || p.Usages == nil || len(p.Usages) > 128 {
		return CompactionUncertain()
	}
	seen := map[string]bool{}
	for _, usage := range p.Usages {
		if usage.Validate() != nil || usage.Source != OpenCodeStepUsage || usage.NativeParentID != string(p.SummaryID) || seen[usage.NativeID] || usage.NativeID == string(p.PartID) {
			return CompactionUncertain()
		}
		seen[usage.NativeID] = true
	}
	return nil
}
