// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"slices"
	"time"
)

// This file owns the closed login lifecycle transitions of the optional
// server-owned Claude usage connection. Every transition validates its source
// state and mutates nothing when it fails, so a caller can persist the result
// only after the whole section still validates.
//
//	(none | reconnect-required) --NewClaudeUsageLogin--> connecting
//	connecting --Stage--> confirming --Link--> linked
//	connecting --Settle--> reconnect-required
//	confirming --DiscardConfirmation--> reconnect-required
//	any        --Disconnect--> reconnect-required (evidence cleared)

// OwesCleanup reports whether the section still owes vault deletion of a
// credential reference or removal of native credential storage in the usage
// profile. A section that owes cleanup is never silently dropped.
func (s ClaudeServerUsageState) OwesCleanup() bool {
	return len(s.RetiredCredentials) != 0 || s.ProfileCleanup
}

// NewClaudeUsageLogin begins one usage login. previous is the current section
// (nil when the account never connected usage). A new login is only allowed
// from no section or from a settled reconnect-required section that owes no
// native storage removal: a leftover credential in the usage profile must never
// be mistaken for the new login's result. Retired credential references that
// still owe vault deletion are carried over. Evidence of the previous
// connection is cleared because the generation changes.
func NewClaudeUsageLogin(previous *ClaudeServerUsageState, generation ID, operation ClaudeUsageOperation) (*ClaudeServerUsageState, error) {
	if generation.Validate() != nil || operation.Validate() != nil || operation.State != SubscriptionPreparing || operation.CodeSubmissionID != "" || operation.Diagnostic != nil {
		return nil, invalidClaudeServerUsage()
	}
	state := &ClaudeServerUsageState{Connection: ClaudeUsageConnecting, Generation: generation, Operation: &operation, ProfileCleanup: true}
	if previous != nil {
		if previous.Connection != ClaudeUsageReconnectRequired || previous.ProfileCleanup || previous.Operation != nil && previous.Operation.Active() || previous.Generation == generation {
			return nil, invalidClaudeServerUsage()
		}
		state.RetiredCredentials = slices.Clone(previous.RetiredCredentials)
	}
	return state, nil
}

// MarkWaiting records that the original login published its authorization
// address.
func (s *ClaudeServerUsageState) MarkWaiting() error {
	if s.Connection != ClaudeUsageConnecting || s.Operation == nil || s.Operation.State != SubscriptionPreparing {
		return invalidClaudeServerUsage()
	}
	operation := *s.Operation
	operation.State = SubscriptionWaiting
	s.Operation = &operation
	return nil
}

// ClaimCode records the durable single-use claim of one approval-code
// submission. The code itself is never part of the metadata.
func (s *ClaudeServerUsageState) ClaimCode(submission ID) error {
	if s.Connection != ClaudeUsageConnecting || s.Operation == nil || s.Operation.State != SubscriptionWaiting || s.Operation.CodeSubmissionID != "" || submission.Validate() != nil {
		return invalidClaudeServerUsage()
	}
	operation := *s.Operation
	operation.CodeSubmissionID = submission
	s.Operation = &operation
	return nil
}

// ConsumeCode records that the claimed approval code is about to reach the
// original child. A second consumption is refused, so an uncertain step can
// never deliver the same code twice.
func (s *ClaudeServerUsageState) ConsumeCode() error {
	if s.Connection != ClaudeUsageConnecting || s.Operation == nil || s.Operation.State != SubscriptionWaiting || s.Operation.CodeSubmissionID == "" || s.Operation.CodeConsumed {
		return invalidClaudeServerUsage()
	}
	operation := *s.Operation
	operation.CodeConsumed = true
	s.Operation = &operation
	return nil
}

// RecordCredentialIntent durably records the vault reference that the login is
// about to stage, before the vault is written. Whatever happens next (an
// uncertain write, a crash or a discarded login), the reference stays owed for
// deletion until Stage publishes it into the section.
func (s *ClaudeServerUsageState) RecordCredentialIntent(id ID) error {
	if s.Connection != ClaudeUsageConnecting || s.Operation == nil || !s.Operation.Active() || id.Validate() != nil || slices.Contains(s.RetiredCredentials, id) || len(s.RetiredCredentials) >= MaxClaudeUsageRetiredCredentials {
		return invalidClaudeServerUsage()
	}
	s.RetiredCredentials = append(slices.Clone(s.RetiredCredentials), id)
	return nil
}

// Stage moves a joined native login into the identity-review state. The staged
// credential was recorded by RecordCredentialIntent and is now owned by the
// section instead of the retirement list. Nothing is linked or scheduled.
// profileCleaned reports that the native credential storage of the usage
// profile was proven removed after the capture. The identity review gets its
// own bounded window starting at now, so a late browser approval can still be
// reviewed.
func (s *ClaudeServerUsageState) Stage(identity ClaudeUsageIdentity, credential ID, profileCleaned bool, now time.Time) error {
	if s.Connection != ClaudeUsageConnecting || s.Operation == nil || s.Operation.State != SubscriptionWaiting || identity.Validate() != nil || credential.Validate() != nil || !slices.Contains(s.RetiredCredentials, credential) || !validClaudeUsageTime(now) {
		return invalidClaudeServerUsage()
	}
	operation := *s.Operation
	operation.State = SubscriptionSucceeded
	operation.Diagnostic = nil
	operation.StartedAt = now.UTC()
	operation.ExpiresAt = now.UTC().Add(MaxClaudeUsageLoginWindow)
	s.Operation = &operation
	s.Connection = ClaudeUsageConfirming
	s.Identity = &identity
	s.CredentialID = credential
	s.RetiredCredentials = slices.DeleteFunc(slices.Clone(s.RetiredCredentials), func(id ID) bool { return id == credential })
	s.ProfileCleanup = !profileCleaned
	return nil
}

// Link publishes the reviewed staged credential. The caller has already
// compared the echoed identity commitment and proven the vault generation.
func (s *ClaudeServerUsageState) Link(now time.Time) error {
	if s.Connection != ClaudeUsageConfirming || s.Identity == nil || s.CredentialID == "" || s.Published || !validClaudeUsageTime(now) {
		return invalidClaudeServerUsage()
	}
	linked := now.UTC()
	s.Connection = ClaudeUsageLinked
	s.Published = true
	s.LinkedAt = &linked
	s.Refresh = ClaudeUsageIdle
	s.Failure = ClaudeUsageFailureNone
	return nil
}

// Settle ends a login that never reached the identity review. The operation
// keeps the closed terminal state for progress reads, no identity or credential
// is attached, and every owed reference stays in the retirement list.
func (s *ClaudeServerUsageState) Settle(state SubscriptionLoginState, diagnostic *NativeSubscriptionDiagnostic) error {
	switch state {
	case SubscriptionCanceled, SubscriptionExpired, SubscriptionUnsupported, SubscriptionFailed:
	default:
		return invalidClaudeServerUsage()
	}
	if s.Connection != ClaudeUsageConnecting || s.Operation == nil {
		return invalidClaudeServerUsage()
	}
	operation := *s.Operation
	operation.State = state
	operation.Diagnostic = diagnostic
	if state == SubscriptionCanceled {
		operation.Diagnostic = nil
	}
	if operation.Validate() != nil {
		return invalidClaudeServerUsage()
	}
	s.Operation = &operation
	s.Connection = ClaudeUsageReconnectRequired
	s.Identity, s.CredentialID, s.Published, s.LinkedAt = nil, "", false, nil
	return nil
}

// MarkRecovery fences a login whose original native child is no longer owned by
// a live goroutine. The login is never relaunched; recovery only cleans up.
func (s *ClaudeServerUsageState) MarkRecovery() bool {
	if s.Connection != ClaudeUsageConnecting || s.Operation == nil || !s.Operation.Active() || s.Operation.State == SubscriptionRecovery {
		return false
	}
	operation := *s.Operation
	operation.State = SubscriptionRecovery
	s.Operation = &operation
	return true
}

// DiscardConfirmation abandons an unlinked identity review: Back or Cancel
// (SubscriptionCanceled) or an elapsed review window (SubscriptionExpired).
// The staged credential moves to the retirement list and no usage is published.
// It fails closed, without mutation, when the bounded list cannot hold it.
func (s *ClaudeServerUsageState) DiscardConfirmation(state SubscriptionLoginState) error {
	if s.Connection != ClaudeUsageConfirming || s.Operation == nil || s.CredentialID == "" || state != SubscriptionCanceled && state != SubscriptionExpired {
		return invalidClaudeServerUsage()
	}
	retired := slices.Clone(s.RetiredCredentials)
	if !slices.Contains(retired, s.CredentialID) {
		retired = append(retired, s.CredentialID)
	}
	if len(retired) > MaxClaudeUsageRetiredCredentials {
		return invalidClaudeServerUsage()
	}
	operation := *s.Operation
	operation.State = state
	operation.Diagnostic = nil
	s.Operation = &operation
	s.RetiredCredentials = retired
	s.Connection = ClaudeUsageReconnectRequired
	s.Identity, s.CredentialID, s.Published, s.LinkedAt = nil, "", false, nil
	return nil
}

// Disconnect is the explicit user revocation. All read authority stops, the
// credential reference moves to the retirement list, and the observed evidence
// and any ambiguous rotation phase are cleared because the credential they
// belong to is removed. The caller clears the projected account quota with
// ClearClaudeUsageQuota.
func (s *ClaudeServerUsageState) Disconnect() error {
	if err := s.Retire(); err != nil {
		return err
	}
	if s.Operation != nil {
		operation := *s.Operation
		if operation.State != SubscriptionCanceled {
			operation.State = SubscriptionCanceled
			operation.Diagnostic = nil
		}
		s.Operation = &operation
	}
	s.Identity, s.LinkedAt = nil, nil
	s.Refresh, s.Failure = ClaudeUsageIdle, ClaudeUsageFailureNone
	s.LastSuccessAt, s.BackoffUntil, s.Windows = nil, nil, nil
	return nil
}

// ConfirmCleanup records that the listed vault references were deleted and, when
// profile is true, that the native credential storage of the usage profile was
// removed. Only confirmed items are released.
func (s *ClaudeServerUsageState) ConfirmCleanup(removed []ID, profile bool) {
	if len(removed) != 0 {
		s.RetiredCredentials = slices.DeleteFunc(slices.Clone(s.RetiredCredentials), func(id ID) bool { return slices.Contains(removed, id) })
		if len(s.RetiredCredentials) == 0 {
			s.RetiredCredentials = nil
		}
	}
	if profile {
		s.ProfileCleanup = false
	}
}
