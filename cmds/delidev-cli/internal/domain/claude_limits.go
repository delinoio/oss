// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Claude usage tracking is a separate, server-owned, read-only connection of a
// server-token Claude account. It never replaces the execution setup-token and
// never changes execution readiness. Usage windows are informational: they are
// projected with Blocking=false, so routing never consumes them.

const (
	// ClaudeLimitEndpoint is the only origin and path the usage read may contact.
	ClaudeLimitEndpoint = "https://api.anthropic.com/api/oauth/usage"
	// ClaudeLimitBeta is the required anthropic-beta header value.
	ClaudeLimitBeta = "oauth-2025-04-20"
	// ClaudeLimitComparisonGroup labels projected windows so they are never
	// compared with another provider's windows.
	ClaudeLimitComparisonGroup = "claude"
	// ClaudeLimitDueInterval is the existing inactive-maintenance cadence. The
	// presentation freshness limit stays five minutes.
	ClaudeLimitDueInterval = 4 * time.Minute
	// ClaudeLimitRequestTimeout bounds one authenticated read.
	ClaudeLimitRequestTimeout = 5 * time.Second
	// ClaudeLimitMaxResponse bounds the decoded response body.
	ClaudeLimitMaxResponse = 1 << 20
	// ClaudeLimitDefaultBackoff applies to 403/429 without a usable Retry-After.
	ClaudeLimitDefaultBackoff = 5 * time.Minute
	// ClaudeLimitMaxBackoff caps every Retry-After value.
	ClaudeLimitMaxBackoff = time.Hour
	// ClaudeLimitOperationWindow bounds one usage login operation.
	ClaudeLimitOperationWindow = 15 * time.Minute
	// ClaudeLimitRefreshMargin renews the access token before its expiry.
	ClaudeLimitRefreshMargin = 2 * time.Minute
)

// ClaudeLimitWindowID is the closed set of normalized windows.
type ClaudeLimitWindowID string

const (
	ClaudeLimitFiveHour ClaudeLimitWindowID = "five_hour"
	ClaudeLimitSevenDay ClaudeLimitWindowID = "seven_day"
)

// ClaudeLimitWindowIDs lists the normalized windows in presentation order.
var ClaudeLimitWindowIDs = []ClaudeLimitWindowID{ClaudeLimitFiveHour, ClaudeLimitSevenDay}

// Valid reports whether the window is normalized by v1.
func (w ClaudeLimitWindowID) Valid() bool { return slices.Contains(ClaudeLimitWindowIDs, w) }

// DurationMinutes is the fixed length of the window.
func (w ClaudeLimitWindowID) DurationMinutes() int64 {
	if w == ClaudeLimitFiveHour {
		return 5 * 60
	}
	return 7 * 24 * 60
}

// ClaudeLimitFailure is the closed vocabulary of usage failure causes. It is the
// only failure detail logged or presented; native bodies, identities and paths
// never appear.
type ClaudeLimitFailure string

const (
	ClaudeLimitFailureNone                ClaudeLimitFailure = ""
	ClaudeLimitFailureTimeout             ClaudeLimitFailure = "timeout"
	ClaudeLimitFailureNetwork             ClaudeLimitFailure = "network"
	ClaudeLimitFailureRoute               ClaudeLimitFailure = "route_unsupported"
	ClaudeLimitFailureRedirect            ClaudeLimitFailure = "redirect"
	ClaudeLimitFailureOversized           ClaudeLimitFailure = "oversized"
	ClaudeLimitFailureMalformed           ClaudeLimitFailure = "malformed"
	ClaudeLimitFailureEmpty               ClaudeLimitFailure = "empty"
	ClaudeLimitFailureUnauthorized        ClaudeLimitFailure = "unauthorized"
	ClaudeLimitFailureForbidden           ClaudeLimitFailure = "forbidden"
	ClaudeLimitFailureRateLimited         ClaudeLimitFailure = "rate_limited"
	ClaudeLimitFailureServer              ClaudeLimitFailure = "server_error"
	ClaudeLimitFailureCredentials         ClaudeLimitFailure = "credentials_unavailable"
	ClaudeLimitFailureStorage             ClaudeLimitFailure = "storage_unsupported"
	ClaudeLimitFailureRefreshRejected     ClaudeLimitFailure = "refresh_rejected"
	ClaudeLimitFailureRefreshUnsupported  ClaudeLimitFailure = "refresh_unsupported"
	ClaudeLimitFailureRefreshUncertain    ClaudeLimitFailure = "refresh_uncertain"
	ClaudeLimitFailureIdentityChanged     ClaudeLimitFailure = "identity_changed"
	ClaudeLimitFailureLoginRejected       ClaudeLimitFailure = "login_rejected"
	ClaudeLimitFailureLoginUnsupported    ClaudeLimitFailure = "login_unsupported"
	ClaudeLimitFailureProfileUnsafe       ClaudeLimitFailure = "profile_unsafe"
	ClaudeLimitFailureCleanupUncertain    ClaudeLimitFailure = "cleanup_uncertain"
	ClaudeLimitFailureInstallation        ClaudeLimitFailure = "installation_unsupported"
	ClaudeLimitFailureInterrupted         ClaudeLimitFailure = "interrupted"
	ClaudeLimitFailureAuthorityChanged    ClaudeLimitFailure = "authority_changed"
	ClaudeLimitFailureLoginNotInteractive ClaudeLimitFailure = "login_manual_code_required"
)

// Valid reports whether the failure is a member of the closed vocabulary.
func (f ClaudeLimitFailure) Valid() bool {
	switch f {
	case ClaudeLimitFailureNone, ClaudeLimitFailureTimeout, ClaudeLimitFailureNetwork, ClaudeLimitFailureRoute, ClaudeLimitFailureRedirect, ClaudeLimitFailureOversized, ClaudeLimitFailureMalformed, ClaudeLimitFailureEmpty, ClaudeLimitFailureUnauthorized, ClaudeLimitFailureForbidden, ClaudeLimitFailureRateLimited, ClaudeLimitFailureServer, ClaudeLimitFailureCredentials, ClaudeLimitFailureStorage, ClaudeLimitFailureRefreshRejected, ClaudeLimitFailureRefreshUnsupported, ClaudeLimitFailureRefreshUncertain, ClaudeLimitFailureIdentityChanged, ClaudeLimitFailureLoginRejected, ClaudeLimitFailureLoginUnsupported, ClaudeLimitFailureProfileUnsafe, ClaudeLimitFailureCleanupUncertain, ClaudeLimitFailureInstallation, ClaudeLimitFailureInterrupted, ClaudeLimitFailureAuthorityChanged, ClaudeLimitFailureLoginNotInteractive:
		return true
	}
	return false
}

// ReconnectRequired reports whether the failure can only be repaired by an
// explicit new usage login.
func (f ClaudeLimitFailure) ReconnectRequired() bool {
	switch f {
	case ClaudeLimitFailureRefreshRejected, ClaudeLimitFailureRefreshUnsupported, ClaudeLimitFailureRefreshUncertain, ClaudeLimitFailureIdentityChanged, ClaudeLimitFailureStorage, ClaudeLimitFailureCredentials:
		return true
	}
	return false
}

// ClaudeLimitFailureError carries a closed usage failure through the error
// chain. Its message is generic; the code is the only detail.
type ClaudeLimitFailureError struct {
	Failure ClaudeLimitFailure
	// RetryAfter is the suppression the provider requested for 403/429.
	RetryAfter time.Duration
}

func (e *ClaudeLimitFailureError) Error() string { return "Claude usage read failed." }

// Unwrap exposes the equivalent domain error so SafeError classifies it.
func (e *ClaudeLimitFailureError) Unwrap() error {
	code := Unavailable
	switch e.Failure {
	case ClaudeLimitFailureRoute, ClaudeLimitFailureRefreshUnsupported, ClaudeLimitFailureStorage, ClaudeLimitFailureLoginUnsupported:
		code = Unsupported
	case ClaudeLimitFailureUnauthorized, ClaudeLimitFailureForbidden:
		code = Unauthenticated
	case ClaudeLimitFailureRateLimited:
		code = ResourceExhausted
	case ClaudeLimitFailureRefreshUncertain, ClaudeLimitFailureCleanupUncertain:
		code = RecoveryRequired
	case ClaudeLimitFailureMalformed, ClaudeLimitFailureEmpty, ClaudeLimitFailureOversized:
		code = InvalidArgument
	}
	return Fail(code, "Claude usage tracking could not complete.", "Inspect the usage connection status; reconnect only when it asks for it.")
}

// ClaudeLimitFailureOf extracts the closed failure of err, or Internal-safe none.
func ClaudeLimitFailureOf(err error) ClaudeLimitFailure {
	var failure *ClaudeLimitFailureError
	if errors.As(err, &failure) {
		return failure.Failure
	}
	return ClaudeLimitFailureNone
}

func usageFailure(f ClaudeLimitFailure) error { return &ClaudeLimitFailureError{Failure: f} }

// ClaudeLimitWindow is one normalized, accepted window. Remaining is the
// fraction left (0..1); ResetAt is nil when the provider reported null.
type ClaudeLimitWindow struct {
	ID        ClaudeLimitWindowID `json:"id"`
	Remaining float64             `json:"remaining"`
	ResetAt   *time.Time          `json:"reset_at,omitempty"`
}

// ClaudeLimitObservation is one validated nonempty fetch.
type ClaudeLimitObservation struct {
	ObservedAt time.Time           `json:"observed_at"`
	Windows    []ClaudeLimitWindow `json:"windows"`
}

// ParseClaudeLimit normalizes a usage response. Only five_hour and seven_day
// are recognized and unrelated fields are ignored. A recognized window must be
// null/absent or a complete object: finite utilization from 0 through 100 and a
// null/absent or RFC 3339 reset time. Any malformed recognized window fails the
// whole observation. Duplicate recognized keys are malformed. A response with no
// available window is the closed Empty failure and never advances evidence.
func ParseClaudeLimit(body []byte, observedAt time.Time) (ClaudeLimitObservation, error) {
	if len(body) > ClaudeLimitMaxResponse {
		return ClaudeLimitObservation{}, usageFailure(ClaudeLimitFailureOversized)
	}
	if observedAt.IsZero() {
		return ClaudeLimitObservation{}, usageFailure(ClaudeLimitFailureMalformed)
	}
	fields, err := usageTopLevel(body)
	if err != nil {
		return ClaudeLimitObservation{}, usageFailure(ClaudeLimitFailureMalformed)
	}
	observation := ClaudeLimitObservation{ObservedAt: observedAt.UTC()}
	for _, id := range ClaudeLimitWindowIDs {
		raw, present := fields[string(id)]
		if !present || isJSONNull(raw) {
			continue
		}
		window, err := parseClaudeLimitWindow(id, raw)
		if err != nil {
			return ClaudeLimitObservation{}, err
		}
		observation.Windows = append(observation.Windows, window)
	}
	if len(observation.Windows) == 0 {
		return ClaudeLimitObservation{}, usageFailure(ClaudeLimitFailureEmpty)
	}
	return observation, nil
}

func isJSONNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

// usageTopLevel decodes the top-level object while rejecting duplicate keys,
// which a plain map decode would silently resolve to the last value.
func usageTopLevel(body []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, errors.New("not an object")
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("invalid key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, errors.New("duplicate key")
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data")
	}
	return fields, nil
}

func parseClaudeLimitWindow(id ClaudeLimitWindowID, raw json.RawMessage) (ClaudeLimitWindow, error) {
	malformed := usageFailure(ClaudeLimitFailureMalformed)
	fields, err := usageTopLevel(raw)
	if err != nil {
		return ClaudeLimitWindow{}, malformed
	}
	utilizationRaw, present := fields["utilization"]
	if !present {
		return ClaudeLimitWindow{}, malformed
	}
	trimmed := bytes.TrimSpace(utilizationRaw)
	// A JSON number only: a quoted or literal NaN/Infinity is not a number.
	if len(trimmed) == 0 || trimmed[0] == '"' || trimmed[0] == 'N' || trimmed[0] == 'I' || trimmed[0] == '-' && len(trimmed) > 1 && trimmed[1] == 'I' {
		return ClaudeLimitWindow{}, malformed
	}
	utilization, err := strconv.ParseFloat(string(trimmed), 64)
	if err != nil || math.IsNaN(utilization) || math.IsInf(utilization, 0) || utilization < 0 || utilization > 100 {
		return ClaudeLimitWindow{}, malformed
	}
	window := ClaudeLimitWindow{ID: id, Remaining: 1 - utilization/100}
	if window.Remaining < 0 {
		window.Remaining = 0
	}
	if resetRaw, present := fields["resets_at"]; present && !isJSONNull(resetRaw) {
		var text string
		if json.Unmarshal(resetRaw, &text) != nil || text == "" {
			return ClaudeLimitWindow{}, malformed
		}
		at, err := time.Parse(time.RFC3339Nano, text)
		if err != nil || at.Unix() <= 0 || at.Year() > 9999 {
			return ClaudeLimitWindow{}, malformed
		}
		at = at.UTC()
		window.ResetAt = &at
	}
	return window, nil
}

// ParseClaudeRetryAfter converts a Retry-After header (delay seconds or an HTTP
// date) into a suppression duration. A missing or unusable value selects the
// five-minute default and every usable value is capped at one hour.
func ParseClaudeRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return ClaudeLimitDefaultBackoff
	}
	var delay time.Duration
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 {
			return ClaudeLimitDefaultBackoff
		}
		if seconds > int64(ClaudeLimitMaxBackoff/time.Second) {
			return ClaudeLimitMaxBackoff
		}
		delay = time.Duration(seconds) * time.Second
	} else if at, err := http.ParseTime(value); err == nil {
		delay = at.Sub(now)
	} else {
		return ClaudeLimitDefaultBackoff
	}
	if delay < time.Second {
		delay = time.Second
	}
	return min(delay, ClaudeLimitMaxBackoff)
}

// ClaudeLimitLoginState is the lifecycle of one usage login operation.
type ClaudeLimitLoginState string

const (
	ClaudeLimitLoginPreparing   ClaudeLimitLoginState = "preparing"
	ClaudeLimitLoginWaiting     ClaudeLimitLoginState = "waiting"
	ClaudeLimitLoginConfirming  ClaudeLimitLoginState = "confirming"
	ClaudeLimitLoginCanceled    ClaudeLimitLoginState = "canceled"
	ClaudeLimitLoginExpired     ClaudeLimitLoginState = "expired"
	ClaudeLimitLoginUnsupported ClaudeLimitLoginState = "unsupported"
	ClaudeLimitLoginFailed      ClaudeLimitLoginState = "failed"
	ClaudeLimitLoginRecovery    ClaudeLimitLoginState = "recovery-required"
	ClaudeLimitLoginLinked      ClaudeLimitLoginState = "linked"
)

// Valid reports whether the state belongs to the closed set.
func (s ClaudeLimitLoginState) Valid() bool {
	switch s {
	case ClaudeLimitLoginPreparing, ClaudeLimitLoginWaiting, ClaudeLimitLoginConfirming, ClaudeLimitLoginCanceled, ClaudeLimitLoginExpired, ClaudeLimitLoginUnsupported, ClaudeLimitLoginFailed, ClaudeLimitLoginRecovery, ClaudeLimitLoginLinked:
		return true
	}
	return false
}

// Active reports whether the operation still owns the exclusive usage profile
// or awaits the user's confirmation of a staged credential.
func (s ClaudeLimitLoginState) Active() bool {
	return s == ClaudeLimitLoginPreparing || s == ClaudeLimitLoginWaiting || s == ClaudeLimitLoginConfirming || s == ClaudeLimitLoginRecovery
}

// ClaudeLimitIdentity is the validated OAuth identity shown for confirmation. It
// is a display assertion only: it is never matched against the execution
// account, its alias or its setup-token commitment.
type ClaudeLimitIdentity struct {
	Email        string `json:"email"`
	Organization string `json:"organization,omitempty"`
	Plan         string `json:"plan"`
	// Commitment is the keyed digest of the stable identity (email and
	// organization identifier). A refresh must reproduce it unchanged.
	Commitment string `json:"commitment"`
}

// Validate bounds the identity.
func (i ClaudeLimitIdentity) Validate() error {
	invalid := Fail(InvalidArgument, "Invalid Claude usage identity.", "Reconnect the usage account.")
	if Text(i.Email, "usage email", 512, true) != nil || Text(i.Organization, "usage organization", 512, false) != nil || len(i.Commitment) != 64 {
		return invalid
	}
	if !slices.Contains([]string{"pro", "max", "team", "enterprise"}, i.Plan) {
		return invalid
	}
	return nil
}

// ClaudeLimitOperation is the durable record of one usage login. The approval
// code, login address and credentials are never part of it.
type ClaudeLimitOperation struct {
	ID               ID                    `json:"id"`
	Epoch            ID                    `json:"epoch"`
	Generation       ID                    `json:"generation"`
	Actor            Principal             `json:"actor"`
	State            ClaudeLimitLoginState `json:"state"`
	NativeStarted    bool                  `json:"native_started"`
	CodeSubmissionID ID                    `json:"code_submission_id,omitempty"`
	CodeConsumed     bool                  `json:"code_consumed,omitempty"`
	// CredentialID is the staged claude-usage vault reference awaiting
	// confirmation. It grants no authority until the association is confirmed.
	CredentialID ID                 `json:"credential_id,omitempty"`
	Failure      ClaudeLimitFailure `json:"failure,omitempty"`
	StartedAt    time.Time          `json:"started_at"`
	ExpiresAt    time.Time          `json:"expires_at"`
}

// Active reports whether the operation still owns resources.
func (o ClaudeLimitOperation) Active() bool { return o.State.Active() }

// ClaudeLimitReadPhase is the refresh phase of the single usage-read owner.
type ClaudeLimitReadPhase string

const (
	ClaudeLimitReadIdle       ClaudeLimitReadPhase = "idle"
	ClaudeLimitReadFetching   ClaudeLimitReadPhase = "fetching"
	ClaudeLimitReadRefreshing ClaudeLimitReadPhase = "refreshing"
)

// ClaudeLimitRead is the durable single-flight marker of the read owner.
type ClaudeLimitRead struct {
	Phase     ClaudeLimitReadPhase `json:"phase,omitempty"`
	ID        ID                   `json:"id,omitempty"`
	Epoch     ID                   `json:"epoch,omitempty"`
	StartedAt *time.Time           `json:"started_at,omitempty"`
	// LastAttemptAt starts the four-minute due interval for every attempt.
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	// RefreshClaimed is written before a native refresh-login starts and is
	// cleared only by a confirmed outcome, so a restart never repeats it.
	RefreshClaimed bool `json:"refresh_claimed,omitempty"`
}

// Active reports whether a read is in flight.
func (r ClaudeLimitRead) Active() bool {
	return r.Phase == ClaudeLimitReadFetching || r.Phase == ClaudeLimitReadRefreshing
}

// ClaudeLimitState is the optional server-owned usage connection stored in the
// protected subscription metadata. Generic configuration writes and older
// clients cannot change it; it never exists for an account the user did not
// explicitly connect.
type ClaudeLimitState struct {
	// Generation changes with every new usage login. Vault references,
	// receipts and every mutation bind to it.
	Generation ID                    `json:"generation"`
	Operation  *ClaudeLimitOperation `json:"operation,omitempty"`
	// Identity is the validated OAuth identity: pending confirmation while the
	// operation confirms, retained as the linked identity afterwards.
	Identity *ClaudeLimitIdentity `json:"identity,omitempty"`
	// LinkedAt is the explicit manual association. Nil means not associated.
	LinkedAt *time.Time `json:"linked_at,omitempty"`
	// CredentialID is the current published claude-usage vault reference.
	CredentialID ID                  `json:"credential_id,omitempty"`
	Read         ClaudeLimitRead     `json:"read"`
	LastSuccess  *time.Time          `json:"last_success_at,omitempty"`
	Windows      []ClaudeLimitWindow `json:"windows,omitempty"`
	BackoffUntil *time.Time          `json:"backoff_until,omitempty"`
	Failure      ClaudeLimitFailure  `json:"failure,omitempty"`
	FailureAt    *time.Time          `json:"failure_at,omitempty"`
	// ReconnectRequired is set when only an explicit new login can repair the
	// connection. Reconnection is never automatic.
	ReconnectRequired bool `json:"reconnect_required,omitempty"`
	// CleanupPending records an unfinished removal of usage-owned storage. It
	// blocks a new login until the original removal is confirmed.
	CleanupPending bool `json:"cleanup_pending,omitempty"`
	// RetiredCredentials are claude-usage references still owed a deletion.
	RetiredCredentials []ID `json:"retired_credentials,omitempty"`
}

// Linked reports whether usage evidence may be read and published.
func (s ClaudeLimitState) Linked() bool {
	return s.LinkedAt != nil && s.CredentialID != "" && s.Identity != nil
}

// Readable reports whether a fetch may be attempted at all.
func (s ClaudeLimitState) Readable() bool {
	return s.Linked() && !s.ReconnectRequired && !s.CleanupPending && (s.Operation == nil || !s.Operation.Active())
}

// SuppressedAt reports whether a provider-requested backoff is still active.
func (s ClaudeLimitState) SuppressedAt(now time.Time) bool {
	return s.BackoffUntil != nil && now.Before(*s.BackoffUntil)
}

// DueAt reports whether the automatic cadence permits a fetch: the previous
// attempt is at least four minutes old and no backoff is active. A manual
// refresh ignores the cadence but never the backoff.
func (s ClaudeLimitState) DueAt(now time.Time) bool {
	if !s.Readable() || s.Read.Active() || s.SuppressedAt(now) {
		return false
	}
	return s.Read.LastAttemptAt == nil || now.Sub(*s.Read.LastAttemptAt) >= ClaudeLimitDueInterval
}

// ManualAllowedAt reports whether an explicit refresh may start a read.
func (s ClaudeLimitState) ManualAllowedAt(now time.Time) bool {
	return s.Readable() && !s.Read.Active() && !s.SuppressedAt(now)
}

// RecordSuccess publishes one validated nonempty fetch. Only this method
// advances the observation time and replaces the windows. A window absent from
// the observation becomes unavailable, never zero.
func (s *ClaudeLimitState) RecordSuccess(observation ClaudeLimitObservation) {
	at := observation.ObservedAt
	s.Windows = slices.Clone(observation.Windows)
	s.LastSuccess = &at
	s.Failure = ClaudeLimitFailureNone
	s.FailureAt = nil
	s.BackoffUntil = nil
}

// RecordFailure keeps the previous windows and observation time untouched and
// records only the closed failure and any suppression deadline.
func (s *ClaudeLimitState) RecordFailure(failure ClaudeLimitFailure, at time.Time, backoff time.Duration) {
	s.Failure = failure
	value := at.UTC()
	s.FailureAt = &value
	if backoff > 0 {
		until := at.Add(min(backoff, ClaudeLimitMaxBackoff)).UTC()
		s.BackoffUntil = &until
	}
	if failure.ReconnectRequired() {
		s.ReconnectRequired = true
	}
}

// ClearEvidence drops every usage observation, used when the identity or
// generation changes or the connection ends.
func (s *ClaudeLimitState) ClearEvidence() {
	s.Windows, s.LastSuccess, s.BackoffUntil, s.Failure, s.FailureAt = nil, nil, nil, ClaudeLimitFailureNone, nil
}

// Validate bounds the state for an account.
func (s ClaudeLimitState) Validate(account Account) error {
	invalid := func() error {
		return Fail(InvalidArgument, "Invalid Claude usage state.", "Use the server-owned usage connection operations.")
	}
	if !account.ManagedClaudeAccount() || s.Generation.Validate() != nil || !s.Failure.Valid() {
		return invalid()
	}
	if o := s.Operation; o != nil {
		for _, id := range []ID{o.ID, o.Epoch, o.Generation} {
			if id.Validate() != nil {
				return invalid()
			}
		}
		if o.Generation != s.Generation || !o.State.Valid() || !o.Failure.Valid() || o.StartedAt.IsZero() || !o.ExpiresAt.After(o.StartedAt) || (o.Actor.Type != OwnerDevice && o.Actor.Type != ClientDevice) || o.Actor.Type == ClientDevice && o.Actor.DeviceID.Validate() != nil {
			return invalid()
		}
		if o.CredentialID != "" && o.CredentialID.Validate() != nil || o.CodeSubmissionID != "" && o.CodeSubmissionID.Validate() != nil {
			return invalid()
		}
	}
	if s.Identity != nil && s.Identity.Validate() != nil {
		return invalid()
	}
	if s.CredentialID != "" && s.CredentialID.Validate() != nil {
		return invalid()
	}
	if s.LinkedAt != nil && (s.CredentialID == "" || s.Identity == nil || s.LinkedAt.IsZero()) {
		return invalid()
	}
	if s.LinkedAt == nil && (s.CredentialID != "" || len(s.Windows) != 0 || s.LastSuccess != nil) {
		return invalid()
	}
	if r := s.Read; r.Phase != "" && r.Phase != ClaudeLimitReadIdle && r.Phase != ClaudeLimitReadFetching && r.Phase != ClaudeLimitReadRefreshing || r.Active() && (r.ID.Validate() != nil || r.Epoch.Validate() != nil || r.StartedAt == nil) {
		return invalid()
	}
	if len(s.Windows) > len(ClaudeLimitWindowIDs) || len(s.RetiredCredentials) > 64 {
		return invalid()
	}
	seen := map[ClaudeLimitWindowID]bool{}
	for _, w := range s.Windows {
		if !w.ID.Valid() || seen[w.ID] || math.IsNaN(w.Remaining) || w.Remaining < 0 || w.Remaining > 1 || w.ResetAt != nil && (w.ResetAt.Unix() <= 0 || w.ResetAt.Year() > 9999) {
			return invalid()
		}
		seen[w.ID] = true
	}
	if len(s.Windows) != 0 && s.LastSuccess == nil {
		return invalid()
	}
	for _, id := range s.RetiredCredentials {
		if id.Validate() != nil {
			return invalid()
		}
	}
	return nil
}

// ProjectClaudeLimits rewrites the Claude-group presentation windows of the
// account from the authoritative usage state. The windows are informational
// (Blocking=false) so routing evidence never consumes them. Projection happens
// only while the connection is linked; otherwise every Claude-group window is
// removed.
func (a *Account) ProjectClaudeLimits() {
	kept := make([]QuotaWindow, 0, len(a.Quota))
	for _, w := range a.Quota {
		if w.ComparisonGroup != ClaudeLimitComparisonGroup {
			kept = append(kept, w)
		}
	}
	a.Quota = kept
	if a.Type != SubscriptionAccount || a.SubscriptionService != SubscriptionClaude || a.Subscription == nil || a.Subscription.ClaudeUsage == nil {
		return
	}
	usage := a.Subscription.ClaudeUsage
	if !usage.Linked() || usage.LastSuccess == nil {
		return
	}
	for _, w := range usage.Windows {
		remaining := w.Remaining
		duration := w.ID.DurationMinutes()
		window := QuotaWindow{ID: string(w.ID), ComparisonGroup: ClaudeLimitComparisonGroup, Blocking: false, Remaining: &remaining, DurationMinutes: &duration, ObservedAt: *usage.LastSuccess, State: Observed}
		if w.ResetAt != nil {
			reset := *w.ResetAt
			window.ResetAt = &reset
		}
		a.Quota = append(a.Quota, window)
	}
}
