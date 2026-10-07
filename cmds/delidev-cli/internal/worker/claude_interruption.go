package worker

import (
	"context"
	"reflect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

type claudePublishedInterruption struct {
	settlement domain.ExecutionClaudeCallbackSettlement
	contextID  domain.ID
	resultID   domain.ID
}

// Called under the original publisher lock. Native session results retain no
// input UUID; the initiating callback is independently bound by its settlement.
func (c *ClaudeContentPublisher) publishInterruption(ctx context.Context, o claude.LifecycleObservation) error {
	b, prior := c.binding, c.interruption
	if prior == nil || prior.resultID != "" || !c.inputPublished || c.active != "" || !c.toolsComplete() || !c.interactionsSettled() || c.resultUsage ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(o.SessionID), o.SessionID != b.journal.SessionID) ||
		o.TurnID != b.turn || c.seen[o.NativeID] || len(c.seen) >= 65536 {
		return b.block()
	}
	s := prior.settlement
	v := domain.ClaudeInterruption{NativeEventID: o.NativeID, InteractionID: s.InteractionID, ArrivalID: s.ArrivalID, ToolMessageID: s.ToolMessageID, ToolResultNativeID: s.ResultNativeID}
	switch o.Kind {
	case claude.ContentObserved:
		if prior.contextID != "" || o.InputID != b.journal.InputID || !o.Accepted || o.Result != nil || len(o.Content) != 1 {
			return b.block()
		}
		n := o.Content[0]
		if len(n.Blocks) != 1 || !reflect.DeepEqual(n, claude.ContentEvent{Kind: claude.NativeCallbackInterruptContext, CallbackArrivalID: s.ArrivalID, Blocks: n.Blocks}) {
			return b.block()
		}
		block, err := claudeDisplayBlock(&n.Blocks[0])
		if err != nil || block.Kind != domain.ClaudeText || block.Text != domain.ClaudeDenialContextText {
			return b.block()
		}
		v.Kind, v.Context = domain.ClaudeDenialContext, &block.Text
	case claude.CallbackInterruptResultObserved:
		r := o.Result
		if prior.contextID == "" || o.InputID != "" || o.Accepted || o.CallbackArrivalID != s.ArrivalID || len(o.Content) != 0 || r == nil || r.Kind != claude.ResultExecutionError || r.Reason != claude.AbortedTools || !r.Error || r.Origin != nil || r.KnownWork != (claude.NativeWorkObservation{}) || r.Usage == nil {
			return b.block()
		}
		v.Kind, v.Result = domain.ClaudeDenialResult, &domain.ClaudeInterruptionResult{Kind: domain.ClaudeInterruptionError, Reason: domain.ClaudeToolsAborted, Error: true}
		if convertClaudeUsage(r.Usage, &v.Result.Usage) != nil {
			return b.block()
		}
	default:
		return b.block()
	}
	u := &domain.ExecutionClaudeInterruption{ID: domain.NewID(), Observation: v}
	if u.Validate() != nil {
		return b.block()
	}
	c.seen[o.NativeID] = true
	c.queue = []claudeContentCommit{{event: domain.ExecutionEvent{Kind: domain.ExecutionClaudeInterruptionObserved, ClaudeInterruption: u}}}
	return c.drain(ctx)
}
