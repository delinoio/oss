package domain

import "time"

type AuthenticationEvidence string

const (
	CredentialAccepted    AuthenticationEvidence = "credential-accepted"
	KeylessEndpoint       AuthenticationEvidence = "keyless-endpoint"
	AuthenticationUnknown AuthenticationEvidence = "unknown"
)

// A non-inference observation is not selected-model execution capability, quota
// recovery, or cost evidence. Custom public catalogs may not authenticate keys.
type AccountValidation struct {
	RequestID         ID                     `json:"request_id"`
	ConnectionID      ID                     `json:"connection_id"`
	ObservedAt        time.Time              `json:"observed_at"`
	State             ObservationState       `json:"state"`
	Authentication    AuthenticationEvidence `json:"authentication"`
	ModelCount        uint32                 `json:"model_count"`
	HTTPStatus        uint32                 `json:"http_status,omitempty"`
	RetryAfterSeconds *uint32                `json:"retry_after_seconds,omitempty"`
	Problem           *Error                 `json:"problem,omitempty"`
}
