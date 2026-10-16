// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

// This file owns the usage-read schedule and the read, renewal and rotation
// transitions of a linked Claude usage connection. Every transition validates
// its source state and mutates nothing when it fails.
//
//	linked/idle --BeginRead--> linked/reading --ApplyClaudeUsageObservation--> linked/idle
//	linked/reading --SettleRead--> linked/idle (closed failure, optional backoff)
//	linked/reading --BeginRenewal--> linked/renewing --PublishRenewal--> linked/reading
//	linked/renewing --SettleRead--> linked/idle (renewal never started)
//	linked/(reading|renewing) --RequireReconnect--> reconnect-required
//	                                   (idle, or rotation-uncertain when the rotation is ambiguous)

const (
	// ClaudeUsageDueInterval is the inactive-maintenance cadence of the usage
	// read owner, deliberately shorter than ClaudeUsageFreshness so a retained
	// observation is renewed before it is presented as out of date.
	ClaudeUsageDueInterval = 4 * time.Minute
	// ClaudeUsageFreshness is the presentation freshness limit of an observation.
	ClaudeUsageFreshness = 5 * time.Minute
	// ClaudeUsageDefaultBackoff applies when a 403 or 429 carries no usable
	// Retry-After.
	ClaudeUsageDefaultBackoff = 5 * time.Minute
	// ClaudeUsageMaxBackoff caps every provider-requested suppression.
	ClaudeUsageMaxBackoff = time.Hour
	// ClaudeUsageMinBackoff is the floor of a provider-requested suppression, so a
	// zero or past Retry-After still spaces the next attempt.
	ClaudeUsageMinBackoff = time.Second
	// ClaudeUsageRenewalMargin is how long before the access token expires the
	// supervised native renewal runs.
	ClaudeUsageRenewalMargin = 2 * time.Minute
)

// ClaudeUsageBackoffDelay converts a Retry-After header value into a bounded
// delay. Delta seconds and the three HTTP date formats are honored. A missing or
// unusable value selects ClaudeUsageDefaultBackoff, and every honored value is
// clamped to [ClaudeUsageMinBackoff, ClaudeUsageMaxBackoff].
func ClaudeUsageBackoffDelay(retryAfter string, now time.Time) time.Duration {
	value := strings.TrimSpace(retryAfter)
	if value == "" || len(value) > 64 {
		return ClaudeUsageDefaultBackoff
	}
	var delay time.Duration
	if seconds, err := strconv.ParseUint(value, 10, 63); err == nil {
		// Compare in seconds first so a huge value never overflows a Duration.
		if seconds >= uint64(ClaudeUsageMaxBackoff/time.Second) {
			return ClaudeUsageMaxBackoff
		}
		delay = time.Duration(seconds) * time.Second
	} else if allDigits(value) {
		return ClaudeUsageMaxBackoff
	} else {
		at, ok := parseClaudeUsageHTTPDate(value)
		if !ok {
			return ClaudeUsageDefaultBackoff
		}
		delay = at.Sub(now)
	}
	return min(max(delay, ClaudeUsageMinBackoff), ClaudeUsageMaxBackoff)
}

func allDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func parseClaudeUsageHTTPDate(value string) (time.Time, bool) {
	for _, layout := range []string{"Mon, 02 Jan 2006 15:04:05 GMT", time.RFC850, time.ANSIC} {
		if at, err := time.Parse(layout, value); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

// ReadAllowed reports whether a provider read may begin at now: the connection
// is linked with a published credential, no read or renewal is in flight, no
// ambiguous rotation is retained and no provider-requested suppression is
// active. A scheduled and an explicit read both require it, so an explicit
// refresh can never bypass a backoff.
func (s ClaudeServerUsageState) ReadAllowed(now time.Time) bool {
	if !s.Reading() || s.Refresh != ClaudeUsageIdle {
		return false
	}
	return s.BackoffUntil == nil || !now.Before(*s.BackoffUntil)
}

// ReadDue reports whether a scheduled read is due at now: a read is allowed and
// the latest claimed read began at least ClaudeUsageDueInterval ago. The
// boundary is exact: 3m59.999s after the latest attempt is not due and 4m is. A
// wall clock that moved backward by more than one interval cannot prove spacing
// and is treated as due.
func (s ClaudeServerUsageState) ReadDue(now time.Time) bool {
	if !s.ReadAllowed(now) {
		return false
	}
	if s.LastAttemptAt == nil {
		return true
	}
	elapsed := now.Sub(*s.LastAttemptAt)
	return elapsed >= ClaudeUsageDueInterval || elapsed < -ClaudeUsageDueInterval
}

// BeginRead durably claims one provider read at now. It fails closed, without
// mutation, while a read is in flight or suppressed.
func (s *ClaudeServerUsageState) BeginRead(now time.Time) error {
	if !s.ReadAllowed(now) || !validClaudeUsageTime(now) {
		return invalidClaudeServerUsage()
	}
	attempt := now.UTC()
	s.Refresh = ClaudeUsageReading
	s.LastAttemptAt = &attempt
	return nil
}

// SettleRead ends a read or a renewal that never rotated a credential. It
// records the closed failure and the optional retry deadline exactly like
// RecordFailure and returns the phase to idle. Every last-success window and
// time is kept.
func (s *ClaudeServerUsageState) SettleRead(failure ClaudeUsageFailure, backoffUntil *time.Time) error {
	if s.Refresh != ClaudeUsageReading && s.Refresh != ClaudeUsageRenewing {
		return invalidClaudeServerUsage()
	}
	next := *s
	next.Refresh = ClaudeUsageIdle
	if err := next.RecordFailure(failure, backoffUntil); err != nil {
		return err
	}
	*s = next
	return nil
}

// ApplyClaudeUsageObservation merges one validated nonempty observation of the
// read claimed by BeginRead, as ApplyClaudeUsageQuota does, and returns the
// phase to idle. An observation older than the last success changes no evidence
// but still ends the read.
func ApplyClaudeUsageObservation(a *Account, generation ID, v ClaudeUsageQuota, now time.Time) (bool, error) {
	if a == nil || a.Subscription == nil || a.Subscription.ClaudeUsage == nil || a.Subscription.ClaudeUsage.Refresh != ClaudeUsageReading {
		return false, invalidClaudeServerUsage()
	}
	applied, err := ApplyClaudeUsageQuota(a, generation, v, now)
	if err != nil {
		return false, err
	}
	a.Subscription.ClaudeUsage.Refresh = ClaudeUsageIdle
	return applied, nil
}

// BeginRenewal durably claims the single supervised native refresh-login of the
// current read and owes removal of the native credential storage the child will
// create in the usage profile. No further renewal may be claimed by the same
// read: the caller must not call it again after PublishRenewal.
func (s *ClaudeServerUsageState) BeginRenewal() error {
	if !s.Reading() || s.Refresh != ClaudeUsageReading {
		return invalidClaudeServerUsage()
	}
	s.Refresh = ClaudeUsageRenewing
	s.ProfileCleanup = true
	return nil
}

// RecordRenewalIntent durably records the vault reference that the renewal is
// about to stage, before the vault is written, exactly like
// RecordCredentialIntent for the login. The reference stays owed for deletion
// until PublishRenewal makes it the current credential.
func (s *ClaudeServerUsageState) RecordRenewalIntent(id ID) error {
	if !s.Reading() || s.Refresh != ClaudeUsageRenewing || id.Validate() != nil || id == s.CredentialID || slices.Contains(s.RetiredCredentials, id) || len(s.RetiredCredentials) >= MaxClaudeUsageRetiredCredentials {
		return invalidClaudeServerUsage()
	}
	s.RetiredCredentials = append(slices.Clone(s.RetiredCredentials), id)
	return nil
}

// PublishRenewal makes the staged rotated credential the current one after the
// joined child, the unchanged identity and the vault write were proven. The
// superseded credential moves to the retirement list, so a lost deletion stays
// owed, and the phase returns to reading for the one GET retry. profileCleaned
// reports that the native credential storage was proven removed after the
// capture.
func (s *ClaudeServerUsageState) PublishRenewal(credential ID, profileCleaned bool) error {
	if !s.Reading() || s.Refresh != ClaudeUsageRenewing || credential.Validate() != nil || !slices.Contains(s.RetiredCredentials, credential) {
		return invalidClaudeServerUsage()
	}
	retired := slices.DeleteFunc(slices.Clone(s.RetiredCredentials), func(id ID) bool { return id == credential })
	retired = append(retired, s.CredentialID)
	if len(retired) > MaxClaudeUsageRetiredCredentials {
		return invalidClaudeServerUsage()
	}
	s.RetiredCredentials = retired
	s.CredentialID = credential
	s.Refresh = ClaudeUsageReading
	s.ProfileCleanup = !profileCleaned
	return nil
}

// RequireReconnect revokes reads after a definite 401, a rejected or
// unsupported renewal, a foreign identity or an ambiguous rotation. The
// connection becomes reconnect-required through Retire, so the credential is
// unpublished and owed for deletion and the last evidence stays visible. An
// ambiguous rotation keeps the rotation-uncertain phase so the recovery state
// is never silently dropped; the native storage stays owed for removal and is
// never used for another exchange. The closed failure carries no provider text.
func (s *ClaudeServerUsageState) RequireReconnect(failure ClaudeUsageFailure, uncertain bool) error {
	if s.Connection != ClaudeUsageLinked || !failure.Valid() || (s.Refresh != ClaudeUsageReading && s.Refresh != ClaudeUsageRenewing) {
		return invalidClaudeServerUsage()
	}
	next := *s
	if err := next.Retire(); err != nil {
		return err
	}
	next.Failure = failure
	next.Refresh = ClaudeUsageIdle
	if uncertain {
		next.Refresh = ClaudeUsageRotationUncertain
		next.ProfileCleanup = true
	}
	*s = next
	return nil
}

// FenceInterruptedRead settles the read state of a linked connection whose
// owner no longer exists, such as after a server restart. An interrupted read is
// idempotent and simply returns to idle (the latest attempt time still spaces
// the next one). An interrupted renewal may have rotated the credential, so it
// is never repeated: the connection requires an explicit reconnect with the
// rotation-uncertain phase. It reports whether anything changed.
func (s *ClaudeServerUsageState) FenceInterruptedRead() bool {
	if s.Connection != ClaudeUsageLinked {
		return false
	}
	switch s.Refresh {
	case ClaudeUsageReading:
		s.Refresh = ClaudeUsageIdle
		return true
	case ClaudeUsageRenewing:
		return s.RequireReconnect(ClaudeUsageFailureNone, true) == nil
	}
	return false
}
