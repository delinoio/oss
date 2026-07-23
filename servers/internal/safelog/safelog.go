// Package safelog provides allowlisted slog fields and a defense-in-depth
// redacting handler. Its APIs intentionally do not accept tokens, secrets,
// arbitrary errors, raw billing PII, or card data.
package safelog

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"regexp"
	"strings"

	"github.com/delinoio/oss/servers/internal/redact"
	"github.com/delinoio/oss/servers/internal/requestmeta"
	"github.com/delinoio/oss/servers/internal/safeerr"
)

// Event is a stable operational event name.
type Event string

const (
	EventRequest        Event = "request"
	EventAuthentication Event = "authentication"
	EventAuthorization  Event = "authorization"
	EventReservation    Event = "reservation"
	EventSettlement     Event = "settlement"
	EventIntegration    Event = "integration"
)

// Decision is an authorization or policy outcome.
type Decision string

const (
	DecisionNone  Decision = ""
	DecisionAllow Decision = "allow"
	DecisionDeny  Decision = "deny"
)

// Result is the operation outcome.
type Result string

const (
	ResultNone    Result = ""
	ResultSuccess Result = "success"
	ResultFailure Result = "failure"
	ResultNoop    Result = "noop"
)

// ActorPseudonym is a keyed, non-reversible actor reference.
type ActorPseudonym string

// Fields is the complete allowlist of shared structured fields.
type Fields struct {
	Method            string
	Procedure         string
	Actor             ActorPseudonym
	OrganizationID    string
	TeamID            string
	ServiceID         string
	MeterID           string
	ReservationID     string
	Decision          Decision
	Result            Result
	ErrorClass        safeerr.Class
	IncludeErrorClass bool
}

var safeValuePattern = regexp.MustCompile(`^[A-Za-z0-9/][A-Za-z0-9._:/-]{0,127}$`)

// Record writes one allowlisted operational event.
func Record(ctx context.Context, logger *slog.Logger, level slog.Level, event Event, fields Fields) {
	if logger == nil {
		return
	}
	eventName := safeValue(string(event))
	if eventName == "" {
		eventName = "invalid_event"
	}
	attributes := []slog.Attr{slog.String("event", eventName)}
	if metadata, ok := requestmeta.FromContext(ctx); ok {
		attributes = appendSafe(attributes, "request_id", metadata.RequestID)
		attributes = appendSafe(attributes, "trace_id", metadata.TraceID)
	}
	attributes = appendSafe(attributes, "request_method", fields.Method)
	attributes = appendSafe(attributes, "request_procedure", fields.Procedure)
	if actor := string(fields.Actor); strings.HasPrefix(actor, "actor:v1:") {
		attributes = appendSafe(attributes, "actor", actor)
	}
	attributes = appendSafe(attributes, "organization_id", fields.OrganizationID)
	attributes = appendSafe(attributes, "team_id", fields.TeamID)
	attributes = appendSafe(attributes, "service_id", fields.ServiceID)
	attributes = appendSafe(attributes, "meter_id", fields.MeterID)
	attributes = appendSafe(attributes, "reservation_id", fields.ReservationID)
	attributes = appendSafe(attributes, "decision", string(fields.Decision))
	attributes = appendSafe(attributes, "result", string(fields.Result))
	if fields.IncludeErrorClass {
		attributes = append(attributes, slog.String("error_class", fields.ErrorClass.String()))
	}
	logger.LogAttrs(ctx, level, eventName, attributes...)
}

func appendSafe(attributes []slog.Attr, key, value string) []slog.Attr {
	if value = safeValue(value); value != "" {
		return append(attributes, slog.String(key, value))
	}
	return attributes
}

func safeValue(value string) string {
	if !safeValuePattern.MatchString(value) || redact.Text(value) != value {
		return ""
	}
	return value
}

// Pseudonymizer creates stable, non-reversible actor references for logs.
type Pseudonymizer struct {
	key []byte
}

// NewPseudonymizer requires a distinct high-entropy operational key. This key
// is not a Logto client secret and should be rotated under service policy.
func NewPseudonymizer(key []byte) (*Pseudonymizer, error) {
	if len(key) < 32 {
		return nil, errors.New("safelog: pseudonymization key must contain at least 32 bytes")
	}
	return &Pseudonymizer{key: append([]byte(nil), key...)}, nil
}

// Actor returns a stable pseudonym suitable for Fields.Actor.
func (p *Pseudonymizer) Actor(subject string) ActorPseudonym {
	if p == nil || subject == "" {
		return ""
	}
	hash := hmac.New(sha256.New, p.key)
	_, _ = hash.Write([]byte(subject))
	return ActorPseudonym("actor:v1:" + hex.EncodeToString(hash.Sum(nil)[:16]))
}
