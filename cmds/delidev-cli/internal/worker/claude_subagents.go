// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func (c *ClaudeContentPublisher) publishChild(ctx context.Context, o domain.SubagentObservation, usageModel *string) error {
	state := domain.SubagentState{}
	for id, child := range c.children {
		state[id] = domain.SubagentOwner{ID: child.ID, ParentID: child.ParentID, ParentToolID: child.ParentToolID, Status: child.Status, Tool: child.Tool, Tools: child.Tools}
	}
	next, err := domain.ApplySubagents(state, string(c.binding.journal.SessionID), []domain.SubagentObservation{o})
	if err != nil {
		return c.binding.block()
	}
	o.Tools = next[o.NativeID].Tools
	var historyDigest *[sha256.Size]byte
	if o.Source == domain.ClaudeHistorySource {
		// The verifier rechecks the file and current sidecar on every read. Only
		// the selected native leaf's projected telemetry belongs to this source;
		// unrelated file/sidecar changes cannot manufacture another receipt.
		raw, err := json.Marshal(struct {
			Output *domain.SubagentOutput
			Model  *string
			Usage  *domain.SubagentUsage
		}{o.Output, o.ObservedModel, o.Usage})
		if err != nil {
			return c.binding.block()
		}
		digest := sha256.Sum256(raw)
		key := claudeChildHistorySource{o.NativeID, o.SourceID, digest}
		if _, exists := c.childHistorySources[key]; exists {
			if logger := c.binding.publisher.config.Logger; logger != nil {
				logger.InfoContext(ctx, "claude_child_history_already_published", "job_id", c.binding.journal.JobID)
			}
			return nil
		}
		if len(c.childHistorySources) >= 65536 {
			return c.binding.block()
		}
		historyDigest = &digest
	}
	c.queue = []claudeContentCommit{{event: domain.ExecutionEvent{Kind: domain.ExecutionSubagentObserved, Subagents: []domain.SubagentObservation{o}}, child: &o, childUsageModel: usageModel, childHistoryDigest: historyDigest}}
	return c.drain(ctx)
}

func (c *ClaudeContentPublisher) publishChildTask(ctx context.Context, o claude.LifecycleObservation) (bool, error) {
	n := o.Task
	child, exists := c.children[n.ID]
	if !exists && (n.Type == nil || *n.Type != claude.LocalAgentTask) {
		return false, nil
	}
	if !exists {
		if n.Kind != claude.TaskStarted || n.ToolID == nil {
			return true, c.binding.block()
		}
		child = domain.SubagentObservation{ID: domain.NewID(), NativeID: n.ID, ParentID: string(c.binding.journal.SessionID), ParentToolID: *n.ToolID, Status: domain.SubagentRunning}
		if tool, ok := c.tools[*n.ToolID]; ok {
			if c.resultUsage {
				return true, c.binding.block()
			}
			if tool.state != domain.MessageStreaming || tool.content == nil || tool.content.Proposal == nil || tool.reference.Name != "Agent" && tool.reference.Name != "Task" {
				return true, c.binding.block()
			}
			child.Tool = cloneClaudeTaskField(&tool.reference)
			var requested map[string]json.RawMessage
			if domain.Decode([]byte(tool.content.Proposal.Applied), &requested) == nil {
				var model *string
				if raw, ok := requested["model"]; ok {
					if json.Unmarshal(raw, &model) != nil {
						return true, c.binding.block()
					}
					child.RequestedModel = model
				}
			}
		} else {
			parent := c.childTools[*n.ToolID]
			if parent == "" {
				return true, c.binding.block()
			}
			child.ParentID = parent
			for _, tool := range c.children[parent].Tools {
				if tool.NativeID == child.ParentToolID {
					child.RequestedModel = cloneClaudeTaskField(tool.RequestedModel)
				}
			}
		}
		child.Task = &domain.SubagentTask{AgentType: cloneClaudeTaskField(n.SubagentType), Description: cloneClaudeTaskField(n.Description), Depth: cloneClaudeTaskField(n.SpawnDepth), Backgrounded: cloneClaudeTaskField(n.Backgrounded), SkipTranscript: cloneClaudeTaskField(n.SkipTranscript), Ambient: cloneClaudeTaskField(n.Ambient)}
	} else if n.Kind == claude.TaskStarted || n.ToolID != nil && *n.ToolID != child.ParentToolID {
		return true, c.binding.block()
	}
	// Copy retained metadata before updating it: an unacknowledged publication
	// must not alter the original child or authorize a transcript read.
	task := cloneClaudeTaskField(child.Task)
	if task == nil {
		return true, c.binding.block()
	}
	if n.SubagentType != nil {
		if task.AgentType != nil && *task.AgentType != *n.SubagentType {
			return true, c.binding.block()
		}
		task.AgentType = cloneClaudeTaskField(n.SubagentType)
	}
	if n.SpawnDepth != nil {
		if task.Depth != nil && *task.Depth != *n.SpawnDepth {
			return true, c.binding.block()
		}
		task.Depth = cloneClaudeTaskField(n.SpawnDepth)
	}
	if n.Description != nil {
		task.Description = cloneClaudeTaskField(n.Description)
	}
	if n.Backgrounded != nil {
		task.Backgrounded = cloneClaudeTaskField(n.Backgrounded)
	}
	if n.SkipTranscript != nil {
		if task.SkipTranscript != nil && *task.SkipTranscript && !*n.SkipTranscript {
			return true, c.binding.block()
		}
		task.SkipTranscript = cloneClaudeTaskField(n.SkipTranscript)
	}
	if n.Ambient != nil {
		task.Ambient = cloneClaudeTaskField(n.Ambient)
	}
	if n.Patch != nil {
		if n.Patch.Description != nil {
			task.Description = cloneClaudeTaskField(n.Patch.Description)
		}
		if n.Patch.Backgrounded != nil {
			task.Backgrounded = cloneClaudeTaskField(n.Patch.Backgrounded)
		}
	}
	child.Task = task
	child.Source, child.SourceID = domain.ClaudeTaskSource, o.NativeID
	child.Usage, child.Output, child.ObservedModel = nil, nil, nil
	if exists {
		child.RequestedModel = nil
	}
	if n.WorkflowName != nil {
		return true, c.binding.block()
	}
	status := n.Status
	if n.Patch != nil && n.Patch.Status != nil {
		status = n.Patch.Status
	}
	if status != nil {
		switch *status {
		case claude.TaskPending:
			child.Status = domain.SubagentPending
		case claude.TaskRunning:
			child.Status = domain.SubagentRunning
		case claude.TaskPaused:
			child.Status = domain.SubagentPaused
		case claude.TaskCompleted:
			child.Status = domain.SubagentCompleted
		case claude.TaskFailed:
			child.Status = domain.SubagentFailed
		case claude.TaskKilled, claude.TaskStopped:
			child.Status = domain.SubagentInterrupted
		default:
			return true, c.binding.block()
		}
	}
	if n.Usage != nil {
		total := strconv.FormatUint(n.Usage.TotalTokens, 10)
		child.Usage = &domain.SubagentUsage{Scope: domain.SubagentCumulativeUsage, Total: &total}
		report, err := json.Marshal(n.Usage)
		if err != nil {
			return true, c.binding.block()
		}
		child.Usage.NativeReport = string(report)
	}
	// Task summaries and output locators are not the child's verified transcript.
	// They cannot supply child content/model or authorize file access.
	return true, c.publishChild(ctx, child, nil)
}

func (c *ClaudeContentPublisher) publishChildContent(ctx context.Context, o claude.LifecycleObservation) (bool, error) {
	if len(o.Content) == 0 || o.Content[0].ParentToolID == "" {
		return false, nil
	}
	b := c.binding
	if !c.inputPublished || o.SessionID != b.journal.SessionID || o.InputID != b.journal.InputID || o.TurnID != b.turn || !o.Accepted || len(o.Content) > 128 || domain.NativeIdentity(o.NativeID).Validate(domain.ClaudeCode, domain.NativeTurnIdentity) != nil || c.seen[o.NativeID] || len(c.seen) >= 65536 {
		return true, b.block()
	}
	parentTool := o.Content[0].ParentToolID
	var child domain.SubagentObservation
	for _, v := range c.children {
		if v.ParentToolID == parentTool {
			child = v
			break
		}
	}
	if child.ID == "" {
		return true, b.block()
	}
	child.Source, child.SourceID = domain.ClaudeContentSource, o.NativeID
	child.Usage, child.Output, child.ObservedModel, child.RequestedModel = nil, nil, nil, nil
	if o.Native != nil && o.Native.Kind == claude.NativeMessage && (o.Native.Type == "user" || o.Native.Type == "assistant") {
		proof, err := claude.ObserveChildHistoryMessage(*o.Native, b.journal.SessionID, parentTool)
		if err != nil {
			return true, b.block()
		}
		if c.childProofs == nil {
			c.childProofs = map[string][]claude.HistoryMessageProof{}
		}
		if len(c.childProofs[child.NativeID]) >= 4096 {
			return true, b.block()
		}
		c.childProofs[child.NativeID] = append(c.childProofs[child.NativeID], proof)
	}
	tools := []domain.SubagentTool{}
	for _, n := range o.Content {
		if n.ParentToolID != parentTool {
			return true, b.block()
		}
		if n.Model != "" {
			child.ObservedModel = cloneClaudeTaskField(&n.Model)
		}
		if n.Usage != nil {
			usage, err := claude.SubagentProviderUsage(n.Usage)
			if err != nil {
				return true, b.block()
			}
			child.Usage = usage
		}
		blocks := n.Blocks
		if n.Block != nil && n.Kind == claude.ContentCompleted {
			blocks = []claude.NativeContentBlock{*n.Block}
		}
		if len(blocks) != 0 && (n.Kind == claude.ProviderMessageSnapshot || n.Kind == claude.ContentCompleted) {
			message := n.MessageID
			if message == "" {
				message = o.NativeID
			}
			output := &domain.SubagentOutput{NativeMessageID: message, Partial: true}
			for _, block := range blocks {
				text := block.Text
				if block.Kind == claude.ThinkingBlock {
					text = block.Thinking
				}
				output.Blocks = append(output.Blocks, domain.SubagentOutputBlock{Kind: string(block.Kind), Text: cloneClaudeTaskField(text)})
				if block.Tool != nil {
					tool := domain.SubagentTool{NativeID: block.Tool.ID, Name: block.Tool.Name}
					if (tool.Name == "Agent" || tool.Name == "Task") && len(block.Tool.Input) != 0 {
						var proposal map[string]json.RawMessage
						if domain.Decode(block.Tool.Input, &proposal) != nil {
							return true, b.block()
						}
						if raw, ok := proposal["model"]; ok && json.Unmarshal(raw, &tool.RequestedModel) != nil {
							return true, b.block()
						}
					}
					tools = append(tools, tool)
				}
			}
			child.Output = output
		}
		// Forwarded user/context and tool-result messages are history proofs,
		// not assistant output. They cannot populate the recent-output field.
	}
	// All sibling references validate before installing any nested ownership.
	for _, tool := range tools {
		if prior := c.childTools[tool.NativeID]; prior != "" && prior != child.NativeID {
			return true, b.block()
		}
	}
	if len(c.childTools)+len(tools) > 4096 {
		return true, b.block()
	}
	if len(tools) > 0 {
		child.Tools = tools
	}
	c.seen[o.NativeID] = true
	var usageModel *string
	if len(o.Content) == 1 && o.Content[0].Usage != nil {
		// Preserve this report's model presence independently of the child's
		// last available observed model. Commit it only with its exact receipt.
		model := o.Content[0].Model
		usageModel = &model
	}
	if err := c.publishChild(ctx, child, usageModel); err != nil {
		return true, err
	}
	return true, nil
}

func (c *ClaudeContentPublisher) PublishChildHistory(ctx context.Context, config claude.APIStreamConfig) error {
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := c.verify(); err != nil {
		return err
	}
	for _, child := range c.children {
		if !child.Status.Terminal() || child.Task == nil || child.Task.AgentType == nil || child.Task.Description == nil || child.Task.Depth == nil || child.Task.SkipTranscript != nil && *child.Task.SkipTranscript {
			continue
		}
		parent := ""
		if child.ParentID != string(b.journal.SessionID) {
			parent = child.ParentID
		}
		observed, err := claude.ReadChildTranscript(ctx, config.Home, b.journal.SessionID, config.Workspace, claude.ChildHistoryBinding{TaskID: child.NativeID, ToolID: child.ParentToolID, ParentAgentID: parent, AgentType: *child.Task.AgentType, Description: *child.Task.Description, SpawnDepth: *child.Task.Depth}, c.childProofs[child.NativeID], config.Process.Logger)
		if err != nil {
			if ctx.Err() != nil {
				return domain.SafeError(ctx.Err())
			}
			continue
		}
		child.Source, child.SourceID = domain.ClaudeHistorySource, observed.Transcript.LeafID
		child.Output, child.ObservedModel = observed.Output, observed.ObservedModel
		child.Usage = observed.Usage
		if err := c.publishChild(ctx, child, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *ClaudeContentPublisher) childrenClosed() bool {
	for _, child := range c.children {
		if !child.Status.Terminal() {
			return false
		}
	}
	return true
}
