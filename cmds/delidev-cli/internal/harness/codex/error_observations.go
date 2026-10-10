// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// These bounded observations never replace terminal, input or credential proof.
type NativeErrorObservation struct{ WillRetry bool }
type nativeRecoveryTelemetry struct {
	RetryingError, NonretryingError, AuthStarted, AuthCompleted bool
}

func closedRecoveryObject(raw json.RawMessage, required, optional []string, target any) bool {
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil || fields == nil {
		return false
	}
	for key := range fields {
		if !slices.Contains(required, key) && !slices.Contains(optional, key) {
			return false
		}
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return false
		}
	}
	return domain.Decode(raw, target) == nil
}
func nullableRecoveryText(value *string) bool {
	return value == nil || domain.Text(*value, "private native diagnostic", 256<<10, false) == nil
}
func validRecoveryErrorInfo(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var tag string
	if json.Unmarshal(raw, &tag) == nil {
		return slices.Contains([]string{"contextWindowExceeded", "sessionBudgetExceeded", "usageLimitExceeded", "rateLimitExceeded", "flexUnavailable", "serverOverloaded", "cyberPolicy", "misalignmentPolicyViolation", "tooManyDenials", "internalServerError", "unauthorized", "badRequest", "threadRollbackFailed", "sandboxError", "other"}, tag)
	}
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil || len(fields) != 1 {
		return false
	}
	for kind, value := range fields {
		if kind == "activeTurnNotSteerable" {
			var detail struct {
				TurnKind string `json:"turnKind"`
			}
			return closedRecoveryObject(value, []string{"turnKind"}, nil, &detail) && slices.Contains([]string{"review", "compact"}, detail.TurnKind)
		}
		if !slices.Contains([]string{"httpConnectionFailed", "responseStreamConnectionFailed", "responseStreamDisconnected", "responseTooManyFailedAttempts"}, kind) {
			return false
		}
		var detail struct {
			HTTPStatusCode *uint16 `json:"httpStatusCode"`
		}
		return closedRecoveryObject(value, nil, []string{"httpStatusCode"}, &detail)
	}
	return false
}
func validRecoveryMisalignment(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var detail struct {
		Explanation *string         `json:"detailedExplanation"`
		Type        *string         `json:"errorType"`
		Target      *string         `json:"reviewTarget"`
		Steer       json.RawMessage `json:"steer"`
	}
	if !closedRecoveryObject(raw, nil, []string{"detailedExplanation", "errorType", "reviewTarget", "steer"}, &detail) || !nullableRecoveryText(detail.Explanation) || !nullableRecoveryText(detail.Type) || !nullableRecoveryText(detail.Target) {
		return false
	}
	if len(detail.Steer) == 0 || string(detail.Steer) == "null" {
		return true
	}
	var steer struct {
		Message *string `json:"message"`
	}
	return closedRecoveryObject(detail.Steer, []string{"message"}, nil, &steer) && steer.Message != nil && nullableRecoveryText(steer.Message)
}
func (c *Client) observeRecoveryTelemetryLocked(native nativewire.Event) (Event, error) {
	var thread, turn domain.ID
	var retry *bool
	if native.Method == "error" {
		var params struct {
			ThreadID  domain.ID       `json:"threadId"`
			TurnID    domain.ID       `json:"turnId"`
			WillRetry *bool           `json:"willRetry"`
			Error     json.RawMessage `json:"error"`
		}
		if !closedRecoveryObject(native.Params, []string{"threadId", "turnId", "willRetry", "error"}, nil, &params) || params.WillRetry == nil {
			return Event{}, incompatible()
		}
		var failure struct {
			Message      *string         `json:"message"`
			Details      *string         `json:"additionalDetails"`
			Code         json.RawMessage `json:"codexErrorInfo"`
			Misalignment json.RawMessage `json:"misalignment"`
		}
		if !closedRecoveryObject(params.Error, []string{"message"}, []string{"additionalDetails", "codexErrorInfo", "misalignment"}, &failure) || failure.Message == nil || !nullableRecoveryText(failure.Message) || !nullableRecoveryText(failure.Details) || !validRecoveryErrorInfo(failure.Code) || !validRecoveryMisalignment(failure.Misalignment) {
			return Event{}, incompatible()
		}
		thread, turn, retry = params.ThreadID, params.TurnID, params.WillRetry
	} else {
		var params struct {
			ThreadID domain.ID `json:"threadId"`
			TurnID   domain.ID `json:"turnId"`
			Provider *string   `json:"provider"`
			Message  *string   `json:"message"`
		}
		if !closedRecoveryObject(native.Params, []string{"threadId", "turnId", "provider", "message"}, nil, &params) || params.Provider == nil || domain.Text(*params.Provider, "native provider", 1024, true) != nil || params.Message == nil || !nullableRecoveryText(params.Message) {
			return Event{}, incompatible()
		}
		if *params.Provider != c.execution.settings.Provider {
			return Event{}, incompatible()
		}
		thread, turn = params.ThreadID, params.TurnID
	}
	if thread.Validate() != nil || turn.Validate() != nil {
		return Event{}, incompatible()
	}
	if thread != c.thread {
		return privateNative(native), nil
	}
	prior, known := c.execution.turns[turn]
	if !known || turn != c.execution.active || prior.Turn.Status != TurnRunning || c.problem != nil || c.execution.paused || c.execution.interrupt != "" || c.execution.compaction != nil {
		return Event{}, incompatible()
	}
	event := Event{ThreadID: c.thread, TurnID: turn, Correlated: true}
	if retry != nil {
		event.Kind, event.Notice = NoticeEvent, domain.NativeWarning
		event.NativeError = &NativeErrorObservation{WillRetry: *retry}
		if *retry {
			prior.recoveryTelemetry.RetryingError = true
		} else {
			prior.recoveryTelemetry.NonretryingError = true
		}
	} else {
		event.Kind = MetadataEvent
		if native.Method == "modelProvider/authRecoveryStarted" {
			event.Metadata = AuthRecoveryStartedObserved
			prior.recoveryTelemetry.AuthStarted = true
		} else {
			event.Metadata = AuthRecoveryCompletedObserved
			prior.recoveryTelemetry.AuthCompleted = true
		}
	}
	c.execution.turns[turn] = prior
	return event, nil
}
