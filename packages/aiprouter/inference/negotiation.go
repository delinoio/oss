// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"time"
)

// Negotiator derives a concrete per-attempt route/session from a logical selection.
// Support sends must use the supplied policy and inherited physical-send budget.
type Negotiator interface {
	Negotiate(context.Context, Principal, Target, Credential, json.RawMessage, DestinationPolicy) (Target, Credential, time.Time, error)
}
type negotiationExpiryKey struct{}

func validateNegotiationExpiry(ctx context.Context) error {
	if expires, ok := ctx.Value(negotiationExpiryKey{}).(time.Time); ok && !expires.IsZero() && time.Until(expires) < time.Second {
		return &Error{503, "negotiated_session_expired", 0}
	}
	return nil
}
