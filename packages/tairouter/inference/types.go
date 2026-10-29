// SPDX-License-Identifier: Apache-2.0
// Package inference routes inference without owning credentials or application storage.
package inference

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/packages/tairouter/inference/policy"
	"log/slog"
	"net/http"
	"time"
)

type Wire string

const (
	Chat      Wire = "openai-chat"
	Responses Wire = "openai-responses"
	Anthropic Wire = "anthropic"
)

type Operation string

const (
	Generate        Operation = "generate"
	Compact         Operation = "compact"
	CountTokens     Operation = "count_tokens"
	ImageGeneration Operation = "images/generations"
	ImageEdit       Operation = "images/edits"
	Search          Operation = "alpha/search"
)

type AuthMode string

const (
	APIKey    AuthMode = "api-key"
	OAuth     AuthMode = "oauth"
	Anonymous AuthMode = "anonymous"
)

type Principal struct {
	OrganizationID, UserID, KeyID string
	AccountID, CredentialVersion  string
	MembershipGeneration          int64
}

// Admit must consult authoritative storage, reserve admission, and return an idempotent release.
// BeforeSend must reject revoked keys, removed membership, or unavailable storage without caches.
type Authorizer interface {
	Admit(context.Context, string) (Principal, func(), error)
	BeforeSend(context.Context, Principal) error
}
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}
type Router interface {
	Resolve(context.Context, Principal, string, Operation) (Route, error)
	Models(context.Context, Principal) ([]Model, error)
}
type Route struct {
	Targets  []Target
	MaxSends int
}
type Target struct {
	ReasoningRetention *ReasoningRetentionPolicy
	// ForceMessagesTranslation disables Messages passthrough without changing the upstream wire or authentication.
	ForceMessagesTranslation bool
	Fast                     bool
	FastPolicy               *FastPolicy
	ReasoningEffort          string
	Policy                   *policy.Resolved
	Endpoint                 string
	Transport                Transport
	AttemptTimeout           time.Duration
	Provider, BaseURL, Model string
	Wire                     Wire
	Auth                     AuthMode
	Headers                  http.Header
	Retry                    RetryPolicy
	// Nil chooses 300 seconds publicly and no stall deadline for approved local destinations.
	StallTimeout *time.Duration
}

// Policies are captured by the router; absent policies never imply generic retries.
// RateLimitRetries counts extra sends; TransientSends includes the initial send.
type RetryPolicy struct {
	RateLimitRetries, TransientSends                        int
	Interval, MaxWait                                       time.Duration
	RotateOn429, RefreshOn401, FallbackOn429, FallbackOn5xx bool
}
type Credential struct {
	AccountID, Secret string
	// StateBinding identifies an upstream account generation, stable across token refresh.
	StateBinding      string
	CredentialVersion string
	Headers           http.Header
}

// Implementations must fence selection, refresh, and cooldown writes against deletion.
// Select(previousAccount) must return a different eligible account, or ErrNoCredential.
type Credentials interface {
	Select(context.Context, Principal, Target, string) (Credential, error)
	Refresh(context.Context, Principal, Target, Credential) (Credential, error)
}
type FailureReporter interface {
	Failed(context.Context, Principal, Target, Credential, int, time.Duration) error
}
type Continuation struct {
	ResponseID          string
	Provider, AccountID string
	Model               string
	RoutedModel         string
	Input               json.RawMessage
	ExpiresAt           time.Time
}

// Load must reject expiry and ownership mismatches. Save must atomically recheck membership,
// enforce encrypted temporary storage and oldest-first TTL/count/byte limits.
type ContinuationStore interface {
	Load(context.Context, Principal, string) (Continuation, error)
	Save(context.Context, Principal, string, Continuation) error
}
type Usage struct {
	InputTokens        *int64 `json:"input_tokens,omitempty"`
	OutputTokens       *int64 `json:"output_tokens,omitempty"`
	CacheReadTokens    *int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens   *int64 `json:"cache_write_tokens,omitempty"`
	CacheWrite1hTokens *int64 `json:"cache_write_1h_tokens,omitempty"`
}
type RequestOutcome string

const (
	OutcomeSuccess       RequestOutcome = "success"
	OutcomeUpstreamError RequestOutcome = "upstream_error"
	OutcomeGatewayError  RequestOutcome = "gateway_error"
	OutcomeRateLimited   RequestOutcome = "rate_limited"
	OutcomeCanceled      RequestOutcome = "canceled"
)

type UsageRecord struct {
	budget            *physicalBudgetState
	continuationInput any
	Outcome           RequestOutcome
	AccountingRole    UsageAccountingRole
	OperationID       string
	// ServiceTier is confirmed by the provider response, never copied from request.
	ServiceTier string
	// IntermediateUsage contains confirmed prior model legs on this exact target.
	IntermediateUsage                     []Usage
	ResponseID                            string
	UnmeasuredAttempts                    int
	RequestID, Provider, Model, AccountID string
	Status, Sends                         int
	Usage                                 Usage
	Canceled                              bool
}

type UsageAccountingRole uint8

const (
	UsageAccountingParent UsageAccountingRole = iota
	UsageAccountingNestedOperation
)

// Record fixes price/provenance at processing time and must not recreate deleted state.
type UsageRecorder interface {
	Record(context.Context, Principal, UsageRecord) error
}
type Observer interface{ Event(context.Context, string) }

// ArtifactStore owns R2 upload, owner-scoped metadata, 200-file eviction, 24h expiry,
// and signed URLs of at most five minutes, bounded by the object's expiry.
type GeneratedImage struct {
	Data        []byte
	ContentType string
}

// ArtifactBatchStore publishes all validated response images through one owner-scoped R2 batch.
type ArtifactBatchStore interface {
	PutBatch(context.Context, Principal, []GeneratedImage) ([]string, error)
}

type ArtifactStore interface {
	Put(context.Context, Principal, []byte, string) (string, error)
	URL(context.Context, Principal, string) (string, error)
}
type Config struct {
	CompactionHandoffs   CompactionHandoffs
	ResponseObserver     PhysicalResponseObserver
	Negotiator           Negotiator
	ProviderStates       ProviderStates
	RecoveryPolicies     map[Wire]HTTPRecoveryPolicy
	RecoveryDestinations RecoveryDestinations
	BodyDecoders         map[string]BodyDecoder
	Views                ClientViews
	Contexts             ContextResolver
	LiveBindings         LiveBindingStore
	SocketDialer         SocketDialer
	Adapters             map[Wire]Adapter
	Authorizer           Authorizer
	Router               Router
	Credentials          Credentials
	Continuations        ContinuationStore
	Usage                UsageRecorder
	Artifacts            ArtifactStore
	Destinations         DestinationPolicy
	Observer             Observer
	Logger               *slog.Logger
	MaxRequestBytes      int64
}
type Error struct {
	Status     int
	Code       string
	RetryAfter time.Duration
}

func (e *Error) Error() string { return e.Code }

var (
	ErrUnauthorized = &Error{Status: 401, Code: "unauthorized"}
	ErrUnavailable  = &Error{Status: 503, Code: "authorization_unavailable"}
	ErrNoCredential = &Error{Status: 503, Code: "no_eligible_credential"}
	ErrContinuation = &Error{Status: 400, Code: "continuation_unavailable"}
	ErrUnsupported  = &Error{Status: 501, Code: "unsupported_provider_path"}
	ErrDestination  = &Error{Status: 403, Code: "destination_not_allowed"}
	ErrAmbiguous    = &Error{Status: 429, Code: "upstream_reset_replay_refused"}
)

func publicError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Status: 504, Code: "upstream_timeout"}
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Status: 499, Code: "request_canceled"}
	}
	return &Error{Status: 502, Code: "upstream_error"}
}
