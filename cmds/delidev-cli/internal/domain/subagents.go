// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"slices"
	"strconv"
)

type SubagentStatus string
type SubagentSource string
type SubagentUsageScope string

const (
	SubagentPending          SubagentStatus     = "pending"
	SubagentRunning          SubagentStatus     = "running"
	SubagentPaused           SubagentStatus     = "paused"
	SubagentCompleted        SubagentStatus     = "completed"
	SubagentFailed           SubagentStatus     = "failed"
	SubagentInterrupted      SubagentStatus     = "interrupted"
	SubagentShutdown         SubagentStatus     = "shutdown"
	SubagentUnavailable      SubagentStatus     = "unavailable"
	CodexCollaborationSource SubagentSource     = "codex-collaboration"
	CodexActivitySource      SubagentSource     = "codex-activity"
	CodexHistorySource       SubagentSource     = "codex-history"
	ClaudeTaskSource         SubagentSource     = "claude-task"
	ClaudeContentSource      SubagentSource     = "claude-content"
	ClaudeHistorySource      SubagentSource     = "claude-history"
	SubagentCumulativeUsage  SubagentUsageScope = "child-cumulative"
	SubagentResponseUsage    SubagentUsageScope = "child-response"
)

func (s SubagentStatus) Terminal() bool {
	return slices.Contains([]SubagentStatus{SubagentCompleted, SubagentFailed, SubagentInterrupted, SubagentShutdown}, s)
}

// These counters are observations only. They never enter the response billing
// ledger: task, context and parent-inclusive counters can overlap them.
type SubagentUsage struct {
	Scope  SubagentUsageScope `json:"scope"`
	Total  *string            `json:"total"`
	Input  *string            `json:"input"`
	Output *string            `json:"output"`
	// Validated native counter metadata stays an exact JSON string, so numeric
	// spelling and overlapping report families survive JavaScript decoding.
	NativeReport string `json:"native_report,omitempty"`
}

type SubagentOutput struct {
	NativeMessageID string `json:"native_message_id"`
	Text            string `json:"text"`
	// Full native blocks are not reconstructed from this observed text subset.
	Partial bool                  `json:"partial"`
	Blocks  []SubagentOutputBlock `json:"blocks,omitempty"`
}

// Preserve the order and native kinds of the text subset. Opaque signatures,
// binary media, tool bodies and private diagnostic extensions stay private.
type SubagentOutputBlock struct {
	Kind string  `json:"kind"`
	Text *string `json:"text"`
}
type SubagentTool struct {
	NativeID       string  `json:"native_id"`
	Name           string  `json:"name"`
	RequestedModel *string `json:"requested_model,omitempty"`
}
type SubagentTask struct {
	AgentType      *string `json:"agent_type"`
	Description    *string `json:"description"`
	Depth          *uint32 `json:"depth"`
	Backgrounded   *bool   `json:"backgrounded"`
	SkipTranscript *bool   `json:"skip_transcript"`
	Ambient        *bool   `json:"ambient"`
}

type SubagentObservation struct {
	OpenCodeCleanup *OpenCodeStopObservation `json:"opencode_cleanup,omitempty"`
	OpenCodeTool    *OpenCodeParentTool      `json:"opencode_tool,omitempty"`
	Tool            *ClaudeToolReference     `json:"tool,omitempty"`
	Tools           []SubagentTool           `json:"tools,omitempty"`
	Task            *SubagentTask            `json:"task,omitempty"`
	ID              ID                       `json:"id"`
	NativeID        string                   `json:"native_id"`
	ParentID        string                   `json:"parent_id"`
	ParentToolID    string                   `json:"parent_tool_id,omitempty"`
	Source          SubagentSource           `json:"source"`
	SourceID        string                   `json:"source_id"`
	Status          SubagentStatus           `json:"status"`
	RequestedModel  *string                  `json:"requested_model"`
	ObservedModel   *string                  `json:"observed_model"`
	Output          *SubagentOutput          `json:"output"`
	Usage           *SubagentUsage           `json:"usage"`
}

type SubagentRecord struct {
	Sources       []SubagentEvidence  `json:"sources"`
	ExecutionID   ID                  `json:"execution_id"`
	Harness       Harness             `json:"harness"`
	Version       string              `json:"native_version"`
	RootID        string              `json:"root_id"`
	FirstSequence uint64              `json:"first_sequence,string"`
	LastSequence  uint64              `json:"last_sequence,string"`
	Observation   SubagentObservation `json:"observation"`
}
type SubagentEvidence struct {
	Source   SubagentSource `json:"source"`
	SourceID string         `json:"source_id"`
	Sequence uint64         `json:"sequence,string"`
	Usage    *SubagentUsage `json:"usage,omitempty"`
}

// Current execution metadata excludes output, prompts and counters. Immutable
// publication receipts and the retained resource preserve each observation.
type SubagentOwner struct {
	OpenCodeTool *OpenCodeParentTool  `json:"opencode_tool,omitempty"`
	Tool         *ClaudeToolReference `json:"tool,omitempty"`
	Tools        []SubagentTool       `json:"tools,omitempty"`
	ID           ID                   `json:"id"`
	ParentID     string               `json:"parent_id"`
	ParentToolID string               `json:"parent_tool_id,omitempty"`
	Status       SubagentStatus       `json:"status"`
}
type SubagentState map[string]SubagentOwner

func (s SubagentState) Closed() bool {
	for _, child := range s {
		if !child.Status.Terminal() {
			return false
		}
	}
	return true
}

func invalidSubagent() error {
	return Fail(Conflict, "The child observation does not match its original ownership.", "Inspect the original native hierarchy without controlling or replaying child work.")
}

func sameSubagentTool(left, right SubagentTool) bool {
	if left.NativeID != right.NativeID || left.Name != right.Name {
		return false
	}
	if left.RequestedModel == nil || right.RequestedModel == nil {
		return left.RequestedModel == nil && right.RequestedModel == nil
	}
	return *left.RequestedModel == *right.RequestedModel
}

func (o SubagentObservation) Validate() error {
	// Validate the incoming source before retained last-available values are
	// merged. A task/activity report cannot claim absent native telemetry.
	switch o.Source {
	case ClaudeTaskSource:
		if o.Output != nil || o.ObservedModel != nil {
			return invalidSubagent()
		}
	case CodexActivitySource:
		if o.Output != nil || o.ObservedModel != nil || o.RequestedModel != nil {
			return invalidSubagent()
		}
	case CodexCollaborationSource:
		if o.ObservedModel != nil || o.Output != nil && len(o.Output.Blocks) != 0 {
			return invalidSubagent()
		}
	case OpenCodeTaskSource, OpenCodeChildContentSource, OpenCodeChildHistorySource, OpenCodeChildCleanupSource:
		if validateOpenCodeSubagent(o) != nil {
			return invalidSubagent()
		}
	case CodexHistorySource, ClaudeContentSource, ClaudeHistorySource:
		// These sources can supply independently verified output and model.
	default:
		return invalidSubagent()
	}
	if (o.OpenCodeTool != nil || o.OpenCodeCleanup != nil) && o.Source != OpenCodeTaskSource && o.Source != OpenCodeChildContentSource && o.Source != OpenCodeChildHistorySource && o.Source != OpenCodeChildCleanupSource {
		return invalidSubagent()
	}
	if o.Tool != nil && (o.Tool.Validate() != nil || o.Tool.NativeID != o.ParentToolID || o.Tool.Name != "Agent" && o.Tool.Name != "Task") {
		return invalidSubagent()
	}
	if len(o.Tools) > 128 {
		return invalidSubagent()
	}
	seenTools := map[string]bool{}
	for _, tool := range o.Tools {
		if Text(tool.NativeID, "native child tool", 1024, true) != nil || Text(tool.Name, "native child tool name", 256, true) != nil || tool.RequestedModel != nil && Text(*tool.RequestedModel, "native requested model", 256, true) != nil || seenTools[tool.NativeID] {
			return invalidSubagent()
		}
		seenTools[tool.NativeID] = true
	}
	if o.Task != nil {
		for _, value := range []*string{o.Task.AgentType, o.Task.Description} {
			if value != nil && Text(*value, "native child task", MaxMessageText, false) != nil {
				return invalidSubagent()
			}
		}
		if o.Task.Depth != nil && (*o.Task.Depth == 0 || *o.Task.Depth > 128) {
			return invalidSubagent()
		}
	}
	if o.ID.Validate() != nil || o.NativeID == o.ParentID || !slices.Contains([]SubagentStatus{SubagentPending, SubagentRunning, SubagentPaused, SubagentCompleted, SubagentFailed, SubagentInterrupted, SubagentShutdown, SubagentUnavailable}, o.Status) || !slices.Contains([]SubagentSource{CodexCollaborationSource, CodexActivitySource, CodexHistorySource, ClaudeTaskSource, ClaudeContentSource, ClaudeHistorySource, OpenCodeTaskSource, OpenCodeChildContentSource, OpenCodeChildHistorySource, OpenCodeChildCleanupSource}, o.Source) {
		return invalidSubagent()
	}
	for _, value := range []string{o.NativeID, o.ParentID, o.SourceID} {
		if Text(value, "native child identity", 1024, true) != nil {
			return invalidSubagent()
		}
	}
	if Text(o.ParentToolID, "native parent tool", 1024, false) != nil {
		return invalidSubagent()
	}
	for _, model := range []*string{o.RequestedModel, o.ObservedModel} {
		if model != nil && Text(*model, "native model", 256, true) != nil {
			return invalidSubagent()
		}
	}
	if o.Output != nil && (!o.Output.Partial || Text(o.Output.NativeMessageID, "native output identity", 1024, true) != nil || Text(o.Output.Text, "native child output", MaxMessageText, false) != nil) {
		return invalidSubagent()
	}
	if o.Output != nil {
		if len(o.Output.Blocks) > 128 {
			return invalidSubagent()
		}
		for _, block := range o.Output.Blocks {
			if Text(block.Kind, "native block kind", 256, true) != nil || block.Text != nil && Text(*block.Text, "native child text", MaxMessageText, false) != nil {
				return invalidSubagent()
			}
		}
	}
	if u := o.Usage; u != nil {
		if u.validateNative(o.Source) != nil {
			return invalidSubagent()
		}
		for _, count := range []*string{u.Total, u.Input, u.Output} {
			if count == nil {
				continue
			}
			v, err := strconv.ParseUint(*count, 10, 64)
			if err != nil || strconv.FormatUint(v, 10) != *count {
				return invalidSubagent()
			}
		}
	}
	return nil
}

// Validate the complete batch before returning any new ownership. Reparenting,
// cycles, identity reuse and terminal regression cannot partially publish.
func ApplySubagents(prior SubagentState, root string, batch []SubagentObservation) (SubagentState, error) {
	if len(batch) == 0 || len(batch) > 128 {
		return nil, invalidSubagent()
	}
	raw, err := json.Marshal(batch)
	if err != nil || len(raw) > 512<<10 {
		return nil, invalidSubagent()
	}
	next := SubagentState{}
	ids := map[ID]string{}
	for native, owner := range prior {
		next[native] = owner
		ids[owner.ID] = native
	}
	seen := map[string]bool{}
	for _, o := range batch {
		if o.Validate() != nil || o.NativeID == root || seen[o.NativeID] || ids[o.ID] != "" && ids[o.ID] != o.NativeID {
			return nil, invalidSubagent()
		}
		seen[o.NativeID], ids[o.ID] = true, o.NativeID
		if old, ok := next[o.NativeID]; ok {
			if old.ID != o.ID || old.ParentID != o.ParentID || old.ParentToolID != o.ParentToolID || old.Status.Terminal() && o.Status != old.Status && o.Status != SubagentShutdown {
				return nil, invalidSubagent()
			}
			if old.OpenCodeTool != nil && (o.OpenCodeTool == nil || *old.OpenCodeTool != *o.OpenCodeTool) {
				return nil, invalidSubagent()
			}
			if old.Tool != nil && o.Tool != nil && *old.Tool != *o.Tool {
				return nil, invalidSubagent()
			}
		}
		if o.Source != ClaudeContentSource {
			// Only verified child content can introduce a new nested tool. Task
			// and history reports may repeat tools already retained for this
			// child, but cannot add a tool or fill its requested model later.
			for _, tool := range o.Tools {
				retained := false
				for _, prior := range next[o.NativeID].Tools {
					if sameSubagentTool(prior, tool) {
						retained = true
						break
					}
				}
				if !retained {
					return nil, invalidSubagent()
				}
			}
		}
		owner := SubagentOwner{ID: o.ID, ParentID: o.ParentID, ParentToolID: o.ParentToolID, Status: o.Status, Tool: o.Tool, OpenCodeTool: o.OpenCodeTool, Tools: slices.Clone(next[o.NativeID].Tools)}
		if owner.Tool == nil {
			owner.Tool = next[o.NativeID].Tool
		}
		for _, tool := range o.Tools {
			found := false
			for i, prior := range owner.Tools {
				if prior.NativeID == tool.NativeID {
					if prior.Name != tool.Name {
						return nil, invalidSubagent()
					}
					if prior.RequestedModel != nil && (tool.RequestedModel == nil || *prior.RequestedModel != *tool.RequestedModel) {
						return nil, invalidSubagent()
					}
					if prior.RequestedModel == nil {
						owner.Tools[i].RequestedModel = tool.RequestedModel
					}
					found = true
				}
			}
			if !found {
				owner.Tools = append(owner.Tools, tool)
			}
		}
		if len(owner.Tools) > 128 {
			return nil, invalidSubagent()
		}
		next[o.NativeID] = owner
	}
	if len(next) > 1024 {
		return nil, invalidSubagent()
	}
	live := 0
	parentTools := map[string]string{}
	for native, owner := range next {
		if owner.ParentToolID != "" {
			// Claude Agent/Task content identifies its child by the original
			// parent tool. Keep that claim unique even after child completion.
			if prior, claimed := parentTools[owner.ParentToolID]; claimed && prior != native {
				return nil, invalidSubagent()
			}
			parentTools[owner.ParentToolID] = native
		}
		if !owner.Status.Terminal() {
			live++
		}
		visited := map[string]bool{native: true}
		for parent := owner.ParentID; parent != root; {
			p, ok := next[parent]
			if !ok || visited[parent] {
				return nil, invalidSubagent()
			}
			visited[parent], parent = true, p.ParentID
		}
	}
	encoded, err := json.Marshal(next)
	if err != nil || len(encoded) > 512<<10 {
		return nil, invalidSubagent()
	}
	if live > 128 {
		return nil, invalidSubagent()
	}
	return next, nil
}
