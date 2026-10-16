// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"time"

	"github.com/google/uuid"
)

// SubscriptionRecoveryBlockedReason is the closed vocabulary that explains why a
// fenced managed ChatGPT execution lease stays protected. It is exposed through
// the account document and carries no path, credential or native output.
type SubscriptionRecoveryBlockedReason string

const (
	// RecoveryCredentialProofUnavailable marks legacy evidence that cannot prove
	// the latest credentials or their cleanup. No receipt is fabricated.
	RecoveryCredentialProofUnavailable SubscriptionRecoveryBlockedReason = "credential-proof-unavailable"
	RecoveryCleanupUnconfirmed         SubscriptionRecoveryBlockedReason = "cleanup-unconfirmed"
	RecoveryOwnershipEvidenceInvalid   SubscriptionRecoveryBlockedReason = "ownership-evidence-invalid"
	RecoveryMembersUnsettled           SubscriptionRecoveryBlockedReason = "members-unsettled"
	RecoveryCredentialUsersActive      SubscriptionRecoveryBlockedReason = "credential-users-active"
	RecoveryActiveOwner                SubscriptionRecoveryBlockedReason = "active-owner"
)

// SubscriptionRecoveryBundleRejectedCause marks the one capture refusal that
// positively rejects the presented bundle itself: it is not the delivered
// generation or a refresh of it, it is unparsable or it names another identity.
// Every other refusal (binding, authorization, capability or lease evidence) says
// nothing about the bundle, so a Worker must keep its retained file for a retry.
const SubscriptionRecoveryBundleRejectedCause = "subscription_recovery_bundle_rejected"

func (r SubscriptionRecoveryBlockedReason) Valid() bool {
	switch r {
	case RecoveryCredentialProofUnavailable, RecoveryCleanupUnconfirmed, RecoveryOwnershipEvidenceInvalid, RecoveryMembersUnsettled, RecoveryCredentialUsersActive, RecoveryActiveOwner:
		return true
	}
	return false
}

// SubscriptionRecoveryOutcome is the closed settlement result of one recovery.
type SubscriptionRecoveryOutcome string

const (
	// RecoveryRestored publishes one immutable generation and restores readiness.
	RecoveryRestored SubscriptionRecoveryOutcome = "restored"
	// RecoveryDisconnected retires every original reference and settles the
	// account disconnected so the existing explicit login can run.
	RecoveryDisconnected SubscriptionRecoveryOutcome = "disconnected"
)

func (o SubscriptionRecoveryOutcome) Valid() bool {
	return o == RecoveryRestored || o == RecoveryDisconnected
}

// SubscriptionRecoveryCapture is the non-secret metadata of the server-vault
// protected capture of a lease's latest validated bundle. The secret bytes live
// only in the vault under ID (latest) and OriginalID (the unused original the
// lease took). The capture grants no readiness, Take, quota access or
// redistribution and is retired with the lease it belongs to.
type SubscriptionRecoveryCapture struct {
	ID                 ID        `json:"id"`
	Lease              ID        `json:"lease"`
	Generation         ID        `json:"generation"`
	IdentityCommitment string    `json:"identity_commitment"`
	SyncedAt           time.Time `json:"synced_at"`
	// OriginalCaptured reports whether the unused original bundle was captured
	// under OriginalID. A capture of legacy retained authentication carries only
	// the latest bundle.
	OriginalCaptured bool `json:"original_captured,omitempty"`
}

// OriginalID is the deterministic vault reference of the captured original
// bundle. It is derived from the capture identity so a replayed capture request
// always addresses the same two references.
func (c SubscriptionRecoveryCapture) OriginalID() ID { return RecoveryOriginalReference(c.ID) }

// RecoveryOriginalReference derives the vault reference identifier that holds
// the original bundle of one capture.
func RecoveryOriginalReference(capture ID) ID {
	parsed, err := uuid.Parse(string(capture))
	if err != nil {
		return ""
	}
	parsed[15] ^= 0xff
	return ID(parsed.String())
}

func (c SubscriptionRecoveryCapture) Validate() error {
	if c.ID.Validate() != nil || c.Lease.Validate() != nil || c.Generation.Validate() != nil || len(c.IdentityCommitment) != 64 || c.SyncedAt.IsZero() || c.OriginalID().Validate() != nil {
		return Fail(InvalidArgument, "Invalid managed subscription recovery capture.", "Use server-owned subscription operations to change recovery evidence.")
	}
	return nil
}

// SubscriptionRecoverySettlement is the durable checkpoint of one recovery whose
// credential decision was committed but whose protected-reference retirement is
// not yet confirmed. The account stays fenced until the retirement finishes, so
// no execution or login can be admitted while original references remain.
type SubscriptionRecoverySettlement struct {
	Request    ID                          `json:"request"`
	Lease      ID                          `json:"lease"`
	Machine    ID                          `json:"machine"`
	Device     ID                          `json:"device"`
	Outcome    SubscriptionRecoveryOutcome `json:"outcome"`
	Generation ID                          `json:"generation,omitempty"`
	StartedAt  time.Time                   `json:"started_at"`
}

func (s SubscriptionRecoverySettlement) Validate() error {
	invalid := Fail(InvalidArgument, "Invalid managed subscription recovery settlement.", "Use server-owned subscription operations to change recovery evidence.")
	if s.Request.Validate() != nil || s.Lease.Validate() != nil || s.Machine.Validate() != nil || s.Device.Validate() != nil || !s.Outcome.Valid() || s.StartedAt.IsZero() {
		return invalid
	}
	if s.Outcome == RecoveryRestored && s.Generation.Validate() != nil || s.Outcome == RecoveryDisconnected && s.Generation != "" {
		return invalid
	}
	return nil
}

// SubscriptionRecoveryBlock records the latest reason a recovery attempt left the
// account protected. It is observation only and never authorizes anything.
type SubscriptionRecoveryBlock struct {
	Reason     SubscriptionRecoveryBlockedReason `json:"reason"`
	Request    ID                                `json:"request"`
	ObservedAt time.Time                         `json:"observed_at"`
}

func (b SubscriptionRecoveryBlock) Validate() error {
	if !b.Reason.Valid() || b.Request.Validate() != nil || b.ObservedAt.IsZero() {
		return Fail(InvalidArgument, "Invalid managed subscription recovery observation.", "Use server-owned subscription operations to change recovery evidence.")
	}
	return nil
}

// validateRecovery checks the managed ChatGPT recovery extension of a state.
func (s SubscriptionState) validateRecovery(account Account) error {
	invalid := Fail(InvalidArgument, "Invalid managed subscription ownership.", "Use server-owned subscription operations to change leases or credential generations.")
	if s.RecoveryCapture == nil && s.RecoverySettlement == nil && s.RecoveryBlock == nil {
		return nil
	}
	if account.SubscriptionService != SubscriptionChatGPT {
		return invalid
	}
	if c := s.RecoveryCapture; c != nil {
		if c.Validate() != nil || s.Lease == nil || s.Lease.ID != c.Lease || s.Lease.Action != SubscriptionExecute || s.Lease.Generation != c.Generation {
			return invalid
		}
	}
	if st := s.RecoverySettlement; st != nil {
		if st.Validate() != nil || !s.RecoveryRequired || s.Lease != nil || s.RecoveryCapture != nil || st.Outcome == RecoveryRestored && s.Generation != st.Generation || st.Outcome == RecoveryDisconnected && account.Connection != nil {
			return invalid
		}
	}
	if b := s.RecoveryBlock; b != nil && (b.Validate() != nil || !s.RecoveryRequired || s.Lease == nil) {
		return invalid
	}
	return nil
}
