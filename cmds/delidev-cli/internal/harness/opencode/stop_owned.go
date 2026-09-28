package opencode

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// claimOwnedStop handles scheduling uncertainty and lost native observation
// without borrowing native abort authority. It records a separate original
// owner-cleanup intent; closeStoppedRuntime must still join that exact process
// scope before cleanup is confirmed. No HTTP read/write, native reconnect,
// answer, history reconstruction or event-gap recovery occurs here.
func (s *sessionAPI) claimOwnedStop(ctx context.Context, observer *inputObserver, request domain.ID) (StopReceipt, error) {
	if err := s.enter(ctx); err != nil {
		return StopReceipt{}, err
	}
	defer s.leave()
	if observer == nil || observer != s.observer || s.creation == nil || s.input == nil || s.claim == nil || s.closeOwned == nil || s.events == nil || s.owner.Validate() != nil {
		return StopReceipt{}, sessionInvalid()
	}
	observer.mu.Lock()
	if observer.stop != nil {
		observer.mu.Unlock()
		return StopReceipt{}, sessionConflict()
	}
	original := observer.input.receipt
	current := s.input.receipt
	if request.Validate() != nil || request == s.creation.request || request == current.RequestID || observer.responseIDs[request] || observer.creation.request != s.creation.request || observer.creation.identity != s.creation.identity || original.RequestID != current.RequestID || original.SessionID != current.SessionID || original.MessageID != current.MessageID || original.PartID != current.PartID || observer.input.digest != s.input.digest {
		observer.mu.Unlock()
		return StopReceipt{}, sessionInvalid()
	}
	attempt := &inputStopAttempt{
		receipt: StopReceipt{RequestID: request, InputRequestID: current.RequestID, SessionID: current.SessionID, MessageID: current.MessageID},
		claim:   SessionClaim{RequestID: request, Kind: StopOwnedRuntimeMutation, SessionID: current.SessionID, MessageID: current.MessageID, PartID: current.PartID, InputRequestID: current.RequestID, BodyDigest: mutationDigest(nil)},
	}
	observer.stop = attempt
	observer.responseIDs[request] = true
	observer.mu.Unlock()
	// The original event stream may already be canceled. Its lifetime cannot
	// cancel the user's separate owned-cleanup claim or grant a native retry.
	if err := s.claim(ctx, attempt.claim); err != nil {
		s.diagnostic(ctx, StopOwnedRuntimeMutation, err)
		result, _ := observer.stopReceipt()
		return result, sessionUncertain()
	}
	return observer.stopReceipt()
}
