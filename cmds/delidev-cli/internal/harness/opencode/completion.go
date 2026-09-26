package opencode

import (
	"context"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func copyHistoryObservation(value HistoryObservation) HistoryObservation {
	if value.Todo != nil {
		todo := *value.Todo
		value.Todo = &todo
	}
	value.Messages = slices.Clone(value.Messages)
	for i := range value.Messages {
		value.Messages[i].Parts = slices.Clone(value.Messages[i].Parts)
	}
	return value
}

// CloseCompleted joins fresh original stored-history verification to the
// original process owner's cleanup. It cannot reconstruct missing observations,
// close a claimed Stop, prove Worker publication or produce a restart checkpoint.
// A cleanup failure stays uncertain even if ordinary Close later succeeds.
func (a *OwnedAPI) CloseCompleted(ctx context.Context) (HistoryObservation, error) {
	if !a.valid() {
		return HistoryObservation{}, sessionInvalid()
	}
	select {
	case a.reading <- struct{}{}:
		defer func() { <-a.reading }()
	case <-ctx.Done():
		return HistoryObservation{}, unavailable()
	}
	if ctx.Err() != nil {
		return HistoryObservation{}, unavailable()
	}
	if a.completed != nil {
		return copyHistoryObservation(*a.completed), nil
	}
	if a.completionAttempted {
		return HistoryObservation{}, sessionUncertain()
	}
	s := a.session
	if err := s.enter(ctx); err != nil {
		return HistoryObservation{}, err
	}
	defer s.leave()
	if s.closeOwned == nil || s.observer == nil || s.events == nil {
		return HistoryObservation{}, sessionUncertain()
	}
	history, err := s.readHistory(ctx, s.observer)
	if err != nil {
		return HistoryObservation{}, err
	}
	a.completionAttempted = true
	err = s.closeOwned(ctx)
	s.events.Close()
	if err != nil {
		if s.logger != nil {
			s.logger.WarnContext(ctx, "opencode_completion_cleanup_uncertain", "owner_id", s.owner, "code", domain.SafeError(err).Code)
		}
		return HistoryObservation{}, sessionUncertain()
	}
	a.completed = &history
	if s.logger != nil {
		s.logger.InfoContext(ctx, "opencode_original_completion_cleanup_verified", "owner_id", s.owner, "request_id", history.RequestID)
	}
	return copyHistoryObservation(history), nil
}
