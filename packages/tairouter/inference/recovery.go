// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"net/http"
	"time"
)

type RecoveryAction string

const (
	RecoveryDefault  RecoveryAction = "default"
	RecoveryStop     RecoveryAction = "stop"
	RecoveryRetry    RecoveryAction = "retry"
	RecoveryRefresh  RecoveryAction = "refresh"
	RecoveryRotate   RecoveryAction = "rotate"
	RecoveryFallback RecoveryAction = "fallback"
)

type HTTPFailure struct {
	Status                int
	Header                http.Header
	Body                  []byte
	Attempt, EndpointHops int
	Repairs               int
	Refreshed             bool
}
type RecoveryDecision struct {
	Action   RecoveryAction
	Delay    time.Duration
	Endpoint string
	Error    *Error
}

// HTTPRecoveryPolicy classifies bounded pre-output failures. It cannot send or mutate
// account state. A decision is advice; cancellation, authorization, ownership and budget
// checks still apply. Error bodies are request-local and must never enter logs or storage.
type HTTPRecoveryPolicy interface {
	RecoverHTTP(context.Context, AdapterInput, HTTPFailure) (RecoveryDecision, error)
}

// RecoveryDestinations authorizes credential transfer to an alternate provider origin.
// Public HTTPS approval alone does not authorize transfer of an OAuth credential.
type RecoveryDestinations interface {
	AuthorizeRecovery(context.Context, Principal, Target, string) error
}
