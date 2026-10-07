// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/hex"
	"time"
)

type SubscriptionLoginMethod uint32

const (
	SubscriptionBrowserCallback SubscriptionLoginMethod = 1
	SubscriptionBrowserCode     SubscriptionLoginMethod = 2
)

type NativeSubscriptionPhase uint32

const (
	NativeSubscriptionDiscovery NativeSubscriptionPhase = iota + 1
	NativeSubscriptionVersion
	NativeSubscriptionRuntime
	NativeSubscriptionLaunch
	NativeSubscriptionLogin
	NativeSubscriptionStatus
	NativeSubscriptionExecution
	NativeSubscriptionHistory
	NativeSubscriptionCleanup
)

// Only original operation metadata is durable. URLs, codes, native identity
// text and authentication files are excluded from this document.
type NativeSubscriptionOperation struct {
	ID               ID                            `json:"id"`
	Actor            Principal                     `json:"actor"`
	MachineID        ID                            `json:"machine_id"`
	Epoch            ID                            `json:"epoch"`
	Action           SubscriptionAction            `json:"action"`
	State            SubscriptionLoginState        `json:"state"`
	LoginMethod      SubscriptionLoginMethod       `json:"login_method,omitempty"`
	StartedAt        time.Time                     `json:"started_at"`
	ExpiresAt        time.Time                     `json:"expires_at"`
	CodeSubmissionID ID                            `json:"code_submission_id,omitempty"`
	CodeConsumed     bool                          `json:"code_consumed,omitempty"`
	Diagnostic       *NativeSubscriptionDiagnostic `json:"diagnostic,omitempty"`
}
type NativeSubscriptionDiagnostic struct {
	DetectedVersion string                  `json:"detected_version,omitempty"`
	RequiredVersion string                  `json:"required_version"`
	Phase           NativeSubscriptionPhase `json:"phase"`
	Code            Code                    `json:"code"`
	CorrelationID   ID                      `json:"correlation_id"`
}

func (d NativeSubscriptionDiagnostic) Validate() error {
	if d.RequiredVersion != ClaudeProtocolVersion || d.DetectedVersion != "" && !ValidInstallationVersion(d.DetectedVersion) || d.Phase < NativeSubscriptionDiscovery || d.Phase > NativeSubscriptionCleanup || d.CorrelationID.Validate() != nil {
		return Fail(InvalidArgument, "Invalid native subscription diagnostic.", "Keep only the original closed native failure metadata.")
	}
	switch d.Code {
	case NotFound, Unsupported, Unavailable, PermissionDenied, Unauthenticated, RecoveryRequired, Canceled, Conflict, InvalidArgument, Internal, ResourceExhausted:
		return nil
	}
	return Fail(InvalidArgument, "Invalid native subscription failure code.", "Use the closed product failure codes.")
}
func NativeIdentityCommitmentValid(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}
func (s SubscriptionState) validateNativeClaude(account Account) error {
	invalid := func() error {
		return Fail(InvalidArgument, "Invalid native Claude subscription ownership.", "Preserve the original Runner, profile, generation and operation.")
	}
	if account.SubscriptionService != SubscriptionClaude {
		if s.NativeProfileID != "" || s.NativeOperation != nil {
			return invalid()
		}
		return nil
	}
	if s.ServerOperation != nil || s.Observation != nil || s.ResetCredits != nil || s.QuotaState != "" || s.QuotaObservedAt != nil || s.SpendControlReached != nil || s.SpendControlObservedAt != nil {
		return invalid()
	}
	if (s.NativeProfileID == "") != (s.OwnerMachineID == "") {
		return invalid()
	}
	if s.NativeProfileID != "" && (s.NativeProfileID.Validate() != nil || s.OwnerMachineID.Validate() != nil) || s.Generation != "" && (s.NativeProfileID == "" || !NativeIdentityCommitmentValid(s.IdentityCommitment)) {
		return invalid()
	}
	if s.Lease != nil && (s.NativeProfileID == "" || s.Lease.MachineID != s.OwnerMachineID) || s.Pending != nil && (s.Pending.MachineID != s.OwnerMachineID || s.NativeOperation == nil || s.Pending.ID != s.NativeOperation.ID || s.Pending.Actor != s.NativeOperation.Actor || s.Pending.Action != s.NativeOperation.Action) {
		return invalid()
	}
	if o := s.NativeOperation; o != nil {
		if o.ID.Validate() != nil || o.MachineID.Validate() != nil || o.Epoch.Validate() != nil || !o.State.Valid() || o.StartedAt.IsZero() || !o.ExpiresAt.After(o.StartedAt) || o.ExpiresAt.Sub(o.StartedAt) > 15*time.Minute || (o.Actor.Type != OwnerDevice && o.Actor.Type != ClientDevice) || o.Actor.Type == ClientDevice && o.Actor.DeviceID.Validate() != nil || (o.Action != SubscriptionLogin && o.Action != SubscriptionRefresh && o.Action != SubscriptionLogout) {
			return invalid()
		}
		if o.LoginMethod != 0 && o.LoginMethod != SubscriptionBrowserCode && o.LoginMethod != SubscriptionBrowserCallback || o.CodeSubmissionID != "" && o.CodeSubmissionID.Validate() != nil || o.CodeConsumed && o.CodeSubmissionID == "" || o.CodeSubmissionID != "" && o.LoginMethod != SubscriptionBrowserCode {
			return invalid()
		}
		if o.Diagnostic != nil && (o.Diagnostic.Validate() != nil || o.Diagnostic.CorrelationID != o.ID) {
			return invalid()
		}
		if (o.State == SubscriptionPreparing || o.State == SubscriptionWaiting) && (s.Pending == nil || o.MachineID != s.OwnerMachineID || s.NativeProfileID == "") {
			return invalid()
		}
	}
	return nil
}
