package domain

import (
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

type RequestDiagnosticSource int32
type RequestDiagnosticState int32
type RequestDiagnosticOperation int32

const (
	DiagnosticNativeInput   RequestDiagnosticSource    = 1
	DiagnosticProxyHTTP     RequestDiagnosticSource    = 2
	DiagnosticInProgress    RequestDiagnosticState     = 1
	DiagnosticSucceeded     RequestDiagnosticState     = 2
	RequestDiagnosticFailed RequestDiagnosticState     = 3
	DiagnosticCanceled      RequestDiagnosticState     = 4
	DiagnosticInput         RequestDiagnosticOperation = 1
	DiagnosticChat          RequestDiagnosticOperation = 2
	DiagnosticResponse      RequestDiagnosticOperation = 3
	DiagnosticCompact       RequestDiagnosticOperation = 4
	DiagnosticMessage       RequestDiagnosticOperation = 5
	DiagnosticCount         RequestDiagnosticOperation = 6
)

// RequestDiagnostic is a closed projection, never a native document or usage
// sample. Native input observations and HTTP attempts have separate identities.
type RequestDiagnostic struct {
	ID                   ID                         `json:"id"`
	Revision             uint64                     `json:"revision,string"`
	SessionID            ID                         `json:"session_id"`
	ExecutionID          ID                         `json:"execution_id"`
	AccountID            ID                         `json:"account_id"`
	ConnectionID         ID                         `json:"connection_id"`
	ProviderID           ID                         `json:"provider_id"`
	ModelID              ID                         `json:"model_id"`
	Source               RequestDiagnosticSource    `json:"source"`
	Operation            RequestDiagnosticOperation `json:"operation"`
	State                RequestDiagnosticState     `json:"state"`
	Purpose              UsagePurpose               `json:"purpose"`
	Harness              Harness                    `json:"harness"`
	InputID              ID                         `json:"input_id,omitempty"`
	PublicationRequestID ID                         `json:"publication_request_id,omitempty"`
	CorrelationID        ID                         `json:"correlation_id,omitempty"`
	NativeRequestID      string                     `json:"native_request_id,omitempty"`
	ProviderRequestID    string                     `json:"provider_request_id,omitempty"`
	NativeResponseID     string                     `json:"native_response_id,omitempty"`
	NativeThreadID       string                     `json:"native_thread_id,omitempty"`
	NativeTurnID         string                     `json:"native_turn_id,omitempty"`
	RequestedEffort      *string                    `json:"requested_effort"`
	RequestedServiceTier *string                    `json:"requested_service_tier"`
	EffectiveEffort      *string                    `json:"effective_effort"`
	EffectiveServiceTier *string                    `json:"effective_service_tier"`
	ObservedAt           time.Time                  `json:"observed_at"`
	FinishedAt           *time.Time                 `json:"finished_at"`
	DurationMS           *uint64                    `json:"duration_ms,string"`
	HTTPAttempted        *bool                      `json:"http_attempted"`
	HTTPStatus           *uint32                    `json:"http_status"`
	ErrorCode            Code                       `json:"error_code,omitempty"`
}

// Preserve only known non-content settings. Unsupported native spellings are
// unavailable instead of exposing an arbitrary string from a native envelope.
func DiagnosticEffort(value string) *string {
	if slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, value) {
		return &value
	}
	return nil
}

// Request values describe permitted capacity, independently of the effective tier.
func DiagnosticRequestedServiceTier(value string) *string {
	if value == "standard_only" {
		return &value
	}
	return DiagnosticServiceTier(value)
}
func DiagnosticServiceTier(value string) *string {
	if slices.Contains([]string{"auto", "default", "flex", "priority", "standard"}, value) {
		return &value
	}
	return nil
}

// IDs must be opaque canonical UUIDs or closed provider identity spellings.
// Callers additionally apply the protected credential guard before persistence.
func SafeDiagnosticID(value string) bool {
	if id, err := uuid.Parse(value); err == nil && id != uuid.Nil && id.String() == value && id.Variant() == uuid.RFC4122 && (id.Version() == 4 || id.Version() == 7) {
		return true
	}
	for _, prefix := range []string{"req_", "req-", "resp_", "msg_", "chatcmpl-"} {
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) && len(value) <= 128 {
			return strings.IndexFunc(value[len(prefix):], func(r rune) bool {
				return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
			}) < 0
		}
	}
	return false
}

func (d RequestDiagnostic) Validate() error {
	for _, id := range []ID{d.ID, d.SessionID, d.ExecutionID, d.AccountID, d.ConnectionID, d.ProviderID, d.ModelID} {
		if id.Validate() != nil {
			return invalidObservation()
		}
	}
	for _, id := range []ID{d.InputID, d.PublicationRequestID, d.CorrelationID} {
		if id != "" && id.Validate() != nil {
			return invalidObservation()
		}
	}
	if !slices.Contains([]RequestDiagnosticSource{DiagnosticNativeInput, DiagnosticProxyHTTP}, d.Source) || d.Operation < DiagnosticInput || d.Operation > DiagnosticCount || d.State < DiagnosticInProgress || d.State > DiagnosticCanceled || d.ObservedAt.IsZero() || d.Purpose != ConversationUsage && d.Purpose != SessionTitleUsage {
		return invalidObservation()
	}
	if !slices.Contains([]Harness{Codex, ClaudeCode, OpenCode, GrokBuild}, d.Harness) || d.Revision > uint64(1<<63-1) || (d.State == DiagnosticInProgress) != (d.FinishedAt == nil) || (d.State == DiagnosticInProgress || d.State == DiagnosticSucceeded) && d.ErrorCode != "" {
		return invalidObservation()
	}
	for _, setting := range []struct {
		value  *string
		filter func(string) *string
	}{{d.RequestedEffort, DiagnosticEffort}, {d.EffectiveEffort, DiagnosticEffort}, {d.RequestedServiceTier, DiagnosticRequestedServiceTier}, {d.EffectiveServiceTier, DiagnosticServiceTier}} {
		if setting.value != nil && setting.filter(*setting.value) == nil {
			return invalidObservation()
		}
	}
	for _, id := range []string{d.NativeRequestID, d.ProviderRequestID, d.NativeResponseID} {
		if id != "" && !SafeDiagnosticID(id) {
			return invalidObservation()
		}
	}
	if d.NativeThreadID != "" && NativeIdentity(d.NativeThreadID).Validate(d.Harness, NativeThreadIdentity) != nil || d.NativeTurnID != "" && NativeIdentity(d.NativeTurnID).Validate(d.Harness, NativeTurnIdentity) != nil {
		return invalidObservation()
	}
	if d.ErrorCode != "" && !slices.Contains([]Code{InvalidArgument, Unsupported, Unauthenticated, PermissionDenied, ResourceExhausted, Canceled, Conflict, NotFound, RecoveryRequired, Unavailable, Internal}, d.ErrorCode) {
		return invalidObservation()
	}
	if d.FinishedAt != nil && d.FinishedAt.Before(d.ObservedAt) || d.DurationMS != nil && *d.DurationMS > uint64((16*time.Minute).Milliseconds()) || d.HTTPStatus != nil && (*d.HTTPStatus < 100 || *d.HTTPStatus > 599) {
		return invalidObservation()
	}
	if d.Source == DiagnosticNativeInput {
		if d.Operation != DiagnosticInput || d.InputID == "" || d.NativeThreadID == "" || d.NativeRequestID != string(d.ID) || d.HTTPAttempted != nil || d.HTTPStatus != nil || d.DurationMS != nil || d.CorrelationID != "" || d.NativeResponseID != "" || d.ProviderRequestID != "" {
			return invalidObservation()
		}
	} else if d.Operation == DiagnosticInput || d.HTTPAttempted == nil || d.CorrelationID != d.ID || d.InputID != "" || d.NativeThreadID != "" || d.NativeTurnID != "" || d.HTTPStatus != nil && !*d.HTTPAttempted {
		return invalidObservation()
	}
	return nil
}
