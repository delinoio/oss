package claude

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// FinishOriginalDenial closes the original callback-owned interrupted run after
// its independent session result, cancelled command and idle. Its classification
// is deliberately not a correlated input result or a stopped-history checkpoint.
func (s *APISession) FinishOriginalDenial(ctx context.Context, owner, session, input domain.ID, turn string, arrival domain.ID) (NativeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.status(); err != nil {
		return NativeResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return NativeResult{}, domain.SafeError(err)
	}
	b := s.current
	if s.reading || b == nil || b.problem != nil || s.config.Process.OwnerID != owner || s.config.SessionID != session || b.owner != owner || b.session != session || b.input != input || b.turnID != turn || arrival.Validate() != nil || b.interruptedReply != arrival || !b.accepted || !b.initialized || !b.denialToolResult || !b.denialContext || b.finished || b.terminal != nil || s.interrupt != nil || b.interrupt != nil || b.command != CommandCancelled || b.runState != RunIdle || b.continuationSeen || b.continuing || b.pendingCompaction != nil || s.compaction != nil {
		return NativeResult{}, lifecycleUncertain()
	}
	r := b.interruptResult
	callback := b.interactions[arrival]
	if r == nil || r.Kind != ResultExecutionError || r.Reason != AbortedTools || !r.Error || r.Origin != nil || r.KnownWork != (NativeWorkObservation{}) || callback == nil || !callback.prepared || !callback.echoed || callback.canceled || callback.behavior != PermissionDeny || b.knownWork() != (NativeWorkObservation{}) || b.content.openTools != 0 || len(b.content.active) != 0 || b.interactionBytes != 0 {
		return NativeResult{}, lifecycleUncertain()
	}
	for _, interaction := range b.interactions {
		if !interaction.echoed && !interaction.canceled {
			return NativeResult{}, lifecycleUncertain()
		}
	}
	result := NativeResult{Kind: r.Kind, Reason: r.Reason, Error: r.Error}
	if err := s.finishWithLocked(ctx, s.stream.finishDenial); err != nil {
		return NativeResult{}, err
	}
	if logger := s.config.Process.Logger; logger != nil {
		logger.InfoContext(ctx, "claude_original_denial_cleanup_verified", "owner_id", owner, "input_id", input, "arrival_id", arrival)
	}
	return result, nil
}
