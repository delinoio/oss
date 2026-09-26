package opencode

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// NativeRetry retains only original scheduling facts. Provider diagnostic text
// and upgrade/account actions cannot grant routing, credential or retry authority.
type NativeRetry struct {
	Attempt uint64
	Next    uint64
}

type observedRetry struct {
	assistant string
	event     string
	value     NativeRetry
}

func decodeNativeRetry(raw json.RawMessage) (*NativeRetry, error) {
	fields, err := shape(raw, []string{"type", "attempt", "message", "next"}, nil)
	if err != nil || !scalar(fields["type"], "retry") {
		return nil, observerProblem()
	}
	attempt, ok := nativeCount(fields["attempt"])
	next, valid := nativeCount(fields["next"])
	if _, message := boundedString(fields["message"], 64<<10, false); !ok || !valid || !message {
		return nil, observerProblem()
	}
	return &NativeRetry{Attempt: uint64(attempt), Next: uint64(next)}, nil
}

func (o *inputObserver) observeRetry(raw json.RawMessage, eventID string) (*NativeRetry, error) {
	message := o.messages[o.progress.AssistantID]
	if !o.progress.UserSeen || !o.progress.InputPartSeen || message == nil || message.finalized || message.value.Assistant == nil || message.value.Assistant.Completed != nil || o.progress.TerminalObserved || o.progress.SettledObserved || len(o.retries) >= 1024 || o.stop != nil && !o.stop.sent {
		return nil, observerProblem()
	}
	value, err := decodeNativeRetry(raw)
	if err != nil {
		return nil, err
	}
	observed := observedRetry{assistant: o.progress.AssistantID, event: eventID, value: *value}
	o.retries = append(o.retries, observed)
	o.currentRetry = &observed
	if o.stop != nil {
		o.stop.backoff = true
		o.stop.retries = append(o.stop.retries, observed.stopObservation())
	}
	o.progress.Status, o.progress.IdleNotification = NativeStatusRetry, false
	if o.logger != nil {
		o.logger.InfoContext(o.ctx, "opencode_original_retry_observed", "owner_id", o.owner, "request_id", o.input.receipt.RequestID, "event_id", eventID, "attempt", value.Attempt, "next", value.Next)
	}
	return value, nil
}

func (r observedRetry) stopObservation() domain.OpenCodeStopRetryObservation {
	return domain.OpenCodeStopRetryObservation{NativeEventID: r.event, Attempt: r.value.Attempt, Next: r.value.Next}
}
