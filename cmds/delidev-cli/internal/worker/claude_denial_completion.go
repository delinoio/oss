package worker

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

type claudeDenialController interface {
	FinishOriginalDenial(context.Context, domain.ID, domain.ID, domain.ID, string, domain.ID) (claude.NativeResult, error)
}

// Called under the original publisher lock. Command and idle preserve their
// independent identities; neither event fills in the missing native result ID.
func (c *ClaudeContentPublisher) observeDenialBoundary(o claude.LifecycleObservation, command bool) error {
	b, p := c.binding, c.interruption
	if p == nil || p.contextID == "" || p.resultID == "" || !c.inputPublished || !c.resultUsage || c.active != "" || !c.toolsComplete() || !c.interactionsSettled() || c.denial != nil ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(o.SessionID), o.SessionID != b.journal.SessionID) ||
		o.TurnID != b.turn || domain.NativeIdentity(o.NativeID).Validate(domain.ClaudeCode, domain.NativeTurnIdentity) != nil || o.NativeID == b.turn || o.NativeID == string(b.journal.InputID) || o.NativeID == string(b.journal.SessionID) || c.seen[o.NativeID] || b.progressSeen[o.NativeID] || c.usageSeen[o.NativeID] || len(c.seen) >= 65536 || o.Result != nil || len(o.Content) != 0 || o.Progress != nil || o.Interaction != nil {
		return b.block()
	}
	if command {
		if o.Command != claude.CommandCancelled || o.InputID != b.journal.InputID || !o.Accepted || o.Run != nil || c.terminalCommand != "" {
			return b.block()
		}
		c.terminalCommand, c.terminalCommandID = domain.ClaudeCommandCancelled, o.NativeID
	} else {
		if o.Kind != claude.RunStateObserved || o.InputID != "" || o.Accepted || o.Command != "" || o.Run == nil || o.Run.State != claude.RunIdle || o.Run.KnownWork != (claude.NativeWorkObservation{}) || o.Run.ContinuationFailed || c.terminalCommand != domain.ClaudeCommandCancelled {
			return b.block()
		}
		v := &domain.ClaudeDenialCompletion{InputID: b.journal.InputID, InteractionID: p.settlement.InteractionID, ArrivalID: p.settlement.ArrivalID, ContextID: p.contextID, ResultID: p.resultID, CommandNativeID: c.terminalCommandID, IdleNativeID: o.NativeID, CleanupVerified: true}
		if v.Validate() != nil {
			return b.block()
		}
		v.CleanupVerified = false
		c.denial = v
	}
	c.seen[o.NativeID] = true
	return nil
}

func (c *ClaudeContentPublisher) CompleteDenial(ctx context.Context, api claudeDenialController) (domain.ExecutionCompletion, error) {
	if c == nil || c.binding == nil || api == nil {
		return domain.ExecutionCompletion{}, publicationUncertain()
	}
	c.binding.mu.Lock()
	defer c.binding.mu.Unlock()
	return c.completeDenialLocked(ctx, api)
}

func (c *ClaudeContentPublisher) completeDenialLocked(ctx context.Context, api claudeDenialController) (domain.ExecutionCompletion, error) {
	b, v := c.binding, c.denial
	if api == nil || b.verify() != nil || v == nil || c.interruption == nil || c.pending || len(c.queue) != 0 || c.stop != nil || c.terminal != nil {
		return domain.ExecutionCompletion{}, publicationUncertain()
	}
	if !v.CleanupVerified {
		if c.verify() != nil {
			return domain.ExecutionCompletion{}, publicationUncertain()
		}
		result, err := api.FinishOriginalDenial(ctx, b.journal.JobID, b.journal.SessionID, b.journal.InputID, b.turn, v.ArrivalID)
		if err != nil || result.Kind != claude.ResultExecutionError || result.Reason != claude.AbortedTools || !result.Error || result.Usage != nil || result.Origin != nil || result.KnownWork != (claude.NativeWorkObservation{}) || b.verify() != nil {
			return domain.ExecutionCompletion{}, b.block()
		}
		v.CleanupVerified = true
		if v.Validate() != nil {
			return domain.ExecutionCompletion{}, b.block()
		}
		c.queue = []claudeContentCommit{{event: domain.ExecutionEvent{Kind: domain.ExecutionTurnFinished, Outcome: domain.ExecutionStopped, ClaudeDenial: v}}}
		if err := c.drain(ctx); err != nil {
			return domain.ExecutionCompletion{}, err
		}
	}
	// The original native cleanup is never repeated after publication ack loss.
	if b.stage != claudeTerminalPublished || c.terminalSequence != b.sequence {
		return domain.ExecutionCompletion{}, publicationUncertain()
	}
	if c.completion != nil {
		return *c.completion, nil
	}
	completion := domain.ExecutionCompletion{Version: 1, ExecutionID: b.journal.ExecutionID, InputID: b.journal.InputID, NativeThreadID: domain.NativeIdentity(b.journal.SessionID), NativeTurnID: domain.NativeIdentity(b.turn), LastSequence: c.terminalSequence, Outcome: domain.ExecutionStopped, CleanupVerified: true}
	if completion.ValidateForHarness(domain.ClaudeCode) != nil {
		return domain.ExecutionCompletion{}, b.block()
	}
	c.completion = &completion
	return completion, nil
}
