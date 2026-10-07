package domain

import (
	"crypto/sha256"
	"encoding/hex"
)

// Original live Stop, native outcome and joined process cleanup are independent
// observations. This contains no input, answer, tool output or continuation key.
type OpenCodeStopObservation struct {
	RetryCanceledObserved bool                           `json:"retry_canceled_observed,omitempty"`
	RetryObservations     []OpenCodeStopRetryObservation `json:"retry_observations,omitempty"`
	RequestID             ID                             `json:"request_id"`
	InputRequestID        ID                             `json:"input_request_id"`
	InputPartID           string                         `json:"input_part_id"`
	AssistantID           string                         `json:"assistant_id"`
	HistoryDigest         string                         `json:"history_digest"`
	HTTPAccepted          bool                           `json:"http_accepted"`
	InterruptedObserved   bool                           `json:"interrupted_observed"`
	TerminalObserved      bool                           `json:"terminal_observed"`
	IdleObserved          bool                           `json:"idle_observed"`
	PendingCleared        bool                           `json:"pending_cleared"`
	CleanupVerified       bool                           `json:"cleanup_verified"`
}

// These notifications may already be queued when revocation ends the relay and
// the original abort cancels native backoff. They authorize no retry or action.
type OpenCodeStopRetryObservation struct {
	NativeEventID string `json:"native_event_id"`
	Attempt       uint64 `json:"attempt"`
	Next          uint64 `json:"next"`
}

func (p OpenCodeStopObservation) Validate() error {
	digest, err := hex.DecodeString(p.HistoryDigest)
	if p.RequestID.Validate() != nil || p.InputRequestID.Validate() != nil || p.RequestID == p.InputRequestID || NativeIdentity(p.InputPartID).Validate(OpenCode, NativePartIdentity) != nil || NativeIdentity(p.AssistantID).Validate(OpenCode, NativeMessageIdentity) != nil || err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != p.HistoryDigest || !p.HTTPAccepted && !p.InterruptedObserved || !p.TerminalObserved || !p.IdleObserved || !p.PendingCleared {
		return Fail(InvalidArgument, "Incomplete original OpenCode Stop evidence.", "Retain independent native completion, interruption and owned cleanup facts.")
	}
	seen := map[string]bool{}
	if p.RetryCanceledObserved && (!p.HTTPAccepted || p.InterruptedObserved || len(p.RetryObservations) == 0) {
		return invalidInteraction()
	}
	if len(p.RetryObservations) > 1024 {
		return invalidInteraction()
	}
	for _, retry := range p.RetryObservations {
		if NativeIdentity(retry.NativeEventID).Validate(OpenCode, NativeEventIdentity) != nil || seen[retry.NativeEventID] || retry.Attempt > 9007199254740991 || retry.Next > 9007199254740991 {
			return invalidInteraction()
		}
		seen[retry.NativeEventID] = true
	}
	return nil
}

// Closing an unanswered interrupted request cannot assert a native reply. Its
// original proposal and independently interrupted tool remain separately bound.
type OpenCodeStopClosure struct {
	Stop            OpenCodeStopObservation `json:"stop"`
	ProposalEventID string                  `json:"proposal_event_id"`
	ToolInterrupted bool                    `json:"tool_interrupted"`
}

func (p OpenCodeStopClosure) Validate() error {
	if p.Stop.Validate() != nil || NativeIdentity(p.ProposalEventID).Validate(OpenCode, NativeEventIdentity) != nil || !p.ToolInterrupted {
		return invalidInteraction()
	}
	return nil
}
