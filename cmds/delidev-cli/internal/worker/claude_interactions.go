package worker

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

type claudePublishedInteraction struct {
	update       domain.ExecutionInteractionUpdate
	closed       bool
	continuation domain.ClaudeCallbackEvidence
	bytes        int
}

// Callback arrival and native request IDs retain separate namespaces. A native
// cancellation is not a reply echo; neither observation sends a response.
func (c *ClaudeContentPublisher) PublishInteractionObservation(ctx context.Context, o claude.LifecycleObservation) (bool, error) {
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := c.verify(); err != nil {
		return false, err
	}
	if o.Kind != claude.InteractionObserved {
		return false, nil
	}
	n := o.Interaction
	if c.resultUsage || !c.inputPublished || n == nil ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(o.SessionID), o.SessionID != b.journal.SessionID) ||
		o.InputID != b.journal.InputID || o.TurnID != b.turn || !o.Accepted || n.InputID != o.InputID || n.TurnID != o.TurnID || n.ArrivalID.Validate() != nil {
		return true, b.block()
	}
	if n.Kind == claude.InteractionReplyEchoed {
		return true, c.publishReplyEcho(ctx, n)
	}
	if n.ReplyDigest != "" {
		return true, b.block()
	}
	prior, exists := c.interactions[n.ArrivalID]
	var next claudePublishedInteraction
	kind := domain.ExecutionInteractionRequested
	switch n.Kind {
	case claude.InteractionRequested:
		r := n.Request
		if exists || n.Canceled || r == nil || r.ArrivalID != n.ArrivalID || r.ParentToolID != "" || c.callbackRequests[r.RequestID] || len(c.interactions) >= domain.MaxExecutionInteractions {
			return true, b.block()
		}
		retained, ok := c.tools[r.ToolID]
		if !ok || retained.state != domain.MessageStreaming || retained.content == nil || retained.content.Proposal == nil || retained.content.Reference.Name != r.ToolName {
			return true, b.block()
		}
		tool := retained.content
		caller, err := claudeDirectCaller(r.CalledBy)
		if err != nil || (caller == nil) != (tool.Caller == nil) || caller != nil && *caller != *tool.Caller || !domain.EqualClaudeToolInput(tool.Proposal.Applied, string(r.Input)) {
			return true, b.block()
		}
		open, bytes := 0, 0
		for _, value := range c.interactions {
			if !value.closed {
				if value.update.NativeItemID == r.ToolID {
					return true, b.block()
				}
				open++
				bytes += value.bytes
			}
		}
		if open >= domain.MaxOpenInteractions {
			return true, b.block()
		}
		metadata := domain.ClaudeCallbackMetadata{BlockedPath: r.BlockedPath, DecisionReason: r.DecisionReason, DecisionReasonType: r.DecisionReasonType, RequiresUserInteraction: r.RequiresUserInteraction, AgentID: r.AgentID, Title: r.Title, DisplayName: r.DisplayName, Description: r.Description}
		suggestions, err := json.Marshal(r.Suggestions)
		if err != nil || domain.Decode(suggestions, &metadata.Suggestions) != nil {
			return true, b.block()
		}
		request := &domain.ClaudeInteractionRequest{Version: b.publisher.NativeVersion(), Kind: domain.ClaudeInteractionKind(r.Kind), ArrivalID: r.ArrivalID, Tool: tool.Reference, MessageID: tool.MessageID, NativeMessageID: tool.NativeMessageID, Index: tool.Index, Caller: caller, InputJSON: string(r.Input), Metadata: metadata}
		u := domain.ExecutionInteractionUpdate{ID: domain.NewID(), NativeItemID: r.ToolID, NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: r.RequestID}, Type: domain.NativeApprovalInteraction, Claude: request}
		if r.Kind == claude.UserQuestion {
			u.Type = domain.UserQuestionInteraction
		}
		if u.Validate(kind) != nil {
			return true, b.block()
		}
		raw, err := json.Marshal(u)
		if err != nil || len(raw) > domain.MaxOpenInteractionBytes-bytes || domain.Decode(raw, &next.update) != nil {
			return true, b.block()
		}
		next.bytes = len(raw)
	case claude.InteractionCanceled:
		if !exists || prior.closed || !n.Canceled || n.Request != nil {
			return true, b.block()
		}
		next = prior
		next.closed = true
		next.update.Claude = nil
		next.update.Closure = domain.InteractionNativeClosed
		next.update.ClaudeCancellation = &domain.ClaudeInteractionCancellation{ArrivalID: n.ArrivalID}
		kind = domain.ExecutionInteractionClosed
	default:
		return true, b.block()
	}
	c.queue = []claudeContentCommit{{event: domain.ExecutionEvent{Kind: kind, Interaction: &next.update}, interactionArrival: n.ArrivalID, interactionNext: next}}
	return true, c.drain(ctx)
}

func (c *ClaudeContentPublisher) interactionsSettled() bool {
	for _, value := range c.interactions {
		if !value.closed {
			return false
		}
	}
	return true
}
