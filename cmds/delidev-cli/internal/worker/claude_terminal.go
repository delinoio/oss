package worker

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

// PublishBoundaryObservation joins separately observed command closure and
// native idle to the already acknowledged original input-result usage record.
// Idle itself has no product input ID; never fill one into the native event.
func (c *ClaudeContentPublisher) PublishBoundaryObservation(ctx context.Context, o claude.LifecycleObservation) (bool, error) {
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := c.verify(); err != nil {
		return false, err
	}
	command := o.Kind == claude.CommandObserved && (o.Command == claude.CommandCompleted || o.Command == claude.CommandCancelled)
	idle := o.Kind == claude.RunStateObserved && o.Run != nil && o.Run.State == claude.RunIdle
	if !command && !idle {
		return false, nil
	}
	if c.interruption != nil {
		return true, c.observeDenialBoundary(o, command)
	}
	if !c.inputPublished ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(o.SessionID), o.SessionID != b.journal.SessionID) ||
		o.TurnID != b.turn || domain.NativeIdentity(o.NativeID).Validate(domain.ClaudeCode, domain.NativeTurnIdentity) != nil || o.NativeID == b.turn || o.NativeID == string(b.journal.InputID) || c.seen[o.NativeID] || b.progressSeen[o.NativeID] || c.usageSeen[o.NativeID] || len(c.seen) >= 65536 {
		return true, b.block()
	}
	if command {
		if o.InputID != b.journal.InputID || !o.Accepted || c.terminalCommand != "" || o.Run != nil || o.Result != nil || len(o.Content) != 0 {
			return true, b.block()
		}
		c.terminalCommand, c.terminalCommandID = domain.ClaudeCommandCompletion(o.Command), o.NativeID
		c.seen[o.NativeID] = true
		return true, nil
	}
	r := c.resultBoundary
	if o.InputID != "" || o.Accepted || o.Command != "" || o.Result != nil || len(o.Content) != 0 || o.Run.KnownWork != (claude.NativeWorkObservation{}) || o.Run.ContinuationFailed || r == nil || r.KnownWork != (claude.NativeWorkObservation{}) || r.Origin != nil || !c.resultUsage || c.resultUsageNativeID == "" || c.active != "" || !c.toolsComplete() || !c.tasks.Closed() || c.pendingTerminal != nil || !c.interactionsSettled() || !b.compaction.Closed() {
		return true, b.block()
	}
	v := &domain.ClaudeTerminalObservation{InputID: b.journal.InputID, ResultNativeID: c.resultUsageNativeID, CommandNativeID: c.terminalCommandID, IdleNativeID: o.NativeID, Kind: domain.ClaudeResultKind(r.Kind), Reason: domain.ClaudeTerminalReason(r.Reason), Error: r.Error, Command: c.terminalCommand}
	if v.Validate() != nil {
		return true, b.block()
	}
	c.seen[o.NativeID] = true
	// Keep the original result/command/idle proof while owned children settle.
	// Root idle alone cannot release their process or cleanup obligations.
	c.pendingTerminal = v
	if !c.childrenClosed() && b.publisher.config.Logger != nil {
		b.publisher.config.Logger.InfoContext(ctx, "claude_original_terminal_deferred", "job_id", b.journal.JobID, "child_count", len(c.children))
	}
	return true, c.publishPendingBoundary(ctx)
}

func (c *ClaudeContentPublisher) pendingBoundaryReady() bool {
	c.binding.mu.Lock()
	defer c.binding.mu.Unlock()
	return c.pendingTerminal != nil && c.childrenClosed()
}

func (c *ClaudeContentPublisher) terminalPublished() bool {
	c.binding.mu.Lock()
	defer c.binding.mu.Unlock()
	return c.binding.stage == claudeTerminalPublished
}

// PublishPendingBoundary is called after late child observations and their final
// read-only history inspection, preserving the first original idle identity.
func (c *ClaudeContentPublisher) PublishPendingBoundary(ctx context.Context) (bool, error) {
	c.binding.mu.Lock()
	defer c.binding.mu.Unlock()
	if c.pendingTerminal == nil {
		return false, nil
	}
	if err := c.verify(); err != nil {
		return true, err
	}
	return true, c.publishPendingBoundary(ctx)
}

func (c *ClaudeContentPublisher) publishPendingBoundary(ctx context.Context) error {
	if !c.childrenClosed() {
		return nil
	}
	v := c.pendingTerminal
	c.queue = []claudeContentCommit{{event: domain.ExecutionEvent{Kind: domain.ExecutionTurnFinished, Outcome: v.Outcome(), ClaudeTerminal: v}}}
	return c.drain(ctx)
}

// Complete joins clean EOF on the original live controller only after terminal
// publication. The owner still must complete its workspace lease and durably
// report this version-1 result; no checkpoint or continuation is implied.
func (c *ClaudeContentPublisher) Complete(ctx context.Context, api *claude.APISession) (domain.ExecutionCompletion, error) {
	if c == nil || c.binding == nil || api == nil {
		return domain.ExecutionCompletion{}, publicationUncertain()
	}
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if c.interruption != nil {
		return c.completeDenialLocked(ctx, api)
	}
	if b.verify() != nil || b.stage != claudeTerminalPublished || c.terminal == nil || c.terminalSequence != b.sequence || c.pending || len(c.queue) != 0 {
		return domain.ExecutionCompletion{}, publicationUncertain()
	}
	if c.completion != nil {
		return *c.completion, nil
	}
	r, err := api.FinishOriginalInput(ctx, b.journal.JobID, b.journal.SessionID, b.journal.InputID, b.turn)
	if err != nil {
		return domain.ExecutionCompletion{}, b.block()
	}
	if domain.ClaudeResultKind(r.Kind) != c.terminal.Kind || domain.ClaudeTerminalReason(r.Reason) != c.terminal.Reason || r.Error != c.terminal.Error || b.verify() != nil {
		return domain.ExecutionCompletion{}, b.block()
	}
	v := domain.ExecutionCompletion{Version: 1, ExecutionID: b.journal.ExecutionID, InputID: b.journal.InputID, NativeThreadID: domain.NativeIdentity(b.journal.SessionID), NativeTurnID: domain.NativeIdentity(b.turn), LastSequence: c.terminalSequence, Outcome: c.terminal.Outcome(), CleanupVerified: true}
	if v.ValidateForHarness(domain.ClaudeCode) != nil {
		return domain.ExecutionCompletion{}, b.block()
	}
	c.completion = &v
	if logger := b.publisher.config.Logger; logger != nil {
		logger.InfoContext(ctx, "claude_original_completion_ready", "job_id", b.journal.JobID, "execution_id", b.journal.ExecutionID, "sequence", v.LastSequence, "outcome", v.Outcome)
	}
	return v, nil
}
