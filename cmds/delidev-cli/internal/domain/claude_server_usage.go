// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"slices"
	"time"
	"unicode"
	"unicode/utf8"
)

// ClaudeUsageConnection is the closed server-owned lifecycle of the optional
// usage connection of one server-token Claude account. The connection is
// independent of the execution setup token: it can only fetch usage, and its
// credentials never enter execution delivery.
type ClaudeUsageConnection string

const (
	// ClaudeUsageConnecting means one official login is active in the exclusive
	// usage profile. Nothing is linked or scheduled.
	ClaudeUsageConnecting ClaudeUsageConnection = "connecting"
	// ClaudeUsageConfirming means native completion was joined and the identity
	// was validated. The staged credential is not published until the user links.
	ClaudeUsageConfirming ClaudeUsageConnection = "confirming"
	// ClaudeUsageLinked means the user explicitly linked the reviewed identity
	// and reads may be scheduled.
	ClaudeUsageLinked ClaudeUsageConnection = "linked"
	// ClaudeUsageReconnectRequired keeps the last evidence visible but revokes
	// reads until the user connects again.
	ClaudeUsageReconnectRequired ClaudeUsageConnection = "reconnect-required"
)

// Valid reports whether v is one closed lifecycle value.
func (v ClaudeUsageConnection) Valid() bool {
	switch v {
	case ClaudeUsageConnecting, ClaudeUsageConfirming, ClaudeUsageLinked, ClaudeUsageReconnectRequired:
		return true
	}
	return false
}

// ClaudeUsageRefreshPhase is the durable phase of the single usage-read owner.
type ClaudeUsageRefreshPhase string

const (
	ClaudeUsageIdle ClaudeUsageRefreshPhase = ""
	// ClaudeUsageReading marks one claimed provider read.
	ClaudeUsageReading ClaudeUsageRefreshPhase = "reading"
	// ClaudeUsageRenewing marks one supervised native refresh-login.
	ClaudeUsageRenewing ClaudeUsageRefreshPhase = "renewing"
	// ClaudeUsageRotationUncertain marks an ambiguous credential rotation. The
	// account retains recovery state and never repeats an exchange.
	ClaudeUsageRotationUncertain ClaudeUsageRefreshPhase = "rotation-uncertain"
)

// Valid reports whether v is one closed phase value.
func (v ClaudeUsageRefreshPhase) Valid() bool {
	switch v {
	case ClaudeUsageIdle, ClaudeUsageReading, ClaudeUsageRenewing, ClaudeUsageRotationUncertain:
		return true
	}
	return false
}

// ClaudeUsageFailure is the closed reason of the latest failed read. It carries
// no provider text.
type ClaudeUsageFailure string

const (
	ClaudeUsageFailureNone            ClaudeUsageFailure = ""
	ClaudeUsageFailureRateLimited     ClaudeUsageFailure = "rate-limited"
	ClaudeUsageFailureForbidden       ClaudeUsageFailure = "forbidden"
	ClaudeUsageFailureUnauthorized    ClaudeUsageFailure = "unauthorized"
	ClaudeUsageFailureUnavailable     ClaudeUsageFailure = "unavailable"
	ClaudeUsageFailureMalformed       ClaudeUsageFailure = "malformed"
	ClaudeUsageFailureEmpty           ClaudeUsageFailure = "empty"
	ClaudeUsageFailureIdentityChanged ClaudeUsageFailure = "identity-changed"
	ClaudeUsageFailureUnsupported     ClaudeUsageFailure = "unsupported"
)

// Valid reports whether v is one closed failure value.
func (v ClaudeUsageFailure) Valid() bool {
	switch v {
	case ClaudeUsageFailureNone, ClaudeUsageFailureRateLimited, ClaudeUsageFailureForbidden, ClaudeUsageFailureUnauthorized, ClaudeUsageFailureUnavailable, ClaudeUsageFailureMalformed, ClaudeUsageFailureEmpty, ClaudeUsageFailureIdentityChanged, ClaudeUsageFailureUnsupported:
		return true
	}
	return false
}

// ClaudeUsageIdentity is the validated OAuth identity the user reviewed. It is
// never compared with the execution setup token, which carries no identity.
type ClaudeUsageIdentity struct {
	Email        string `json:"email"`
	Organization string `json:"organization,omitempty"`
	Plan         string `json:"plan,omitempty"`
	// Commitment is the keyed SHA-256 commitment of the reviewed identity.
	Commitment string `json:"commitment"`
}

// Validate bounds the identity text and requires a well-formed commitment.
func (i ClaudeUsageIdentity) Validate() error {
	if !boundedClaudeUsageText(i.Email, 320, true) || !boundedClaudeUsageText(i.Organization, 256, false) || !boundedClaudeUsageText(i.Plan, 64, false) || !NativeIdentityCommitmentValid(i.Commitment) {
		return invalidClaudeServerUsage()
	}
	return nil
}

func boundedClaudeUsageText(value string, max int, required bool) bool {
	if !utf8.ValidString(value) || len(value) > max || required && value == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ClaudeUsageOperation is the durable metadata of one usage login. URLs,
// approval codes, native bodies and credential bytes are never stored here.
type ClaudeUsageOperation struct {
	ID               ID                            `json:"id"`
	Actor            Principal                     `json:"actor"`
	Epoch            ID                            `json:"epoch"`
	State            SubscriptionLoginState        `json:"state"`
	StartedAt        time.Time                     `json:"started_at"`
	ExpiresAt        time.Time                     `json:"expires_at"`
	CodeSubmissionID ID                            `json:"code_submission_id,omitempty"`
	CodeConsumed     bool                          `json:"code_consumed,omitempty"`
	Diagnostic       *NativeSubscriptionDiagnostic `json:"diagnostic,omitempty"`
}

// MaxClaudeUsageLoginWindow bounds one usage login, like the setup-token login.
const MaxClaudeUsageLoginWindow = 15 * time.Minute

// Validate checks the closed operation shape.
func (o ClaudeUsageOperation) Validate() error {
	for _, id := range []ID{o.ID, o.Epoch} {
		if id.Validate() != nil {
			return invalidClaudeServerUsage()
		}
	}
	if !o.State.Valid() || !validClaudeUsageTime(o.StartedAt) || !o.ExpiresAt.After(o.StartedAt) || o.ExpiresAt.Sub(o.StartedAt) > MaxClaudeUsageLoginWindow || o.ExpiresAt.Year() > 9999 {
		return invalidClaudeServerUsage()
	}
	if o.Actor.Type != OwnerDevice && o.Actor.Type != ClientDevice || o.Actor.Type == ClientDevice && o.Actor.DeviceID.Validate() != nil {
		return invalidClaudeServerUsage()
	}
	if o.CodeSubmissionID != "" && o.CodeSubmissionID.Validate() != nil || o.CodeConsumed && o.CodeSubmissionID == "" {
		return invalidClaudeServerUsage()
	}
	if d := o.Diagnostic; d != nil && (d.Validate() != nil || d.CorrelationID != o.ID || o.State == SubscriptionSucceeded || o.State == SubscriptionPreparing || o.State == SubscriptionWaiting) {
		return invalidClaudeServerUsage()
	}
	return nil
}

// Active reports whether native login work may still be running.
func (o ClaudeUsageOperation) Active() bool {
	return o.State == SubscriptionPreparing || o.State == SubscriptionWaiting || o.State == SubscriptionRecovery
}

// ClaudeUsageWindowEvidence is one last-success window. ObservedAt is the time
// of the successful provider read that produced it, never advanced by failures.
type ClaudeUsageWindowEvidence struct {
	ID         ClaudeUsageWindowID `json:"id"`
	Remaining  float64             `json:"remaining"`
	ResetAt    *time.Time          `json:"reset_at,omitempty"`
	ObservedAt time.Time           `json:"observed_at"`
}

// MaxClaudeUsageRetiredCredentials bounds credential references that still owe
// vault deletion after a failed or uncertain operation.
const MaxClaudeUsageRetiredCredentials = 16

// ClaudeServerUsageState is the optional server-owned usage section of a
// server-token Claude account. Historical account JSON omits it. Generic
// configuration writes and older clients cannot change it, and it needs no
// SQLite migration because it lives in the protected subscription metadata.
type ClaudeServerUsageState struct {
	Connection ClaudeUsageConnection `json:"connection"`
	// Generation identifies one connection attempt. Every protected credential,
	// receipt and read is bound to it.
	Generation ID                    `json:"generation"`
	Operation  *ClaudeUsageOperation `json:"operation,omitempty"`
	Identity   *ClaudeUsageIdentity  `json:"identity,omitempty"`
	// CredentialID is the claude-usage vault generation of the credential bundle.
	// It is staged until the user links, then Published.
	CredentialID ID         `json:"credential_id,omitempty"`
	Published    bool       `json:"published,omitempty"`
	LinkedAt     *time.Time `json:"linked_at,omitempty"`

	Refresh       ClaudeUsageRefreshPhase     `json:"refresh,omitempty"`
	Failure       ClaudeUsageFailure          `json:"failure,omitempty"`
	LastSuccessAt *time.Time                  `json:"last_success_at,omitempty"`
	BackoffUntil  *time.Time                  `json:"backoff_until,omitempty"`
	Windows       []ClaudeUsageWindowEvidence `json:"windows,omitempty"`
	// RetiredCredentials are protected references whose deletion is still owed.
	RetiredCredentials []ID `json:"retired_credentials,omitempty"`
}

func invalidClaudeServerUsage() *Error {
	return Fail(InvalidArgument, "Invalid server-owned Claude usage state.", "Preserve the original usage generation, operation and protected references.")
}

// Reading reports whether usage reads are currently authorized.
func (s ClaudeServerUsageState) Reading() bool {
	return s.Connection == ClaudeUsageLinked && s.Published && s.Identity != nil
}

// Validate checks the closed shape of the usage section of account.
func (s ClaudeServerUsageState) Validate(account Account) error {
	invalid := invalidClaudeServerUsage
	if !s.Connection.Valid() || s.Generation.Validate() != nil || !s.Refresh.Valid() || !s.Failure.Valid() {
		return invalid()
	}
	// A disconnected execution account cannot hold live usage authority. Only
	// retired references may outlive the account connection.
	if account.Connection == nil && s.Connection != ClaudeUsageReconnectRequired {
		return invalid()
	}
	if o := s.Operation; o != nil {
		if o.Validate() != nil {
			return invalid()
		}
	}
	if s.Identity != nil && s.Identity.Validate() != nil {
		return invalid()
	}
	if s.CredentialID != "" && s.CredentialID.Validate() != nil || s.LinkedAt != nil && !validClaudeUsageTime(*s.LinkedAt) {
		return invalid()
	}
	for _, at := range []*time.Time{s.LastSuccessAt, s.BackoffUntil} {
		if at != nil && !validClaudeUsageTime(*at) {
			return invalid()
		}
	}
	switch s.Connection {
	case ClaudeUsageConnecting:
		if s.Operation == nil || s.Operation.State == SubscriptionSucceeded || s.Identity != nil || s.CredentialID != "" || s.Published || s.LinkedAt != nil || s.Refresh != ClaudeUsageIdle || s.Failure != ClaudeUsageFailureNone || s.LastSuccessAt != nil || s.BackoffUntil != nil || len(s.Windows) != 0 {
			return invalid()
		}
	case ClaudeUsageConfirming:
		if s.Operation == nil || s.Operation.State != SubscriptionSucceeded || s.Identity == nil || s.CredentialID == "" || s.Published || s.LinkedAt != nil || s.Refresh != ClaudeUsageIdle || s.Failure != ClaudeUsageFailureNone || s.LastSuccessAt != nil || s.BackoffUntil != nil || len(s.Windows) != 0 {
			return invalid()
		}
	case ClaudeUsageLinked:
		if s.Identity == nil || s.CredentialID == "" || !s.Published || s.LinkedAt == nil || s.Operation != nil && s.Operation.State != SubscriptionSucceeded {
			return invalid()
		}
	case ClaudeUsageReconnectRequired:
		if s.CredentialID != "" || s.Published || s.BackoffUntil != nil || s.Refresh != ClaudeUsageIdle && s.Refresh != ClaudeUsageRotationUncertain || s.Operation != nil && s.Operation.Active() {
			return invalid()
		}
	}
	if s.Refresh != ClaudeUsageIdle && s.Connection != ClaudeUsageLinked && s.Connection != ClaudeUsageReconnectRequired {
		return invalid()
	}
	if s.BackoffUntil != nil && !claudeUsageFailureAllowsBackoff(s.Failure) {
		return invalid()
	}
	if len(s.Windows) > len(ClaudeUsageWindowIDs()) || (len(s.Windows) != 0) != (s.LastSuccessAt != nil) {
		return invalid()
	}
	seen := map[ClaudeUsageWindowID]bool{}
	for _, w := range s.Windows {
		if !w.ID.Valid() || seen[w.ID] || !(w.Remaining >= 0 && w.Remaining <= 1) || !validClaudeUsageTime(w.ObservedAt) || w.ObservedAt.After(*s.LastSuccessAt) || w.ResetAt != nil && !validClaudeUsageTime(*w.ResetAt) {
			return invalid()
		}
		seen[w.ID] = true
	}
	if len(s.RetiredCredentials) > MaxClaudeUsageRetiredCredentials {
		return invalid()
	}
	retired := map[ID]bool{}
	for _, id := range s.RetiredCredentials {
		if id.Validate() != nil || retired[id] || id == s.CredentialID {
			return invalid()
		}
		retired[id] = true
	}
	return nil
}

// claudeUsageFailureAllowsBackoff reports whether failure is one of the closed
// provider failures that schedule a retry deadline.
func claudeUsageFailureAllowsBackoff(failure ClaudeUsageFailure) bool {
	return failure == ClaudeUsageFailureRateLimited || failure == ClaudeUsageFailureForbidden || failure == ClaudeUsageFailureUnavailable
}

// RecordFailure keeps every last-success window and time and records only the
// closed failure and the retry deadline. A failure never advances an
// observation time and never turns an available window into zero. The pair is
// validated before any mutation: only a rate-limited, forbidden or unavailable
// failure may carry a backoff deadline, and a deadline must be a valid time, so
// a handled provider failure can never produce state that fails persistence.
func (s *ClaudeServerUsageState) RecordFailure(failure ClaudeUsageFailure, backoffUntil *time.Time) error {
	if s.Connection != ClaudeUsageLinked || failure == ClaudeUsageFailureNone || !failure.Valid() {
		return invalidClaudeServerUsage()
	}
	if backoffUntil != nil && (!claudeUsageFailureAllowsBackoff(failure) || !validClaudeUsageTime(*backoffUntil)) {
		return invalidClaudeServerUsage()
	}
	s.Failure = failure
	s.BackoffUntil = nil
	if backoffUntil != nil {
		value := backoffUntil.UTC()
		s.BackoffUntil = &value
	}
	return nil
}

// ApplyClaudeUsageQuota merges one validated nonempty usage observation into the
// linked usage state of a and projects the windows into a.Quota as non-blocking
// Claude comparison-group evidence. Sparse windows are merged independently:
// a window missing from the observation keeps its previous evidence and
// observation time, so it can age to stale but is never refreshed or zeroed.
// An observation older than the last success is ignored and reports false. It
// returns an error for an unlinked account or an observation from the future.
func ApplyClaudeUsageQuota(a *Account, generation ID, v ClaudeUsageQuota, now time.Time) (bool, error) {
	if a == nil || a.Type != SubscriptionAccount || a.SubscriptionService != SubscriptionClaude || a.Subscription == nil || a.Subscription.ClaudeUsage == nil {
		return false, invalidClaudeServerUsage()
	}
	state := a.Subscription.ClaudeUsage
	if !state.Reading() || state.Generation != generation || len(v.Windows) == 0 || len(v.Windows) > len(ClaudeUsageWindowIDs()) || !validClaudeUsageTime(v.ObservedAt) || v.ObservedAt.After(now.Add(time.Second)) {
		return false, invalidClaudeServerUsage()
	}
	seen := map[ClaudeUsageWindowID]bool{}
	for _, w := range v.Windows {
		if !w.ID.Valid() || seen[w.ID] || !(w.Remaining >= 0 && w.Remaining <= 1) || w.ResetAt != nil && !validClaudeUsageTime(*w.ResetAt) {
			return false, invalidClaudeServerUsage()
		}
		seen[w.ID] = true
	}
	if state.LastSuccessAt != nil && v.ObservedAt.Before(*state.LastSuccessAt) {
		return false, nil
	}
	for _, incoming := range v.Windows {
		evidence := ClaudeUsageWindowEvidence{ID: incoming.ID, Remaining: incoming.Remaining, ObservedAt: v.ObservedAt.UTC()}
		if incoming.ResetAt != nil {
			value := incoming.ResetAt.UTC()
			evidence.ResetAt = &value
		}
		if index := slices.IndexFunc(state.Windows, func(w ClaudeUsageWindowEvidence) bool { return w.ID == incoming.ID }); index >= 0 {
			state.Windows[index] = evidence
		} else {
			state.Windows = append(state.Windows, evidence)
		}
	}
	slices.SortFunc(state.Windows, func(x, y ClaudeUsageWindowEvidence) int {
		return slices.Index(ClaudeUsageWindowIDs(), x.ID) - slices.Index(ClaudeUsageWindowIDs(), y.ID)
	})
	observed := v.ObservedAt.UTC()
	state.LastSuccessAt = &observed
	state.Failure = ClaudeUsageFailureNone
	state.BackoffUntil = nil
	a.Quota = projectClaudeUsageQuota(a.Quota, state.Windows)
	return true, nil
}

// projectClaudeUsageQuota replaces only the Claude comparison-group usage
// windows of quota. Every projected window is non-blocking, so usage evidence
// never routes or blocks Claude execution.
func projectClaudeUsageQuota(quota []QuotaWindow, windows []ClaudeUsageWindowEvidence) []QuotaWindow {
	projected := slices.DeleteFunc(slices.Clone(quota), func(w QuotaWindow) bool {
		return w.ComparisonGroup == ClaudeUsageComparisonGroup && ClaudeUsageWindowID(w.ID).Valid()
	})
	for _, w := range windows {
		remaining := w.Remaining
		duration := w.ID.DurationMinutes()
		window := QuotaWindow{ID: string(w.ID), ComparisonGroup: ClaudeUsageComparisonGroup, Blocking: false, DurationMinutes: &duration, Remaining: &remaining, ObservedAt: w.ObservedAt, State: Observed}
		if w.ResetAt != nil {
			value := *w.ResetAt
			window.ResetAt = &value
		}
		projected = append(projected, window)
	}
	return projected
}

// Retire revokes all live usage authority and moves the section to
// reconnect-required. Reads and scheduling stop immediately: the credential is
// unpublished, an active login is marked canceled, an in-flight read or renewal
// is fenced and the backoff is dropped. The last evidence stays visible and an
// ambiguous rotation keeps its recovery phase. Every credential reference that
// still owes vault deletion moves to the retired list so cleanup evidence is
// never lost. It fails closed, without mutation, when the bounded retired list
// cannot hold the references. Cancelling and joining a running native login is
// the caller's duty before the retirement is committed.
func (s *ClaudeServerUsageState) Retire() error {
	retired := slices.Clone(s.RetiredCredentials)
	if s.CredentialID != "" && !slices.Contains(retired, s.CredentialID) {
		retired = append(retired, s.CredentialID)
	}
	if len(retired) > MaxClaudeUsageRetiredCredentials {
		return invalidClaudeServerUsage()
	}
	s.RetiredCredentials = retired
	s.Connection = ClaudeUsageReconnectRequired
	s.CredentialID = ""
	s.Published = false
	s.BackoffUntil = nil
	if s.Refresh != ClaudeUsageRotationUncertain {
		s.Refresh = ClaudeUsageIdle
	}
	if s.Operation != nil && s.Operation.Active() {
		operation := *s.Operation
		operation.State = SubscriptionCanceled
		s.Operation = &operation
	}
	return nil
}

// RetireClaudeUsage retires the usage section of a, if any, and removes its
// projected quota evidence. An account without usage state is unchanged. It is
// the transition for every path that disconnects the execution account, such as
// an explicit logout or a database restore.
func RetireClaudeUsage(a *Account) error {
	if a == nil || a.Subscription == nil || a.Subscription.ClaudeUsage == nil {
		return nil
	}
	if err := a.Subscription.ClaudeUsage.Retire(); err != nil {
		return err
	}
	ClearClaudeUsageQuota(a)
	return nil
}

// ClearClaudeUsageQuota removes the projected usage windows of a. It is used
// when the usage connection is retired.
func ClearClaudeUsageQuota(a *Account) {
	a.Quota = slices.DeleteFunc(a.Quota, func(w QuotaWindow) bool {
		return w.ComparisonGroup == ClaudeUsageComparisonGroup && ClaudeUsageWindowID(w.ID).Valid()
	})
}
