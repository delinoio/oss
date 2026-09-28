package opencode

import (
	"context"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// StoppedHistoryObservation retains native interruption (or normal completion
// racing Stop), exact original history and joined cleanup as separate facts.
// It cannot adopt owner-only Stop, missing observations or a replacement runtime.
type StoppedHistoryObservation struct {
	History  HistoryObservation
	Stop     StopReceipt
	Canceled []StoppedInteractionObservation
	Retries  []domain.OpenCodeStopRetryObservation
}

// These original requests were still unanswered at the independently observed
// interrupted tool boundary. They become canceled only after owned cleanup.
type StoppedInteractionObservation struct {
	RequestID string
	ArrivalID string
	Kind      InteractionKind
	MessageID string
	PartID    string
	CallID    string
}

func copyStoppedHistory(value StoppedHistoryObservation) StoppedHistoryObservation {
	value.History = copyHistoryObservation(value.History)
	value.Canceled = slices.Clone(value.Canceled)
	value.Retries = slices.Clone(value.Retries)
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
	canceled := []StoppedInteractionObservation{}
	var retries []domain.OpenCodeStopRetryObservation
	if ready {
		retries = slices.Clone(s.observer.stop.retries)
		for _, interaction := range s.observer.interactions {
			if !interaction.closed {
				tool := interaction.value.Tool
				canceled = append(canceled, StoppedInteractionObservation{RequestID: interaction.value.ID, ArrivalID: interaction.arrival, Kind: interaction.value.Kind, MessageID: tool.MessageID, PartID: s.observer.calls[tool.CallID], CallID: tool.CallID})
			}
		}
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
	// Stable output order is not native event ordering or a replay cursor.
	slices.SortFunc(canceled, func(a, b StoppedInteractionObservation) int { return strings.Compare(a.RequestID, b.RequestID) })
	value := StoppedHistoryObservation{History: history, Stop: receipt, Canceled: canceled, Retries: retries}
	a.stoppedCompletion = &value
	if s.logger != nil {
		s.logger.InfoContext(ctx, "opencode_original_stopped_history_verified", "owner_id", s.owner, "request_id", receipt.RequestID, "interrupted", receipt.InterruptedObserved, "http_accepted", receipt.HTTPAccepted)
	}
	return copyStoppedHistory(value), nil
}
