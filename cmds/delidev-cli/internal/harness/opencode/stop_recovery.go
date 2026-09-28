package opencode

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const maxStopRecoveryAttempts = 32

// recoverStoppedRuntime is an explicit new cleanup-recovery action, never an
// automatic retry of native abort or of the original cleanup callback. The producer must bind
// reconcileOwned to the original process owner's durable platform journal and
// join its original handle/output drains. PID absence is not cleanup evidence.
func (s *sessionAPI) recoverStoppedRuntime(ctx context.Context, observer *inputObserver, request domain.ID) (StopReceipt, error) {
	if err := s.enter(ctx); err != nil {
		return StopReceipt{}, err
	}
	defer s.leave()
	if observer == nil || observer != s.observer || s.reconcileOwned == nil || s.claim == nil || s.owner.Validate() != nil || s.events == nil {
		return StopReceipt{}, sessionInvalid()
	}
	observer.mu.Lock()
	if observer.stop == nil || !observer.stop.cleanupAttempted {
		observer.mu.Unlock()
		return StopReceipt{}, sessionConflict()
	}
	result, _ := observer.stopReceiptLocked()
	if result.CleanupVerified {
		observer.mu.Unlock()
		if result.RepliesUncertain {
			return result, sessionUncertain()
		}
		return result, nil
	}
	if request.Validate() != nil || request == observer.creation.request || request == observer.input.receipt.RequestID || observer.responseIDs[request] {
		observer.mu.Unlock()
		return result, sessionInvalid()
	}
	if observer.stop.recoveryAttempts >= maxStopRecoveryAttempts {
		observer.mu.Unlock()
		return result, domain.Fail(domain.ResourceExhausted, "OpenCode cleanup recovery reached its attempt limit.", "Retain the original ownership journal for the owning Worker's recovery controller.")
	}
	claim := SessionClaim{RequestID: request, Kind: RecoverStoppedRuntimeMutation, SessionID: result.SessionID, MessageID: result.MessageID, PartID: observer.input.receipt.PartID, InputRequestID: result.InputRequestID, StopRequestID: result.RequestID, BodyDigest: mutationDigest(nil)}
	observer.responseIDs[request] = true
	observer.stop.recoveryAttempts++
	observer.mu.Unlock()
	current := func() StopReceipt { result, _ := observer.stopReceipt(); return result }
	if err := s.claim(ctx, claim); err != nil {
		s.diagnostic(ctx, RecoverStoppedRuntimeMutation, err, "phase", "claim")
		return current(), sessionUncertain()
	}
	if ctx.Err() != nil {
		return current(), sessionUncertain()
	}
	if err := s.reconcileOwned(ctx); err != nil {
		s.diagnostic(ctx, RecoverStoppedRuntimeMutation, err, "phase", "original-owner-recovery")
		return current(), sessionUncertain()
	}
	return s.recordStopCleanup(observer)
}
