package grok

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

type RetryKind string
type RetryError string

const (
	Retrying  RetryKind  = "retrying"
	HTTPRetry RetryError = "http"
)

// Original retry metadata may precede a targeted Stop because relay authority
// is revoked first. The private error reason is validated and discarded; it is
// not a diagnostic, transcript message, input replay or completion capability.
type RetryObservation struct {
	Session     domain.ID  `json:"session_id"`
	Event       string     `json:"event_id"`
	TimestampMS uint64     `json:"timestamp_ms"`
	Kind        RetryKind  `json:"kind"`
	Error       RetryError `json:"error"`
	Attempt     uint64     `json:"attempt"`
	MaxRetries  uint64     `json:"max_retries"`
}

func (v RetryObservation) Validate(session domain.ID) error {
	if v.Session != session || v.Kind != Retrying || v.Error != HTTPRetry || v.Attempt == 0 || v.MaxRetries != 3 || v.Attempt > v.MaxRetries || v.TimestampMS > 253402300799999 {
		return incompatible()
	}
	_, err := eventIndex(v.Event, session)
	return err
}

func parseRetry(raw []byte, session domain.ID) (RetryObservation, error) {
	var v struct {
		Session domain.ID `json:"sessionId"`
		Update  struct {
			Kind       string     `json:"sessionUpdate"`
			Type       RetryKind  `json:"type"`
			Error      RetryError `json:"error_type"`
			Attempt    uint64     `json:"attempt"`
			MaxRetries uint64     `json:"max_retries"`
			Reason     string     `json:"reason"`
		} `json:"update"`
		Meta struct {
			Event       string `json:"eventId"`
			TimestampMS uint64 `json:"agentTimestampMs"`
		} `json:"_meta"`
	}
	if decode(raw, &v) != nil || v.Update.Kind != "retry_state" || !text(v.Update.Reason, 16<<10) {
		return RetryObservation{}, incompatible()
	}
	result := RetryObservation{Session: v.Session, Event: v.Meta.Event, TimestampMS: v.Meta.TimestampMS, Kind: v.Update.Type, Error: v.Update.Error, Attempt: v.Update.Attempt, MaxRetries: v.Update.MaxRetries}
	return result, result.Validate(session)
}
