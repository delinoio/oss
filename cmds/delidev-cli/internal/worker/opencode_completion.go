package worker

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

// Complete closes only the original acknowledged input's native process scope.
// The returned version-1 report contains no continuation checkpoint and cannot
// enable Resume. The owning Worker must retain/report it through its existing
// durable job journal after separately completing its workspace lease.
func (c *OpenCodeEventPublisher) Complete(ctx context.Context) (domain.ExecutionCompletion, error) {
	if c == nil || c.text == nil || c.usage == nil || c.api == nil {
		return domain.ExecutionCompletion{}, publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fail := func(err error) (domain.ExecutionCompletion, error) { return domain.ExecutionCompletion{}, c.fail(err) }
	if c.blocked || !c.finished {
		return domain.ExecutionCompletion{}, publicationUncertain()
	}
	b := c.text.binding
	b.mu.Lock()
	claims, claimErr := b.readClaims()
	sequence, sequenceErr := b.publisher.acknowledgedSequence()
	valid := !c.text.blocked && !c.usage.blocked && b.stage == openCodeAccepted && claimErr == nil && b.validPublicationClaims(claims) && sequenceErr == nil && sequence == c.terminalSequence
	b.mu.Unlock()
	if !valid {
		return fail(publicationUncertain())
	}
	if c.completion != nil {
		return *c.completion, nil
	}
	var history opencode.HistoryObservation
	var err error
	if c.stopRequest != "" {
		if c.stopped == nil || c.stopObservation == nil || c.stopObservation.Validate() != nil || c.stopped.Stop.RequestID != c.stopRequest || domain.OwnershipBlocks(domain.OwnershipCleanup, b.reference.ExecutionID, !c.stopped.Stop.CleanupVerified) {
			return fail(publicationUncertain())
		}
		history = c.stopped.History
	} else {
		history, err = c.api.CloseCompleted(ctx)
	}
	if err != nil {
		return fail(err)
	}
	if history.RequestID != b.reference.InputRequestID ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(history.SessionID), history.SessionID != b.thread) ||
		history.InputID != b.turn || history.AssistantID != c.final || !c.completeHistory(history) {
		return fail(publicationUncertain())
	}
	// Recheck retained authority after potentially slow owned process cleanup.
	b.mu.Lock()
	claims, claimErr = b.readClaims()
	sequence, sequenceErr = b.publisher.acknowledgedSequence()
	valid = !c.text.blocked && !c.usage.blocked && b.stage == openCodeAccepted && claimErr == nil && b.validPublicationClaims(claims) && sequenceErr == nil && sequence == c.terminalSequence
	b.mu.Unlock()
	if !valid {
		return fail(publicationUncertain())
	}
	cleanupVerified := true
	if c.stopRequest != "" {
		cleanupVerified = c.stopped.Stop.CleanupVerified
	}
	value := domain.ExecutionCompletion{Version: 1, ExecutionID: b.reference.ExecutionID, InputID: b.reference.InputID, NativeThreadID: domain.NativeIdentity(b.thread), NativeTurnID: domain.NativeIdentity(b.turn), LastSequence: c.terminalSequence, Outcome: c.terminalOutcome, CleanupVerified: cleanupVerified}
	if value.ValidateForHarness(domain.OpenCode) != nil {
		return fail(publicationUncertain())
	}
	c.completion = &value
	if b.publisher.config.Logger != nil {
		b.publisher.config.Logger.InfoContext(ctx, "opencode_original_completion_ready", "job_id", b.reference.JobID, "execution_id", b.reference.ExecutionID, "sequence", sequence, "outcome", value.Outcome)
	}
	return value, nil
}
