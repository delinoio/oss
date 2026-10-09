// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ForkOrigin is a retained boundary, never an executable copy of source input.
// Snapshot remains immutable even before the child's first explicit input.
type ForkOrigin struct {
	OpenCodeCreationProof     *OpenCodeForkCreationProof `json:"opencode_creation_proof,omitempty"`
	OpenCodeCreationRequestID ID                         `json:"opencode_creation_request_id,omitempty"`
	Startup                   *ExecutionStartupSelection `json:"startup,omitempty"`
	SidechatParentSnapshot    *InitialExecution          `json:"sidechat_parent_snapshot,omitempty"`
	SourceSessionID           ID                         `json:"source_session_id"`
	SourceRevision            uint64                     `json:"source_revision"`
	SourceExecutionID         ID                         `json:"source_execution_id"`
	SourceTurnID              NativeIdentity             `json:"source_turn_id"`
	JobID                     ID                         `json:"job_id"`
	RuntimeID                 ID                         `json:"runtime_id"`
	NativeThreadID            NativeIdentity             `json:"native_thread_id"`
	NativeTurnID              NativeIdentity             `json:"native_turn_id,omitempty"`
	CheckpointDigest          string                     `json:"checkpoint_digest"`
	Snapshot                  InitialExecution           `json:"snapshot"`
	WorkerDeviceID            ID                         `json:"worker_device_id"`
	JobInputDigest            string                     `json:"job_input_digest"`
}

// OpenCodeForkCreationProof binds the verified publication without retaining
// executable source input, prompts or protected native content.
type OpenCodeForkCreationProof struct {
	CreationRequestID ID     `json:"creation_request_id"`
	ChildSessionID    ID     `json:"child_session_id"`
	JobOutputDigest   string `json:"job_output_digest"`
	OriginDigest      string `json:"origin_digest"`
}

func (f ForkOrigin) OpenCodeCreationDigest(child ID, outputDigest string) string {
	f.OpenCodeCreationProof = nil
	raw, err := json.Marshal(struct {
		ChildSessionID  ID         `json:"child_session_id"`
		JobOutputDigest string     `json:"job_output_digest"`
		Origin          ForkOrigin `json:"origin"`
	}{child, outputDigest, f})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (f ForkOrigin) VerifyOpenCodeCreation(child ID) bool {
	p := f.OpenCodeCreationProof
	return p != nil && p.CreationRequestID == f.OpenCodeCreationRequestID && p.ChildSessionID == child && canonicalDigest(p.JobOutputDigest) && canonicalDigest(p.OriginDigest) && p.OriginDigest == f.OpenCodeCreationDigest(child, p.JobOutputDigest)
}

type ForkJobInput struct {
	SubscriptionGeneration ID                         `json:"subscription_generation,omitempty"`
	Startup                *ExecutionStartupSelection `json:"startup,omitempty"`
	Purpose                ForkPurpose                `json:"purpose,omitempty"`
	Version                uint32                     `json:"version"`
	OpenCode               *OpenCodeForkRequests      `json:"opencode,omitempty"`
	SourceSessionID        ID                         `json:"source_session_id"`
	SourceRevision         uint64                     `json:"source_revision"`
	ChildSessionID         ID                         `json:"child_session_id"`
	RuntimeID              ID                         `json:"runtime_id"`
	NativeRequestID        ID                         `json:"native_request_id"`
	Name                   string                     `json:"name"`
	Workspace              WorkspaceType              `json:"workspace"`
	LocalOrigin            *LocalOrigin               `json:"local_origin,omitempty"`
	CreatedBy              ID                         `json:"created_by"`
	Actor                  Principal                  `json:"actor"`
	SourceJobID            ID                         `json:"source_job_id"`
	SourceAssignment       ExecutionJobInput          `json:"source_assignment"`
	Completion             ExecutionCompletion        `json:"completion"`
	Progress               ExecutionProgress          `json:"progress"`
	Snapshot               InitialExecution           `json:"snapshot"`
}

func (i ForkJobInput) Validate() error {
	if i.SourceAssignment.Version == 4 {
		if i.Startup == nil || i.Startup.Validate(i.SourceAssignment.Configuration.Harness) != nil || i.Startup.ExecutableSHA256 == "" {
			return Fail(RecoveryRequired, "The Fork executable selection is incomplete.", "Retain the original source assignment and executable identity.")
		}
	} else if i.Startup != nil {
		return Fail(RecoveryRequired, "The Fork executable selection is incomplete.", "Retain the original source assignment and executable identity.")
	}

	if i.Purpose != IndependentFork && i.Purpose != SidechatFork {
		return SidechatUnavailable()
	}
	if i.Purpose == SidechatFork && (i.Version != 3 || i.SourceAssignment.Configuration.Harness != Codex || i.SourceAssignment.Configuration.SidechatPolicy != "" || i.OpenCode != nil || i.Workspace != i.sourceWorkspace() || len(i.Progress.Subagents) != 0 || i.SourceAssignment.Fork != nil) {
		return SidechatUnavailable()
	}
	// Managed Codex Fork retains its own negotiated capability and exact protected generation.
	if i.SourceAssignment.Configuration.Subscription && !i.SourceAssignment.Configuration.IsOpenCodeGo() && (i.SourceAssignment.Configuration.Harness != Codex || i.SourceAssignment.Configuration.SubscriptionService != SubscriptionChatGPT || i.SubscriptionGeneration.Validate() != nil) {
		return Fail(Unsupported, "This managed subscription lacks its original supported Fork profile.", "Keep the original session; Fork requires a separately verified managed authentication lease.")
	}
	if (!i.SourceAssignment.Configuration.Subscription || i.SourceAssignment.Configuration.IsOpenCodeGo()) && i.SubscriptionGeneration != "" {
		return SidechatUnavailable()
	}
	harness := i.SourceAssignment.Configuration.Harness
	primary, primaryErr := i.SourceAssignment.Configuration.OpenCodePrimaryForInput(i.SourceAssignment.Input.Mode)
	if harness == OpenCode && (i.Version != 2 || i.OpenCode == nil || i.OpenCode.Validate() != nil || i.OpenCode.Fork != i.NativeRequestID || UniqueIDs([]ID{i.OpenCode.Restore, i.OpenCode.Fork, i.OpenCode.Move, i.OpenCode.Mark, i.OpenCode.DeleteSource, i.SourceSessionID, i.ChildSessionID, i.RuntimeID, i.SourceJobID, i.SourceAssignment.ExecutionID, i.SourceAssignment.InputID, i.SourceAssignment.ThreadRequestID, i.SourceAssignment.TurnRequestID}) != nil || primaryErr != nil || primary != OpenCodeBuildAgent || i.Progress.Observed.OpenCodeAgent != OpenCodeBuildAgent || i.Workspace != GeneralChat || i.LocalOrigin != nil || i.SourceAssignment.Input.Mode != ExecuteMode || i.SourceAssignment.Fork != nil || len(i.Progress.Subagents) != 0 || len(i.Progress.NativeCompactions) != 0 || i.Progress.LatestWorkspaceEventID != "" || i.Progress.LatestTodoID != "" || i.Progress.LatestPlanID != "") {
		return Fail(Unsupported, "OpenCode Fork requires the bounded Unix plain-text General Chat profile.", "Keep the original session; tools, children, compaction, Plan and previously forked sources cannot be adopted.")
	}
	validProfile := harness == Codex && ((i.Version == 1 && i.Purpose == IndependentFork) || (i.Version == 3 && i.Purpose == SidechatFork)) && i.OpenCode == nil || harness == OpenCode && i.Version == 2 && i.OpenCode != nil
	digest, digestErr := i.Snapshot.Configuration.Digest()
	if !validProfile || UniqueIDs([]ID{i.SourceSessionID, i.ChildSessionID, i.RuntimeID, i.NativeRequestID, i.SourceJobID}) != nil || i.SourceRevision == 0 || Text(i.Name, "fork name", 256, true) != nil || (i.CreatedBy != "" && i.CreatedBy.Validate() != nil) || i.CreatedBy != i.Actor.DeviceID || i.SourceAssignment.Validate() != nil || i.SourceAssignment.SessionID != i.SourceSessionID || i.Completion.Version != 2 || i.Completion.ValidateForHarness(harness) != nil || i.Completion.Outcome != ExecutionSucceeded || i.Completion.ExecutionID != i.SourceAssignment.ExecutionID || i.Completion.InputID != i.SourceAssignment.InputID || !i.Progress.CleanupVerified || !i.Progress.AutoReviews.Closed() || !i.Progress.NativeCompactions.Closed() || len(i.Progress.Subagents) != 0 || i.Progress.Waiting != (NativeWaiting{}) || i.Progress.UnconfirmedResponses != 0 || i.Progress.ExecutionID != i.Completion.ExecutionID || i.Progress.Outcome != ExecutionSucceeded || i.Progress.LastSequence != i.Completion.LastSequence || i.Progress.JobID != i.SourceJobID || i.Progress.NativeTurnID != string(i.Completion.NativeTurnID) || i.Progress.NativeThreadID != string(i.Completion.NativeThreadID) || digestErr != nil || digest != i.SourceAssignment.ConfigurationDigest || i.Snapshot.ConfigurationDigest != i.SourceAssignment.ConfigurationDigest || i.Snapshot.InitialAccountID != i.SourceAssignment.AccountID || i.Snapshot.ConnectionID != i.SourceAssignment.ConnectionID {
		return Fail(RecoveryRequired, "The fork does not identify one verified completed source boundary.", "Preserve the original session, assignment, checkpoint and cleanup evidence.")
	}
	if i.Workspace != Worktree && i.Workspace != GeneralChat && i.Workspace != Local {
		return Fail(InvalidArgument, "Unknown fork workspace.", "Select an independent Worktree, General Chat or explicit Local sharing.")
	}
	if (i.Workspace == Local) != (i.LocalOrigin != nil) || i.LocalOrigin != nil && i.LocalOrigin.MachineID != i.SourceAssignment.MachineID {
		return Fail(PermissionDenied, "Local fork lacks same-machine authority.", "Authenticate the original machine's private Worker scope.")
	}
	return nil
}

type ForkJobResult struct {
	ManagedFinish    ID                           `json:"managed_finish,omitempty"`
	Version          uint32                       `json:"version"`
	OpenCodeMappings []OpenCodeForkMessageMapping `json:"opencode_mappings,omitempty"`
	ChildSessionID   ID                           `json:"child_session_id"`
	RuntimeID        ID                           `json:"runtime_id"`
	NativeThreadID   NativeIdentity               `json:"native_thread_id"`
	NativeTurnID     NativeIdentity               `json:"native_turn_id"`
	CheckpointDigest string                       `json:"checkpoint_digest"`
	Preparation      json.RawMessage              `json:"preparation"`
	Manifest         json.RawMessage              `json:"manifest"`
	CleanupVerified  bool                         `json:"cleanup_verified"`
}

type ForkExecution struct {
	JobID            ID             `json:"job_id"`
	RuntimeID        ID             `json:"runtime_id"`
	NativeThreadID   NativeIdentity `json:"native_thread_id"`
	NativeTurnID     NativeIdentity `json:"native_turn_id"`
	CheckpointDigest string         `json:"checkpoint_digest"`
	HistoryRequestID ID             `json:"history_request_id"`
}

func (f ForkExecution) Validate(i ExecutionJobInput) error {
	if (i.Configuration.Harness != Codex && i.Configuration.Harness != OpenCode) || i.Configuration.Harness == OpenCode && i.Input.Mode != ExecuteMode || UniqueIDs([]ID{f.JobID, f.RuntimeID, f.HistoryRequestID, i.SessionID, i.ExecutionID, i.InputID, i.ThreadRequestID, i.TurnRequestID}) != nil || f.NativeThreadID.Validate(i.Configuration.Harness, NativeThreadIdentity) != nil || f.NativeTurnID.Validate(i.Configuration.Harness, NativeTurnIdentity) != nil || !canonicalDigest(f.CheckpointDigest) {
		return Fail(RecoveryRequired, "The first fork continuation lacks its original boundary.", "Retain the accepted fork job and Worker-private checkpoint.")
	}
	return nil
}

func canonicalDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (r ForkJobResult) ValidateIdentity(input ForkJobInput) error {
	harness := input.SourceAssignment.Configuration.Harness
	validProfile := harness == Codex && r.Version == input.Version && r.OpenCodeMappings == nil && r.NativeTurnID == input.Completion.NativeTurnID || harness == OpenCode && r.Version == 2 && validateOpenCodeForkMappings(r.OpenCodeMappings, input.Completion.NativeTurnID, r.NativeTurnID) == nil && r.NativeTurnID != input.Completion.NativeTurnID && r.NativeTurnID.Validate(OpenCode, NativeTurnIdentity) == nil
	if (input.SubscriptionGeneration != "" && r.ManagedFinish.Validate() != nil) || (input.SubscriptionGeneration == "" && r.ManagedFinish != "") {
		return SidechatUnavailable()
	}
	if !validProfile || !r.CleanupVerified || r.ChildSessionID != input.ChildSessionID || r.RuntimeID != input.RuntimeID || r.NativeThreadID == input.Completion.NativeThreadID || r.NativeThreadID.Validate(harness, NativeThreadIdentity) != nil || !canonicalDigest(r.CheckpointDigest) {
		return Fail(RecoveryRequired, "Fork completion lacks its exact verified child boundary.", "Retain the original Worker operation without repeating native Fork.")
	}
	return nil
}

// Validate checks the child-owned publication seed without reopening its parent.
func (f ForkOrigin) Validate() error {
	if f.Startup != nil && (f.Startup.Validate(f.Snapshot.Configuration.Harness) != nil || f.Startup.ExecutableSHA256 == "") {
		return Fail(RecoveryRequired, "The child lost its original executable selection.", "Preserve the child-owned Fork seed.")
	}
	if f.Snapshot.Configuration.SidechatPolicy != "" || f.SidechatParentSnapshot != nil {
		if f.SidechatParentSnapshot == nil || f.SidechatParentSnapshot.Configuration.SidechatPolicy != "" {
			return SidechatUnavailable()
		}
		expected, err := SidechatSnapshot(*f.SidechatParentSnapshot)
		actual, _ := json.Marshal(f.Snapshot)
		want, _ := json.Marshal(expected)
		if err != nil || string(actual) != string(want) {
			return SidechatUnavailable()
		}
	}
	harness := f.Snapshot.Configuration.Harness
	if f.OpenCodeCreationRequestID != "" && (harness != OpenCode || f.OpenCodeCreationRequestID.Validate() != nil || UniqueIDs([]ID{f.OpenCodeCreationRequestID, f.JobID, f.RuntimeID, f.SourceSessionID, f.SourceExecutionID}) != nil) {
		return Fail(RecoveryRequired, "The child lost its original OpenCode creation identity.", "Preserve the child-owned Fork seed and checkpoint.")
	}
	if f.OpenCodeCreationProof != nil && (harness != OpenCode || f.OpenCodeCreationRequestID == "" || f.OpenCodeCreationProof.ChildSessionID.Validate() != nil || !f.VerifyOpenCodeCreation(f.OpenCodeCreationProof.ChildSessionID)) {
		return Fail(RecoveryRequired, "The child lost its original OpenCode creation proof.", "Preserve the immutable verified child publication.")
	}
	validProfile := harness == Codex && f.NativeTurnID == "" || harness == OpenCode && f.NativeTurnID.Validate(OpenCode, NativeTurnIdentity) == nil && f.NativeTurnID != f.SourceTurnID
	digest, err := f.Snapshot.Configuration.Digest()
	if err != nil || digest != f.Snapshot.ConfigurationDigest || !validProfile || f.SourceRevision == 0 || f.SourceSessionID.Validate() != nil || f.SourceExecutionID.Validate() != nil || f.JobID.Validate() != nil || f.RuntimeID.Validate() != nil || f.WorkerDeviceID.Validate() != nil || f.NativeThreadID.Validate(harness, NativeThreadIdentity) != nil || f.SourceTurnID.Validate(harness, NativeTurnIdentity) != nil || !canonicalDigest(f.CheckpointDigest) || !canonicalDigest(f.JobInputDigest) || f.Snapshot.InitialAccountID.Validate() != nil || f.Snapshot.ConnectionID.Validate() != nil {
		return Fail(RecoveryRequired, "The child lost its immutable fork boundary.", "Preserve the child-owned seed and original Worker checkpoint.")
	}
	return nil
}

// All native operation identities are frozen before Worker preparation. None
// grants inference authority or permission to repeat an uncertain mutation.
type OpenCodeForkRequests struct {
	Restore      ID `json:"restore"`
	Fork         ID `json:"fork"`
	Move         ID `json:"move"`
	Mark         ID `json:"mark"`
	DeleteSource ID `json:"delete_source"`
}

func (r OpenCodeForkRequests) Validate() error {
	return UniqueIDs([]ID{r.Restore, r.Fork, r.Move, r.Mark, r.DeleteSource})
}
func (f ForkOrigin) ChildTurn() NativeIdentity {
	if f.NativeTurnID != "" {
		return f.NativeTurnID
	}
	return f.SourceTurnID
}

// Native message and part IDs are content-free provenance for atomically cloning
// canonical text. They grant no input, filesystem access or usage attribution.
type OpenCodeForkPartMapping struct {
	Source NativeIdentity `json:"source"`
	Child  NativeIdentity `json:"child"`
}
type OpenCodeForkMessageMapping struct {
	Source NativeIdentity            `json:"source"`
	Child  NativeIdentity            `json:"child"`
	Parts  []OpenCodeForkPartMapping `json:"parts"`
}

func validateOpenCodeForkMappings(mappings []OpenCodeForkMessageMapping, sourceTurn, childTurn NativeIdentity) error {
	if len(mappings) < 2 || len(mappings) > 4096 {
		return Fail(RecoveryRequired, "Fork lost its complete native identity map.", "Retain original native history and do not repeat Fork.")
	}
	seen := map[NativeIdentity]bool{}
	parts, boundary := 0, false
	for _, m := range mappings {
		if m.Source.Validate(OpenCode, NativeMessageIdentity) != nil || m.Child.Validate(OpenCode, NativeMessageIdentity) != nil || seen[m.Source] || seen[m.Child] || m.Source == m.Child || m.Parts == nil {
			return Fail(RecoveryRequired, "Fork has conflicting native ownership.", "Preserve its original identity map.")
		}
		seen[m.Source], seen[m.Child] = true, true
		if m.Source == sourceTurn {
			boundary = m.Child == childTurn
		}
		for _, p := range m.Parts {
			parts++
			if parts > 16384 || p.Source.Validate(OpenCode, NativePartIdentity) != nil || p.Child.Validate(OpenCode, NativePartIdentity) != nil || seen[p.Source] || seen[p.Child] || p.Source == p.Child {
				return Fail(RecoveryRequired, "Fork has conflicting native part ownership.", "Preserve every original native part.")
			}
			seen[p.Source], seen[p.Child] = true, true
		}
	}
	if !boundary {
		return Fail(RecoveryRequired, "Fork lost its original completed input.", "Retain the exact cloned source boundary.")
	}
	return nil
}
