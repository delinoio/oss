// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

type forkCanonicalMessage struct {
	ID    domain.ID
	Value domain.ExecutionMessage
}

// Native complete history is independently verified by the original Worker.
// This transaction separately closes every retained canonical source message
// against that map before allocating any child message or publishing the child.
// Inheritance creates neither a queued input nor an accounting observation.
func prepareOpenCodeForkTranscript(tx *store.Tx, input domain.ForkJobInput, output domain.ForkJobResult) ([]forkCanonicalMessage, error) {
	if input.Version != 2 || input.Validate() != nil || output.ValidateIdentity(input) != nil {
		return nil, forkConflict()
	}
	messages := map[string]string{}
	parts := map[string]string{}
	parent := map[string]string{}
	for _, m := range output.OpenCodeMappings {
		messages[string(m.Source)] = string(m.Child)
		for _, p := range m.Parts {
			parts[string(p.Source)] = string(p.Child)
			parent[string(p.Source)] = string(m.Source)
		}
	}
	records, err := readOpenCodeForkMessages(tx, input.SourceSessionID)
	if err != nil {
		return nil, err
	}
	result := make([]forkCanonicalMessage, 0, len(records))
	used := map[string]bool{}
	for _, row := range records {
		value, err := store.Decode[domain.ExecutionMessage](row)
		// Pinned plain-text native inputs emit an explicit empty session/input
		// diff. This independently validated absence is not a file overlay or
		// inherited conversation content; preserve it only on the source.
		if err == nil && emptyOpenCodeForkChanges(value, input.Completion.NativeThreadID) {
			continue
		}
		if err != nil || value.Inherited != nil || value.State != domain.MessageComplete || (value.Role != domain.UserMessage && value.Role != domain.AssistantMessage) || value.NativeThreadID != string(input.Completion.NativeThreadID) || value.Tool != nil || value.Artifact != nil || value.Progress != nil || value.Claude != nil || value.ClaudeTool != nil || value.ClaudeProgress != nil || value.ClaudeInterruption != nil || value.GrokTool != nil || value.GrokUser != nil || value.GrokText != nil || value.Phase != nil || used[value.NativeID] || parts[value.NativeID] == "" || parent[value.NativeID] != value.NativeParentID || messages[value.NativeTurnID] == "" || messages[value.NativeParentID] == "" || value.FirstSequence == 0 || value.LastSequence < value.FirstSequence {
			return nil, forkConflict()
		}
		if value.Role == domain.UserMessage && (value.NativeParentID != value.NativeTurnID || value.InputID.Validate() != nil) || value.Role == domain.AssistantMessage && value.InputID != "" {
			return nil, forkConflict()
		}
		used[value.NativeID] = true
		value.Inherited = &domain.ForkMessageOrigin{SessionID: input.SourceSessionID, MessageID: row.ID, ExecutionID: value.ExecutionID, InputID: value.InputID, FirstSequence: value.FirstSequence, LastSequence: value.LastSequence}
		value.ExecutionID, value.InputID = input.RuntimeID, ""
		value.NativeThreadID, value.NativeTurnID = string(output.NativeThreadID), messages[value.NativeTurnID]
		value.NativeID, value.NativeParentID = parts[value.NativeID], messages[value.NativeParentID]
		value.FirstSequence, value.LastSequence = 0, 0
		result = append(result, forkCanonicalMessage{ID: domain.NewID(), Value: value})
	}
	if len(result) < 2 {
		return nil, forkConflict()
	}
	return result, nil
}

func emptyOpenCodeForkChanges(value domain.ExecutionMessage, thread domain.NativeIdentity) bool {
	p := value.Progress
	if value.Inherited != nil || value.NativeThreadID != string(thread) || domain.NativeIdentity(value.NativeTurnID).Validate(domain.OpenCode, domain.NativeTurnIdentity) != nil || value.Role != domain.ProgressMessage || value.State != domain.MessageComplete || value.Text != "" || value.NativeID != "" || value.NativeParentID != "" || value.InputID != "" || value.Tool != nil || value.Artifact != nil || value.Claude != nil || value.ClaudeTool != nil || value.ClaudeProgress != nil || value.ClaudeInterruption != nil || value.GrokTool != nil || value.GrokUser != nil || value.GrokText != nil || value.Phase != nil || value.FirstSequence == 0 || value.LastSequence != value.FirstSequence || p == nil || p.Kind != domain.OpenCodeChangesProgressKind || p.Compaction != nil || p.Workspace != nil || p.Todo != nil || p.Plan != nil || p.Diff != nil || p.Changes == nil {
		return false
	}
	c := p.Changes
	return c.Validate() == nil && len(c.Diffs) == 0 && c.Title == nil && c.Body == nil && (c.Source == domain.OpenCodeSessionDiff || c.Source == domain.OpenCodeInputSummary && c.NativeMessageID == value.NativeTurnID)
}

func readOpenCodeForkMessages(tx *store.Tx, session domain.ID) ([]store.Record, error) {
	records := []store.Record{}
	after := domain.ID("")
	bytes := 0
	for {
		page, err := tx.List(store.Filter{Kind: domain.MessageKind, SessionID: session, After: after, Limit: 200})
		if err != nil {
			return nil, err
		}
		for _, row := range page {
			bytes += len(row.Data)
			if len(records) >= 10000 || bytes > 8<<20 {
				return nil, forkConflict()
			}
			records = append(records, row)
			after = row.ID
		}
		if len(page) < 200 {
			break
		}
	}
	return records, nil
}

// Public canonical eligibility is checked before any native preparation and
// again under publication authority. Native private history is a separate proof.
func validateOpenCodeForkTranscript(tx *store.Tx, source domain.ID, thread domain.NativeIdentity) error {
	records, err := readOpenCodeForkMessages(tx, source)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	count := 0
	for _, row := range records {
		value, err := store.Decode[domain.ExecutionMessage](row)
		if err == nil && emptyOpenCodeForkChanges(value, thread) {
			continue
		}
		if err != nil || value.Inherited != nil || value.State != domain.MessageComplete || (value.Role != domain.UserMessage && value.Role != domain.AssistantMessage) || value.NativeThreadID != string(thread) || value.Tool != nil || value.Artifact != nil || value.Progress != nil || value.Claude != nil || value.ClaudeTool != nil || value.ClaudeProgress != nil || value.ClaudeInterruption != nil || value.GrokTool != nil || value.GrokUser != nil || value.GrokText != nil || value.Phase != nil || seen[value.NativeID] || domain.NativeIdentity(value.NativeID).Validate(domain.OpenCode, domain.NativePartIdentity) != nil || domain.NativeIdentity(value.NativeParentID).Validate(domain.OpenCode, domain.NativeMessageIdentity) != nil || domain.NativeIdentity(value.NativeTurnID).Validate(domain.OpenCode, domain.NativeTurnIdentity) != nil || value.FirstSequence == 0 || value.LastSequence < value.FirstSequence {
			return forkConflict()
		}
		if value.Role == domain.UserMessage && (value.NativeParentID != value.NativeTurnID || value.InputID.Validate() != nil) || value.Role == domain.AssistantMessage && value.InputID != "" {
			return forkConflict()
		}
		seen[value.NativeID] = true
		count++
	}
	if count < 2 {
		return forkConflict()
	}
	return nil
}
