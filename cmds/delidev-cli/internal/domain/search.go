package domain

import (
	"slices"
	"strings"
)

// SearchSelection describes current retained session membership. AccountID is
// matched to the message's original execution, never an edited Agent candidate.
type SearchSelection struct {
	SessionID ID               `json:"session_id,omitempty"`
	ProjectID ID               `json:"project_id,omitempty"`
	AgentID   ID               `json:"agent_id,omitempty"`
	AccountID ID               `json:"account_id,omitempty"`
	Outcome   ExecutionOutcome `json:"outcome,omitempty"`
	Archive   ArchiveState     `json:"archive,omitempty"`
}

func (f SearchSelection) Validate() error {
	for _, id := range []ID{f.SessionID, f.ProjectID, f.AgentID, f.AccountID} {
		if id != "" {
			if err := id.Validate(); err != nil {
				return err
			}
		}
	}
	if f.Outcome != "" && !slices.Contains([]ExecutionOutcome{ExecutionNotStarted, ExecutionRunning, ExecutionSucceeded, ExecutionFailed, ExecutionStopped}, f.Outcome) {
		return Fail(InvalidArgument, "Unknown execution outcome filter.", "Select a supported outcome or omit the filter.")
	}
	if f.Archive != "" && !slices.Contains([]ArchiveState{NotArchived, ArchivePending, Archived}, f.Archive) {
		return Fail(InvalidArgument, "Unknown archive filter.", "Select active, archiving or archived, or omit the filter to include all.")
	}
	return nil
}

// SearchText indexes only already-retained transcript content, including native
// tool and plan observations. It never reads a workspace, credential or private
// harness history. Separators prevent a match spanning unrelated text fields.
func (m ExecutionMessage) SearchText() string {
	parts := []string{m.Text}
	add := func(value *string) {
		if value != nil {
			parts = append(parts, *value)
		}
	}
	changes := func(values []FileChangeObservation) {
		for _, v := range values {
			parts = append(parts, v.Path, v.Diff)
			add(v.MovePath)
		}
	}
	tool := func(value ToolSnapshot) {
		if value.Command != nil {
			parts = append(parts, value.Command.Command, value.Command.Cwd)
			add(value.Command.AggregatedOutput)
		}
		changes(value.Changes)
	}
	if m.Tool != nil {
		tool(m.Tool.Started)
		if m.Tool.Completed != nil {
			tool(*m.Tool.Completed)
		}
		add(m.Tool.Output)
		for _, input := range m.Tool.Inputs {
			parts = append(parts, input.Input.Text)
		}
		for _, patch := range m.Tool.Patches {
			changes(patch.Changes)
		}
	}
	artifact := func(v ArtifactSnapshot) {
		parts = append(parts, v.Text)
		parts = append(parts, v.Summary...)
		parts = append(parts, v.Content...)
	}
	if m.Artifact != nil {
		artifact(m.Artifact.Started)
		if m.Artifact.Completed != nil {
			artifact(*m.Artifact.Completed)
		}
		// Deltas are searchable both as individual observations and as the
		// ordered text of each native part, without replacing final content.
		type partKey struct {
			kind  ArtifactDeltaKind
			index int64
		}
		streams := map[partKey]*strings.Builder{}
		order := []partKey{}
		for _, delta := range m.Artifact.Deltas {
			key := partKey{kind: delta.Delta.Kind, index: -1}
			if delta.Delta.Index != nil {
				key.index = *delta.Delta.Index
			}
			if streams[key] == nil {
				order = append(order, key)
				streams[key] = &strings.Builder{}
			}
			streams[key].WriteString(delta.Delta.Text)
		}
		for _, key := range order {
			parts = append(parts, streams[key].String())
		}
	}
	if m.Progress != nil {
		add(m.Progress.Diff)
		if m.Progress.Plan != nil {
			add(m.Progress.Plan.Explanation)
			for _, step := range m.Progress.Plan.Steps {
				parts = append(parts, step.Step)
			}
		}
	}
	return strings.ToLower(strings.Join(parts, "\x1f"))
}
