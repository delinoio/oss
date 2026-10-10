// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

const NativeDiagnosticObserved MetadataKind = "native-diagnostic-observed"

type nativeErrorCode string
type nonSteerableTurnKind string

const nativeReviewTurn nonSteerableTurnKind = "review"
const nativeCompactTurn nonSteerableTurnKind = "compact"

// Observations belong to their original turn, not an account recovery action.
// Unexported fields prevent native text from entering generic serialization.
type nativeDiagnostics struct {
	retryingError, nonretryingError *nativeFailureObservation
	authStarted, authCompleted      *nativeAuthObservation
}
type nativeFailureObservation struct {
	willRetry         bool
	message           string
	additionalDetails *string
	code              nativeErrorCode
	httpStatus        *uint16
	turnKind          nonSteerableTurnKind
	misalignment      *nativeMisalignment
}
type nativeAuthObservation struct{ provider, message string }
type nativeMisalignment struct {
	errorType, explanation, target *string
	steer                          *string
}

func diagnosticText(text *string, required bool) bool {
	return (!required || text != nil) && (text == nil || domain.Text(*text, "native diagnostic", nativewire.MaxFrame, false) == nil)
}

func decodeNativeErrorInfo(raw json.RawMessage, failure *nativeFailureObservation) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var tag nativeErrorCode
	if json.Unmarshal(raw, &tag) == nil {
		// The native catalog is forward-open. This observed profile is deliberately
		// closed: future tags need their own schema inspection before acceptance.
		if !slices.Contains([]nativeErrorCode{"contextWindowExceeded", "sessionBudgetExceeded", "usageLimitExceeded", "rateLimitExceeded", "flexUnavailable", "serverOverloaded", "cyberPolicy", "misalignmentPolicyViolation", "tooManyDenials", "internalServerError", "unauthorized", "badRequest", "threadRollbackFailed", "sandboxError", "other"}, tag) {
			return incompatible()
		}
		failure.code = tag
		return nil
	}
	var variants map[string]json.RawMessage
	if domain.Decode(raw, &variants) != nil || len(variants) != 1 {
		return incompatible()
	}
	for key, value := range variants {
		switch key {
		case "httpConnectionFailed", "responseStreamConnectionFailed", "responseStreamDisconnected", "responseTooManyFailedAttempts":
			var p struct {
				Status *uint16 `json:"httpStatusCode"`
			}
			if string(value) == "null" || domain.Decode(value, &p) != nil {
				return incompatible()
			}
			failure.code, failure.httpStatus = nativeErrorCode(key), p.Status
		case "activeTurnNotSteerable":
			var p struct {
				Kind *nonSteerableTurnKind `json:"turnKind"`
			}
			if domain.Decode(value, &p) != nil || p.Kind == nil || (*p.Kind != nativeReviewTurn && *p.Kind != nativeCompactTurn) {
				return incompatible()
			}
			failure.code, failure.turnKind = nativeErrorCode(key), *p.Kind
		default:
			return incompatible()
		}
	}
	return nil
}

func decodeNativeMisalignment(raw json.RawMessage) (*nativeMisalignment, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var p struct {
		Type        *string         `json:"errorType"`
		Explanation *string         `json:"detailedExplanation"`
		Target      *string         `json:"reviewTarget"`
		Steer       json.RawMessage `json:"steer"`
	}
	if domain.Decode(raw, &p) != nil || !diagnosticText(p.Type, false) || !diagnosticText(p.Explanation, false) || !diagnosticText(p.Target, false) {
		return nil, incompatible()
	}
	result := &nativeMisalignment{errorType: p.Type, explanation: p.Explanation, target: p.Target}
	if len(p.Steer) != 0 && string(p.Steer) != "null" {
		var steer struct {
			Message *string `json:"message"`
		}
		if domain.Decode(p.Steer, &steer) != nil || !diagnosticText(steer.Message, true) {
			return nil, incompatible()
		}
		result.steer = steer.Message
	}
	// Native continuation descriptors are retained privately, never submitted.
	return result, nil
}

func (c *Client) observeNativeDiagnosticLocked(native nativewire.Event) (Event, error) {
	var thread, turnID domain.ID
	var failure *nativeFailureObservation
	var auth *nativeAuthObservation
	switch native.Method {
	case "error":
		var p struct {
			Thread    domain.ID       `json:"threadId"`
			Turn      domain.ID       `json:"turnId"`
			WillRetry *bool           `json:"willRetry"`
			Error     json.RawMessage `json:"error"`
		}
		if domain.Decode(native.Params, &p) != nil || p.WillRetry == nil || len(p.Error) == 0 || string(p.Error) == "null" {
			return Event{}, incompatible()
		}
		var detail struct {
			Message      *string         `json:"message"`
			Additional   *string         `json:"additionalDetails"`
			Code         json.RawMessage `json:"codexErrorInfo"`
			Misalignment json.RawMessage `json:"misalignment"`
		}
		if domain.Decode(p.Error, &detail) != nil || !diagnosticText(detail.Message, true) || !diagnosticText(detail.Additional, false) {
			return Event{}, incompatible()
		}
		failure = &nativeFailureObservation{willRetry: *p.WillRetry, message: *detail.Message, additionalDetails: detail.Additional}
		if decodeNativeErrorInfo(detail.Code, failure) != nil {
			return Event{}, incompatible()
		}
		var err error
		failure.misalignment, err = decodeNativeMisalignment(detail.Misalignment)
		if err != nil {
			return Event{}, err
		}
		thread, turnID = p.Thread, p.Turn
	case "modelProvider/authRecoveryStarted", "modelProvider/authRecoveryCompleted":
		var p struct {
			Thread   domain.ID `json:"threadId"`
			Turn     domain.ID `json:"turnId"`
			Provider *string   `json:"provider"`
			Message  *string   `json:"message"`
		}
		if domain.Decode(native.Params, &p) != nil || p.Provider == nil || domain.Text(*p.Provider, "native recovery provider", 1024, false) != nil || !diagnosticText(p.Message, true) {
			return Event{}, incompatible()
		}
		thread, turnID = p.Thread, p.Turn
		auth = &nativeAuthObservation{provider: *p.Provider, message: *p.Message}
	default:
		return privateNative(native), nil
	}
	if thread.Validate() != nil || turnID.Validate() != nil {
		return Event{}, incompatible()
	}
	if thread != c.thread {
		return privateNative(native), nil
	}
	turn, known := c.execution.turns[turnID]
	if !known || (auth != nil && auth.provider != c.execution.settings.Provider) {
		return Event{}, incompatible()
	}
	event := c.metadata(NativeDiagnosticObserved)
	event.TurnID = turnID
	if turn.Turn.Status.terminal() {
		event.Late = true
		return event, nil
	}
	if turnID != c.execution.active {
		return Event{}, incompatible()
	}
	if turn.diagnostics == nil {
		turn.diagnostics = &nativeDiagnostics{}
	}
	if failure != nil {
		// Retry telemetry never retries an input and nonretrying errors cannot stand
		// in for terminal evidence, definite rejection or a credit-routing marker.
		if failure.willRetry {
			turn.diagnostics.retryingError = failure
		} else {
			turn.diagnostics.nonretryingError = failure
		}
	} else if native.Method == "modelProvider/authRecoveryStarted" {
		turn.diagnostics.authStarted = auth
	} else {
		turn.diagnostics.authCompleted = auth
	}
	c.execution.turns[turnID] = turn
	return event, nil
}
