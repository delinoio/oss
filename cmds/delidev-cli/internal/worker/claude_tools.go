package worker

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

type claudePublishedTool struct {
	state                domain.MessageState
	content              *domain.ClaudeToolContent
	readContinuation     bool
	questionContinuation bool
}

func claudeDirectCaller(value claude.NativeToolCaller) (*domain.ClaudeToolCallerKind, error) {
	if value.ToolID != "" || value.Kind != "" && value.Kind != claude.DirectCaller {
		return nil, publicationUncertain()
	}
	if value.Kind == "" {
		return nil, nil
	}
	caller := domain.ClaudeDirectToolCaller
	return &caller, nil
}
func (c *ClaudeContentPublisher) prepareToolProposal(message domain.ID, native claude.ContentEvent) (*domain.ClaudeToolUpdate, claudePublishedTool, error) {
	fail := func() (*domain.ClaudeToolUpdate, claudePublishedTool, error) {
		return nil, claudePublishedTool{}, publicationUncertain()
	}
	block := native.Block
	if block == nil || block.Kind != claude.ToolUseBlock || block.Tool == nil || block.Text != nil || block.Thinking != nil || block.Citations != nil || block.Media != nil || block.ServerTool != nil || block.ServerResult != nil || native.Index == nil {
		return fail()
	}
	tool := block.Tool
	caller, err := claudeDirectCaller(tool.CalledBy)
	if err != nil {
		return fail()
	}
	previous, exists := c.tools[tool.ID]
	u := domain.ClaudeToolUpdate{MessageID: message, NativeMessageID: native.MessageID, Index: *native.Index, Caller: caller}
	if native.Kind == claude.ContentStarted {
		if exists || len(c.tools) >= 4096 {
			return fail()
		}
		open := 0
		for _, value := range c.tools {
			if value.state != domain.MessageComplete {
				open++
			}
		}
		if open >= 128 || len(tool.ProposedInput) != 0 {
			return fail()
		}
		initial := string(tool.Input)
		u.Mutation, u.Reference, u.InitialInput = domain.ClaudeToolStart, domain.ClaudeToolReference{ID: domain.NewID(), NativeID: tool.ID, Name: tool.Name}, &initial
	} else {
		if !exists || previous.state != domain.MessageStreaming || previous.content == nil || native.Kind != claude.ContentCompleted || tool.Name != previous.content.Reference.Name {
			return fail()
		}
		u.Mutation, u.Reference, u.Proposal = domain.ClaudeToolProposalComplete, previous.content.Reference, &domain.ClaudeToolProposal{Proposed: string(tool.ProposedInput), Applied: string(tool.Input)}
	}
	next, state, err := domain.ApplyClaudeTool(previous.content, previous.state, u)
	if err != nil {
		return fail()
	}
	return &u, claudePublishedTool{state: state, content: next}, nil
}
func (c *ClaudeContentPublisher) prepareToolInput(reference domain.ClaudeToolReference, native claude.ContentEvent) (*domain.ClaudeToolUpdate, claudePublishedTool, error) {
	prior, ok := c.tools[reference.NativeID]
	if !ok || prior.state != domain.MessageStreaming || prior.content == nil || native.Index == nil || native.Delta == nil {
		return nil, claudePublishedTool{}, publicationUncertain()
	}
	p := prior.content
	u := domain.ClaudeToolUpdate{Mutation: domain.ClaudeToolInputAppend, Reference: reference, MessageID: p.MessageID, NativeMessageID: native.MessageID, Index: *native.Index, Caller: p.Caller, Delta: native.Delta}
	next, state, err := domain.ApplyClaudeTool(p, prior.state, u)
	if err != nil {
		return nil, claudePublishedTool{}, err
	}
	return &u, claudePublishedTool{state: state, content: next}, nil
}

// One native user envelope can contain several original tool results. Validate
// the entire batch before queuing any publication, then retain each exact receipt.
func (c *ClaudeContentPublisher) publishToolResults(ctx context.Context, o claude.LifecycleObservation) error {
	b := c.binding
	queue := make([]claudeContentCommit, 0, len(o.Content))
	seen := map[string]bool{}
	for _, native := range o.Content {
		result := native.ToolResult
		if native.Kind != claude.ToolResultObserved || native.ParentToolID != "" || native.Index == nil || result == nil || native.Model != "" && native.Model != b.publisher.input.Configuration.NativeModel {
			return b.block()
		}
		prior, ok := c.tools[result.ID]
		parent, parentExists := c.messages[native.MessageID]
		if !ok || prior.state != domain.MessageStreaming || prior.content == nil || !parentExists || parent.state != domain.MessageComplete || seen[result.ID] || result.Name != prior.content.Reference.Name {
			return b.block()
		}
		seen[result.ID] = true
		caller, err := claudeDirectCaller(result.CalledBy)
		if err != nil {
			return b.block()
		}
		value := domain.ClaudeToolResult{NativeEventID: o.NativeID, Error: result.Error, Text: result.Text, NonExecution: result.NonExecution}
		if len(result.Structured) != 0 {
			raw := string(result.Structured)
			value.Structured = &raw
		}
		if result.Blocks != nil {
			value.Blocks = make([]domain.ClaudeTextBlock, 0, len(result.Blocks))
			for _, block := range result.Blocks {
				display, err := claudeDisplayBlock(&block)
				if err != nil || display.Kind != domain.ClaudeText {
					return b.block()
				}
				value.Blocks = append(value.Blocks, display)
			}
		}
		p := prior.content
		update := domain.ClaudeToolUpdate{Mutation: domain.ClaudeToolResultObserved, Reference: p.Reference, MessageID: parent.id, NativeMessageID: native.MessageID, Index: *native.Index, Caller: caller, Result: &value}
		next, state, err := domain.ApplyClaudeTool(p, prior.state, update)
		if err != nil {
			return b.block()
		}
		queue = append(queue, claudeContentCommit{event: domain.ExecutionEvent{Kind: domain.ExecutionClaudeToolObserved, ClaudeTool: &update}, toolNative: result.ID, toolNext: claudePublishedTool{state: state, content: next}})
		for arrival, interaction := range c.interactions {
			if interaction.closed || interaction.update.NativeItemID != result.ID {
				continue
			}
			attempt := c.responses[arrival]
			if attempt == nil || !attempt.echoed {
				return b.block()
			}
			original := domain.ExecutionInteraction{Claude: interaction.update.Claude, Type: interaction.update.Type, NativeRequestID: interaction.update.NativeRequestID, NativeItemID: interaction.update.NativeItemID}
			evidence, err := domain.ClaudeCallbackResultEvidence(original, attempt.input, *next)
			if err != nil {
				return b.block()
			}
			u := &domain.ExecutionClaudeCallbackSettlement{ExecutionClaudeReplyEcho: domain.ExecutionClaudeReplyEcho{InteractionID: interaction.update.ID, ResponseID: attempt.journal.Control.ResponseID, ClaimID: attempt.journal.ClaimID, ArrivalID: arrival, NativeItemID: result.ID, BodyDigest: attempt.journal.Native.BodyDigest}, ToolMessageID: p.Reference.ID, ResultNativeID: o.NativeID, Evidence: evidence}
			if u.Validate() != nil {
				return b.block()
			}
			interaction.closed = true
			queue = append(queue, claudeContentCommit{event: domain.ExecutionEvent{Kind: domain.ExecutionClaudeCallbackSettled, ClaudeSettlement: u}, interactionArrival: arrival, interactionNext: interaction})
		}
	}
	c.seen[o.NativeID] = true
	c.queue = queue
	return c.drain(ctx)
}
func (c *ClaudeContentPublisher) toolsComplete() bool {
	for _, tool := range c.tools {
		if tool.state != domain.MessageComplete {
			return false
		}
	}
	return true
}
