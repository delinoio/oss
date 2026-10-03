package opencode

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const maxCheckpointBytes = 8 << 20

// CheckpointReference must be retained with independent Worker assignment and
// cleanup evidence. Neither the descriptor nor private bytes grant a process,
// account, publication, resumed binding or new input authority.
type CheckpointReference struct {
	SHA256            string    `json:"sha256"`
	OwnerID           domain.ID `json:"owner_id"`
	CreationRequestID domain.ID `json:"creation_request_id"`
	InputRequestID    domain.ID `json:"input_request_id"`
	SessionID         string    `json:"session_id"`
	InputID           string    `json:"input_id"`
	PartID            string    `json:"part_id"`
	InputSHA256       string    `json:"input_sha256"`
	HistorySHA256     string    `json:"history_sha256"`
	RequiresResume    bool      `json:"requires_resume"`
}

type nativeCheckpoint struct {
	Version           uint32                     `json:"version"`
	NativeVersion     string                     `json:"native_version"`
	Reference         CheckpointReference        `json:"reference"`
	RuntimeHome       string                     `json:"runtime_home"`
	Workspace         string                     `json:"workspace"`
	NativeRoot        string                     `json:"native_root"`
	FilesystemRoot    string                     `json:"filesystem_root,omitempty"`
	References        []WorkspaceReference       `json:"workspace_references,omitempty"`
	Project           string                     `json:"project"`
	Slug              string                     `json:"slug"`
	Created           int64                      `json:"created"`
	SettingsSHA256    string                     `json:"settings_sha256"`
	CredentialSHA256  string                     `json:"credential_sha256"`
	History           HistoryObservation         `json:"history"`
	Previous          []HistoryObservation       `json:"previous,omitempty"`
	PredecessorSHA256 string                     `json:"predecessor_sha256,omitempty"`
	ProjectAdoption   *checkpointProjectAdoption `json:"project_adoption,omitempty"`
	Snapshot          *checkpointSnapshot        `json:"snapshot_restoration,omitempty"`
	Tools             *checkpointToolHistory     `json:"tool_restoration,omitempty"`
	Stop              *StopReceipt               `json:"stop,omitempty"`
	Files             []checkpointFile           `json:"files"`
}

// RetainCheckpoint is available only after original complete-history and
// joined process cleanup succeeded. Ordinary Close, owned-stop cleanup and a
// failed completion attempt cannot be upgraded into this evidence. Private
// bytes contain comparison metadata/digests, never prompts, answers or tokens.
func (a *OwnedAPI) RetainCheckpoint(ctx context.Context) (raw []byte, reference CheckpointReference, returned error) {
	if !a.valid() {
		return nil, CheckpointReference{}, sessionInvalid()
	}
	select {
	case a.reading <- struct{}{}:
		defer func() { <-a.reading }()
	case <-ctx.Done():
		return nil, CheckpointReference{}, unavailable()
	}
	s := a.session
	phase := "eligibility"
	defer func() {
		if returned != nil && s.logger != nil {
			s.logger.WarnContext(ctx, "opencode_original_checkpoint_uncertain", "owner_id", s.owner, "phase", phase, "code", domain.SafeError(returned).Code)
		}
	}()
	if err := s.enter(ctx); err != nil {
		return nil, CheckpointReference{}, err
	}
	defer s.leave()
	if ctx.Err() != nil {
		return nil, CheckpointReference{}, unavailable()
	}
	if a.checkpointAttempted {
		phase = "retained-files"
		if len(a.checkpointBytes) == 0 || InspectCheckpoint(ctx, s.runtimeHome, a.checkpointBytes, a.checkpointReference) != nil {
			a.checkpointBytes = nil
			return nil, CheckpointReference{}, sessionUncertain()
		}
		return bytes.Clone(a.checkpointBytes), a.checkpointReference, nil
	}
	if !a.completionAttempted || (a.completed == nil) == (a.stoppedCompletion == nil) || s.problem != nil || s.creation == nil || s.input == nil || s.apiProfile == nil || !s.apiVerified || s.observer == nil || !canonicalDirectory(s.runtimeHome) || len(s.children) != 0 || !s.childInventoryVerified {
		return nil, CheckpointReference{}, sessionUncertain()
	}
	var history HistoryObservation
	var stop *StopReceipt
	if a.completed != nil {
		history = copyHistoryObservation(*a.completed)
	} else {
		history = copyHistoryObservation(a.stoppedCompletion.History)
		value := a.stoppedCompletion.Stop
		if !value.CleanupVerified || !value.NativeAttempted || value.RepliesUncertain {
			return nil, CheckpointReference{}, sessionUncertain()
		}
		stop = &value
	}
	c, i := s.creation, s.input
	if history.RequestID != i.receipt.RequestID || history.SessionID != c.identity.id || history.InputID != i.receipt.MessageID || !validCheckpointHistory(history) {
		return nil, CheckpointReference{}, sessionUncertain()
	}
	// Once inspection starts, a later call cannot replace a failed or changed
	// original file observation with newly accepted runtime contents.
	a.checkpointAttempted = true
	phase = "settings"
	settings, err := checkpointSettings(s)
	if err != nil {
		return nil, CheckpointReference{}, err
	}
	phase = "runtime-files"
	files, err := checkpointFiles(ctx, s.runtimeHome)
	if err != nil {
		return nil, CheckpointReference{}, err
	}
	s.observer.mu.Lock()
	last := s.observer.messages[history.AssistantID]
	if last == nil || last.value.Assistant == nil || s.observer.problem != nil || s.observer.progress.NeedsRecovery {
		s.observer.mu.Unlock()
		return nil, CheckpointReference{}, sessionUncertain()
	}
	requiresResume := stop != nil || s.observer.progress.StoppedOnRejection || last.value.Assistant.Error != nil
	s.observer.mu.Unlock()
	ref := CheckpointReference{OwnerID: s.owner, CreationRequestID: c.request, InputRequestID: i.receipt.RequestID, SessionID: history.SessionID, InputID: history.InputID, PartID: i.receipt.PartID, InputSHA256: hex.EncodeToString(i.digest[:]), HistorySHA256: history.Digest, RequiresResume: requiresResume}
	value := nativeCheckpoint{Version: 1, NativeVersion: SupportedVersion, Reference: ref, RuntimeHome: s.runtimeHome, Workspace: s.cwd, NativeRoot: s.runtimeRoot, Project: c.identity.project, Slug: c.identity.slug, Created: c.identity.created, SettingsSHA256: settings, CredentialSHA256: mutationDigest([]byte(s.apiProfile.Token)), History: history, Stop: stop, Files: files}
	if scope := s.apiProfile.WorkspaceRoot; scope != nil && scope.windowsGlobal() {
		if scope.native != s.runtimeRoot || scope.directory != s.cwd || c.identity.project != "global" {
			return nil, CheckpointReference{}, sessionUncertain()
		}
		value.Version, value.FilesystemRoot = 2, scope.boundary
	}
	value.References = slices.Clone(s.apiProfile.References)
	if s.predecessor != nil {
		value.PredecessorSHA256 = s.predecessorDigest
		for _, prior := range checkpointHistories(*s.predecessor) {
			value.Previous = append(value.Previous, copyHistoryObservation(prior))
		}
	}
	if s.projectAdoption != nil {
		copy := *s.projectAdoption
		value.ProjectAdoption = &copy
	}
	if value.ProjectAdoption == nil && s.predecessor != nil && s.predecessor.ProjectAdoption != nil {
		copy := *s.predecessor.ProjectAdoption
		value.ProjectAdoption = &copy
	}
	value.Tools = s.checkpointToolHistory(value)
	phase = "snapshot"
	if value.NativeRoot == value.Workspace && checkpointHasSnapshotFiles(value) {
		value.Snapshot, err = s.retainCheckpointSnapshot(ctx, value)
		if err != nil {
			return nil, CheckpointReference{}, err
		}
		// Snapshot export creates only its own private archive after native
		// cleanup. Pin the final inventory only after all export children join.
		value.Files, err = checkpointFiles(ctx, s.runtimeHome)
		if err != nil {
			return nil, CheckpointReference{}, err
		}
	}
	phase = "metadata"
	raw, err = json.Marshal(value)
	if err != nil || len(raw) > maxCheckpointBytes {
		return nil, CheckpointReference{}, sessionUncertain()
	}
	ref.SHA256 = mutationDigest(raw)
	if _, err := decodeCheckpoint(raw, ref, s.runtimeHome); err != nil {
		if s.logger != nil {
			s.logger.WarnContext(ctx, "opencode_checkpoint_metadata_invalid", "owner_id", s.owner, "snapshot_valid", validCheckpointSnapshot(value), "files_valid", validateCheckpointFiles(value.Files), "lineage_valid", validCheckpointLineage(value))
		}
		return nil, CheckpointReference{}, err
	}
	a.checkpointBytes, a.checkpointReference = bytes.Clone(raw), ref
	if s.logger != nil {
		toolParts, oncePermissions, alwaysPermissions, policyClosures, questionReplies := 0, 0, 0, 0, 0
		questionDismissals, permissionRejections, rejectionPolicies := 0, 0, 0
		loadedInstructionReads := 0
		var toolProfileVersion uint32
		if value.Tools != nil {
			toolProfileVersion = value.Tools.Version
			for _, part := range value.Tools.Parts {
				if part.InstructionsLoaded {
					loadedInstructionReads++
				}
			}
			toolParts, oncePermissions = len(value.Tools.Parts), len(value.Tools.Once)
			permissionRejections, rejectionPolicies = len(value.Tools.Rejections), len(value.Tools.RejectionPolicy)
			alwaysPermissions, policyClosures, questionReplies = len(value.Tools.Always), len(value.Tools.Policy), len(value.Tools.Questions)
			for _, reply := range value.Tools.Questions {
				if reply.Claim.Kind == RejectQuestionMutation {
					questionDismissals++
				}
			}
		}
		s.logger.InfoContext(ctx, "opencode_original_checkpoint_observed", "owner_id", s.owner, "request_id", i.receipt.RequestID, "messages", len(history.Messages), "entries", len(value.Files), "restorable_tool_profile_version", toolProfileVersion, "restorable_inline_tool_parts", toolParts, "restorable_once_permissions", oncePermissions, "restorable_always_permissions", alwaysPermissions, "restorable_policy_closures", policyClosures, "restorable_question_replies", questionReplies, "restorable_question_dismissals", questionDismissals, "restorable_permission_rejections", permissionRejections, "restorable_rejection_closures", rejectionPolicies, "restorable_instruction_reads", loadedInstructionReads, "restorable_todo_state", latestCheckpointTodo(value) != nil)
	}
	return raw, ref, nil
}

// InspectCheckpoint is read-only. Caller-supplied home and reference must come
// from original durable ownership, not from the bytes being checked. Matching
// files cannot establish a new process/lease or original report acceptance.
func InspectCheckpoint(ctx context.Context, home string, raw []byte, ref CheckpointReference) error {
	value, err := decodeCheckpoint(raw, ref, home)
	if err != nil {
		return err
	}
	files, err := checkpointFiles(ctx, home)
	if err != nil || !sameCheckpointFiles(files, value.Files) {
		return sessionUncertain()
	}
	return nil
}

func decodeCheckpoint(raw []byte, ref CheckpointReference, home string) (nativeCheckpoint, error) {
	var value nativeCheckpoint
	if len(raw) == 0 || len(raw) > maxCheckpointBytes || !checkpointDigest(ref.SHA256) || mutationDigest(raw) != ref.SHA256 || domain.UniqueIDs([]domain.ID{ref.OwnerID, ref.CreationRequestID, ref.InputRequestID}) != nil || !nativeID(ref.SessionID, "ses") || !nativeID(ref.InputID, "msg") || !nativeID(ref.PartID, "prt") || !checkpointDigest(ref.InputSHA256) || !checkpointDigest(ref.HistorySHA256) || domain.Decode(raw, &value) != nil {
		return nativeCheckpoint{}, sessionUncertain()
	}
	expected := ref
	expected.SHA256 = ""
	if !validCheckpointRoots(value) || value.NativeVersion != SupportedVersion || value.Reference != expected || value.RuntimeHome != home || !checkpointPath(home) || !checkpointPath(value.Workspace) || directoryContains(home, value.Workspace) || directoryContains(value.Workspace, home) || !checkpointDigest(value.SettingsSHA256) || !checkpointDigest(value.CredentialSHA256) || domain.Text(value.Project, "native project", 256, true) != nil || domain.Text(value.Slug, "native slug", 256, true) != nil || value.Created <= 0 || value.Created > 9007199254740991 || !validateCheckpointFiles(value.Files) || !validCheckpointHistory(value.History) || value.History.RequestID != ref.InputRequestID || value.History.SessionID != ref.SessionID || value.History.InputID != ref.InputID || value.History.Digest != ref.HistorySHA256 {
		return nativeCheckpoint{}, sessionUncertain()
	}
	input := value.History.Messages[0]
	if len(input.Parts) != 1 || input.Parts[0].ID != ref.PartID || input.Parts[0].Kind != TextPartKind || !validCheckpointLineage(value) || !validCheckpointSnapshot(value) || !validCheckpointProjectAdoption(value) || !validCheckpointReferences(value) {
		return nativeCheckpoint{}, sessionUncertain()
	}
	if stop := value.Stop; stop != nil {
		if !ref.RequiresResume || stop.RequestID.Validate() != nil || stop.InputRequestID != ref.InputRequestID || stop.SessionID != ref.SessionID || stop.MessageID != ref.InputID || !stop.NativeAttempted || !stop.CleanupVerified || !stop.TerminalObserved || !stop.IdleObserved || !stop.PendingCleared || stop.RepliesUncertain || !stop.InterruptedObserved && !stop.HTTPAccepted {
			return nativeCheckpoint{}, sessionUncertain()
		}
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, raw) {
		return nativeCheckpoint{}, sessionUncertain()
	}
	return value, nil
}

func checkpointHistories(value nativeCheckpoint) []HistoryObservation {
	history := make([]HistoryObservation, 0, len(value.Previous)+1)
	history = append(history, value.Previous...)
	return append(history, value.History)
}

func validCheckpointLineage(value nativeCheckpoint) bool {
	if len(value.Previous) >= maxObservedMessages/2 || (len(value.Previous) == 0) != (value.PredecessorSHA256 == "") || len(value.Previous) > 0 && !checkpointDigest(value.PredecessorSHA256) || !validCheckpointTools(value) {
		return false
	}
	requests := map[domain.ID]bool{value.Reference.CreationRequestID: true, value.Reference.OwnerID: true}
	seen := map[string]bool{}
	messages, parts := 0, 0
	for _, history := range checkpointHistories(value) {
		if !validCheckpointHistory(history) || history.SessionID != value.Reference.SessionID || requests[history.RequestID] {
			return false
		}
		requests[history.RequestID] = true
		if history.Todo != nil {
			if seen[history.Todo.EventID] {
				return false
			}
			seen[history.Todo.EventID] = true
		}
		for _, message := range history.Messages {
			if seen[message.ID] {
				return false
			}
			seen[message.ID] = true
			messages++
			for _, part := range message.Parts {
				if seen[part.ID] {
					return false
				}
				seen[part.ID] = true
				parts++
			}
		}
	}
	return messages <= maxObservedMessages && parts <= maxObservedParts
}

func checkpointPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\r\n") && domain.Text(path, "private checkpoint path", 32768, true) == nil
}

func validCheckpointHistory(h HistoryObservation) bool {
	if h.Todo != nil && (!nativeID(h.Todo.EventID, "evt") || !checkpointDigest(h.Todo.Digest)) {
		return false
	}
	if h.RequestID.Validate() != nil || !nativeID(h.SessionID, "ses") || !nativeID(h.InputID, "msg") || !nativeID(h.AssistantID, "msg") || len(h.Messages) < 2 || len(h.Messages) > maxObservedMessages || h.Messages[0].ID != h.InputID || h.Messages[0].Role != UserMessageRole || h.Messages[len(h.Messages)-1].ID != h.AssistantID || !checkpointDigest(h.Digest) {
		return false
	}
	seen := map[string]bool{}
	parts := 0
	for index, m := range h.Messages {
		if !nativeID(m.ID, "msg") || seen[m.ID] || !checkpointDigest(m.Digest) || index > 0 && m.Role != AssistantMessageRole || m.Parts == nil {
			return false
		}
		seen[m.ID] = true
		for _, p := range m.Parts {
			if !nativeID(p.ID, "prt") || seen[p.ID] || !checkpointDigest(p.Digest) || !validCheckpointPart(p.Kind) {
				return false
			}
			seen[p.ID] = true
			parts++
		}
	}
	if parts > maxObservedParts {
		return false
	}
	digest := h.Digest
	h.Digest = ""
	raw, err := json.Marshal(h)
	return err == nil && mutationDigest(raw) == digest
}

func validCheckpointPart(kind PartKind) bool {
	switch kind {
	case TextPartKind, ReasoningPartKind, FilePartKind, ToolPartKind, StepStartPartKind, StepFinishPartKind, SnapshotPartKind, PatchPartKind, AgentPartKind, RetryPartKind, CompactionPartKind, SubtaskPartKind:
		return true
	default:
		return false
	}
}

func checkpointSettings(s *sessionAPI) (string, error) {
	if s.apiProfile == nil {
		return "", sessionUncertain()
	}
	return checkpointSettingsForAgent(s, s.apiProfile.Settings.Agent)
}

// Only the native per-input Build/Plan selector may differ on restoration.
// The caller supplies the preceding selector from its immutable assignment;
// every model, permission, instruction and relay fact must still match.
func checkpointSettingsForAgent(s *sessionAPI, agent PrimaryAgent) (string, error) {
	p := s.apiProfile
	if p == nil || s.creation == nil || !equalSessionSettings(p.Settings, s.creation.settings) || p.ProjectInstructions == nil || (agent != BuildAgent && agent != PlanAgent) {
		return "", sessionUncertain()
	}
	if err := p.inspectInstructions(); err != nil {
		return "", err
	}
	type instruction struct {
		Path   string
		SHA256 string
		Size   int64
	}
	var sources []instruction
	for _, source := range p.ProjectInstructions.Sources {
		sources = append(sources, instruction{source.Path, hex.EncodeToString(source.Digest[:]), source.Size})
	}
	raw, err := json.Marshal(struct {
		Agent                     PrimaryAgent
		Provider, Model           string
		Permission                []PermissionRule
		ContextLimit, OutputLimit int64
		Rejection                 RejectionPolicy
		InstructionsSHA256        string
		TitleSHA256, RelaySHA256  string
		Sources                   []instruction
		References                []WorkspaceReference `json:",omitempty"`
	}{agent, p.Settings.Provider, p.Settings.Model, p.Settings.Permission, p.ContextLimit, p.OutputLimit, p.Rejection, mutationDigest([]byte(p.Instructions)), mutationDigest([]byte(p.Settings.Title)), mutationDigest([]byte(p.BaseURL)), sources, p.References})
	if err != nil {
		return "", sessionUncertain()
	}
	return mutationDigest(raw), nil
}
