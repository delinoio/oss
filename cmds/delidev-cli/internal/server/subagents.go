package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"strings"
)

func publishSubagents(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, p *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	if input.Configuration.Harness != domain.Codex && input.Configuration.Harness != domain.ClaudeCode {
		return executionEventConflict()
	}
	next, err := domain.ApplySubagents(p.Subagents, p.NativeThreadID, event.Subagents)
	if err != nil {
		return err
	}
	for _, child := range event.Subagents {
		usage := child.Usage
		codex := input.Configuration.Harness == domain.Codex
		if codex != strings.HasPrefix(string(child.Source), "codex-") || codex && (domain.ID(child.NativeID).Validate() != nil || domain.ID(child.ParentID).Validate() != nil || child.ParentToolID != "") {
			return executionEventConflict()
		}
		if !codex && child.ParentToolID == "" {
			return executionEventConflict()
		}
		if !codex {
			if child.ParentID == p.NativeThreadID {
				if child.Tool == nil || validateClaudeProgressTool(tx, input, session, event, *child.Tool, false) != nil {
					return executionEventConflict()
				}
			} else {
				parent, ok := p.Subagents[child.ParentID]
				if !ok || child.Tool != nil {
					return executionEventConflict()
				}
				matched := false
				for _, tool := range parent.Tools {
					if tool.NativeID == child.ParentToolID && (tool.Name == "Agent" || tool.Name == "Task") {
						matched = true
					}
				}
				if !matched {
					return executionEventConflict()
				}
			}
		}
		owned, err := tx.SubagentIdentityOwned(session.ID, input.ExecutionID, child.ID, child.NativeID)
		if err != nil {
			return err
		}
		if !owned {
			return executionEventConflict()
		}
		first, revision := event.Sequence, uint64(0)
		evidence := []domain.SubagentEvidence{}
		if _, exists := p.Subagents[child.NativeID]; exists {
			record, err := tx.Get(domain.SubagentKind, child.ID)
			if err != nil {
				return err
			}
			prior, err := store.Decode[domain.SubagentRecord](record)
			if err != nil || prior.ExecutionID != input.ExecutionID || prior.RootID != event.NativeThreadID {
				return executionEventConflict()
			}
			first, revision = prior.FirstSequence, record.Revision
			evidence = prior.Sources
			// Omission means no new observation, not erasure of already retained
			// output/model/counter evidence. Original receipts retain each report.
			if child.Output == nil {
				child.Output = prior.Observation.Output
			}
			if child.Usage == nil {
				child.Usage = prior.Observation.Usage
			}
			if child.ObservedModel == nil {
				child.ObservedModel = prior.Observation.ObservedModel
			}
			if child.RequestedModel == nil {
				child.RequestedModel = prior.Observation.RequestedModel
			}
		}
		child.Tools = next[child.NativeID].Tools
		if len(evidence) >= 4096 {
			return domain.Fail(domain.ResourceExhausted, "Child source coverage reached its bound.", "Retain its original observations; no evidence was truncated.")
		}
		evidence = append(evidence, domain.SubagentEvidence{Source: child.Source, SourceID: child.SourceID, Sequence: event.Sequence, Usage: usage})
		value := domain.SubagentRecord{Sources: evidence, ExecutionID: input.ExecutionID, Harness: input.Configuration.Harness, Version: input.Installation.Version, RootID: event.NativeThreadID, FirstSequence: first, LastSequence: event.Sequence, Observation: child}
		if _, err := tx.Put(domain.SubagentKind, child.ID, revision, session.ID, session.ProjectID, value); err != nil {
			return err
		}
	}
	p.Subagents = next
	return nil
}
