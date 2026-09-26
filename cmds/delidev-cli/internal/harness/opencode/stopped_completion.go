package opencode

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// StoppedHistoryObservation retains native interruption (or normal completion
// racing Stop), exact original history and joined cleanup as separate facts.
// It cannot adopt owner-only Stop, missing observations or a replacement runtime.
type StoppedHistoryObservation struct {
	History HistoryObservation
	Stop    StopReceipt
}

func copyStoppedHistory(value StoppedHistoryObservation) StoppedHistoryObservation {
	value.History = copyHistoryObservation(value.History)
	return value
}

func (a *OwnedAPI) CloseAfterStop(ctx context.Context) (StoppedHistoryObservation, error) {
	if !a.valid() {
		return StoppedHistoryObservation{}, sessionInvalid()
	}
	select {
	case a.reading <- struct{}{}:
		defer func() { <-a.reading }()
	case <-ctx.Done():
		return StoppedHistoryObservation{}, unavailable()
	}
	if ctx.Err() != nil {
		return StoppedHistoryObservation{}, unavailable()
	}
	if a.stoppedCompletion != nil {
		return copyStoppedHistory(*a.stoppedCompletion), nil
	}
	if a.completionAttempted {
		return StoppedHistoryObservation{}, sessionUncertain()
	}
	s := a.session
	if err := s.enter(ctx); err != nil {
		return StoppedHistoryObservation{}, err
	}
	defer s.leave()
	if s.closeOwned == nil || s.owner.Validate() != nil || s.observer == nil || s.events == nil {
		return StoppedHistoryObservation{}, sessionUncertain()
	}
	history, err := s.readHistoryAt(ctx, s.observer, stoppedHistoryBoundary)
	if err != nil {
		return StoppedHistoryObservation{}, err
	}
	// Ordinary Close or separate stop cleanup cannot retroactively supply this
	// original attempt's comparison proof. Failure is latched before cleanup.
	s.observer.mu.Lock()
	ready := s.observer.stoppedHistoryReady()
	if ready {
		s.observer.stop.cleanupAttempted = true
		a.completionAttempted = true
	}
	s.observer.mu.Unlock()
	if !ready {
		return StoppedHistoryObservation{}, sessionUncertain()
	}
	if err := s.closeOwned(ctx); err != nil {
		s.events.Close()
		if s.logger != nil {
			s.logger.WarnContext(ctx, "opencode_stopped_history_cleanup_uncertain", "owner_id", s.owner, "code", domain.SafeError(err).Code)
		}
		return StoppedHistoryObservation{}, sessionUncertain()
	}
	receipt, err := s.recordStopCleanup(s.observer)
	if err != nil || !receipt.CleanupVerified || !receipt.PendingCleared || receipt.RepliesUncertain || !receipt.NativeAttempted || !receipt.TerminalObserved || !receipt.IdleObserved || receipt.InputRequestID != history.RequestID || receipt.SessionID != history.SessionID || receipt.MessageID != history.InputID {
		return StoppedHistoryObservation{}, sessionUncertain()
	}
	value := StoppedHistoryObservation{History: history, Stop: receipt}
	a.stoppedCompletion = &value
	if s.logger != nil {
		s.logger.InfoContext(ctx, "opencode_original_stopped_history_verified", "owner_id", s.owner, "request_id", receipt.RequestID, "interrupted", receipt.InterruptedObserved, "http_accepted", receipt.HTTPAccepted)
	}
	return copyStoppedHistory(value), nil
}
