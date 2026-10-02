// SPDX-License-Identifier: Apache-2.0
package server

import (
	"reflect"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func cloneSubagentTask(value *domain.SubagentTask) *domain.SubagentTask {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneSubagentString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneSubagentUint32(value *uint32) *uint32 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneSubagentBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// mergeSubagentTask keeps task metadata tied to the source that authenticated
// it. Content/history reports may omit or repeat the original task, but cannot
// patch it; only an acknowledged task report may add or change mutable fields.
// Agent type and depth are immutable once observed, and skip-transcript=true
// remains sticky so a regressed Worker cannot reopen history eligibility.
func mergeSubagentTask(prior, incoming *domain.SubagentTask, source domain.SubagentSource) (*domain.SubagentTask, error) {
	if source != domain.ClaudeTaskSource {
		if incoming == nil {
			return cloneSubagentTask(prior), nil
		}
		if !reflect.DeepEqual(prior, incoming) {
			return nil, executionEventConflict()
		}
		return cloneSubagentTask(prior), nil
	}
	if prior == nil {
		return cloneSubagentTask(incoming), nil
	}
	merged := cloneSubagentTask(prior)
	if incoming == nil {
		return merged, nil
	}
	if incoming.AgentType != nil {
		if prior.AgentType != nil && *prior.AgentType != *incoming.AgentType {
			return nil, executionEventConflict()
		}
		merged.AgentType = cloneSubagentString(incoming.AgentType)
	}
	if incoming.Depth != nil {
		if prior.Depth != nil && *prior.Depth != *incoming.Depth {
			return nil, executionEventConflict()
		}
		merged.Depth = cloneSubagentUint32(incoming.Depth)
	}
	if incoming.Description != nil {
		merged.Description = cloneSubagentString(incoming.Description)
	}
	if incoming.Backgrounded != nil {
		merged.Backgrounded = cloneSubagentBool(incoming.Backgrounded)
	}
	if incoming.SkipTranscript != nil {
		if prior.SkipTranscript != nil && *prior.SkipTranscript && !*incoming.SkipTranscript {
			return nil, executionEventConflict()
		}
		merged.SkipTranscript = cloneSubagentBool(incoming.SkipTranscript)
	}
	if incoming.Ambient != nil {
		merged.Ambient = cloneSubagentBool(incoming.Ambient)
	}
	return merged, nil
}

func publishSubagents(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, p *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	if input.Configuration.Harness != domain.Codex && input.Configuration.Harness != domain.ClaudeCode {
		return executionEventConflict()
	}
	next, err := domain.ApplySubagents(p.Subagents, p.NativeThreadID, event.Subagents)
	if err != nil {
		return err
	}
	owned, err := tx.SubagentIdentitiesOwned(session.ID, input.ExecutionID, event.Subagents)
	if err != nil {
		return err
	}
	if !owned {
		return executionEventConflict()
	}
	for _, child := range event.Subagents {
		usage := child.Usage
		codex := input.Configuration.Harness == domain.Codex
		if codex != strings.HasPrefix(string(child.Source), "codex-") || codex && (domain.ID(child.NativeID).Validate() != nil || domain.ID(child.ParentID).Validate() != nil || child.ParentToolID != "" || child.Tool != nil || child.Task != nil || len(child.Tools) != 0) {
			return executionEventConflict()
		}
		if !codex && child.ParentToolID == "" {
			return executionEventConflict()
		}
		if !codex {
			if _, exists := p.Subagents[child.NativeID]; !exists && (child.Source != domain.ClaudeTaskSource || child.Task == nil) {
				// Content/history can refine only an original local_agent task.
				// Parent-tool ownership alone does not establish a native child.
				return executionEventConflict()
			}
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
			if child.RequestedModel != nil && prior.Observation.RequestedModel != nil && *child.RequestedModel != *prior.Observation.RequestedModel {
				return executionEventConflict()
			}
			child.Task, err = mergeSubagentTask(prior.Observation.Task, child.Task, child.Source)
			if err != nil {
				return err
			}
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
