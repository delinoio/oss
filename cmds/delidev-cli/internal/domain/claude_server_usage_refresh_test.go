// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClaudeUsageBackoffDelayHonorsSecondsDateDefaultAndCap(t *testing.T) {
	now := claudeUsageNow
	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"missing", "", ClaudeUsageDefaultBackoff},
		{"seconds", "30", 30 * time.Second},
		{"seconds with space", " 90 ", 90 * time.Second},
		{"zero is floored", "0", ClaudeUsageMinBackoff},
		{"just under cap", "3599", 3599 * time.Second},
		{"cap", "3600", ClaudeUsageMaxBackoff},
		{"over cap", "86400", ClaudeUsageMaxBackoff},
		{"overflowing digits cap", "99999999999999999999999999", ClaudeUsageMaxBackoff},
		{"negative is unusable", "-5", ClaudeUsageDefaultBackoff},
		{"fractional is unusable", "1.5", ClaudeUsageDefaultBackoff},
		{"garbage", "soon", ClaudeUsageDefaultBackoff},
		{"imf date", now.Add(10 * time.Minute).Format("Mon, 02 Jan 2006 15:04:05 GMT"), 10 * time.Minute},
		{"rfc850 date", now.Add(2 * time.Minute).Format(time.RFC850), 2 * time.Minute},
		{"asctime date", now.Add(45 * time.Second).Format(time.ANSIC), 45 * time.Second},
		{"past date is floored", now.Add(-time.Hour).Format("Mon, 02 Jan 2006 15:04:05 GMT"), ClaudeUsageMinBackoff},
		{"far future date is capped", now.Add(48 * time.Hour).Format("Mon, 02 Jan 2006 15:04:05 GMT"), ClaudeUsageMaxBackoff},
	}
	for _, c := range cases {
		if got := ClaudeUsageBackoffDelay(c.value, now); got != c.want {
			t.Errorf("%s: delay(%q) = %v, want %v", c.name, c.value, got, c.want)
		}
	}
}

func TestClaudeUsageReadDueBoundaryIsExact(t *testing.T) {
	usage := linkedClaudeUsage()
	if !usage.ReadDue(claudeUsageNow) {
		t.Fatal("a linked connection that never read must be due")
	}
	attempt := claudeUsageNow
	usage.LastAttemptAt = &attempt
	for _, c := range []struct {
		elapsed time.Duration
		due     bool
	}{
		{0, false},
		{ClaudeUsageDueInterval - time.Millisecond, false},
		{ClaudeUsageDueInterval, true},
		{ClaudeUsageDueInterval + time.Millisecond, true},
		{-time.Second, false},
		{-ClaudeUsageDueInterval - time.Second, true},
	} {
		if got := usage.ReadDue(claudeUsageNow.Add(c.elapsed)); got != c.due {
			t.Errorf("due at %v after the attempt = %v, want %v", c.elapsed, got, c.due)
		}
	}
}

func TestClaudeUsageReadAllowedHonorsBackoffPhaseAndConnection(t *testing.T) {
	usage := linkedClaudeUsage()
	until := claudeUsageNow.Add(time.Minute)
	usage.Failure, usage.BackoffUntil = ClaudeUsageFailureRateLimited, &until
	if usage.ReadAllowed(claudeUsageNow) || usage.ReadDue(claudeUsageNow) {
		t.Fatal("a suppressed connection allowed a read")
	}
	if usage.ReadAllowed(until.Add(-time.Nanosecond)) {
		t.Fatal("a read was allowed before the deadline")
	}
	if !usage.ReadAllowed(until) {
		t.Fatal("a read was refused at the deadline")
	}
	usage.Refresh = ClaudeUsageReading
	if usage.ReadAllowed(until.Add(time.Hour)) {
		t.Fatal("a second read was allowed while one is in flight")
	}
	usage.Refresh = ClaudeUsageRotationUncertain
	if usage.ReadAllowed(until.Add(time.Hour)) {
		t.Fatal("an ambiguous rotation allowed a read")
	}
	other := linkedClaudeUsage()
	other.Connection, other.Published = ClaudeUsageReconnectRequired, false
	if other.ReadAllowed(claudeUsageNow) {
		t.Fatal("a reconnect-required connection allowed a read")
	}
}

func TestClaudeUsageBeginReadAndObservationReturnToIdle(t *testing.T) {
	usage := linkedClaudeUsage()
	account := claudeUsageAccount(t, usage)
	if err := usage.BeginRead(claudeUsageNow); err != nil {
		t.Fatal(err)
	}
	if usage.Refresh != ClaudeUsageReading || usage.LastAttemptAt == nil || !usage.LastAttemptAt.Equal(claudeUsageNow) {
		t.Fatalf("claim = %+v", usage)
	}
	if usage.BeginRead(claudeUsageNow) == nil {
		t.Fatal("a second read was claimed")
	}
	validateLifecycle(t, "begin read", usage)
	reset := claudeUsageNow.Add(time.Hour)
	quota := ClaudeUsageQuota{ObservedAt: claudeUsageNow, Windows: []ClaudeUsageWindow{{ID: ClaudeUsageFiveHour, Remaining: 0.75, ResetAt: &reset}}}
	applied, err := ApplyClaudeUsageObservation(&account, usage.Generation, quota, claudeUsageNow)
	if err != nil || !applied {
		t.Fatalf("apply = %v %v", applied, err)
	}
	if usage.Refresh != ClaudeUsageIdle || usage.LastSuccessAt == nil || !usage.LastSuccessAt.Equal(claudeUsageNow) || usage.Failure != ClaudeUsageFailureNone {
		t.Fatalf("observation did not settle the read: %+v", usage)
	}
	if _, err := ApplyClaudeUsageObservation(&account, usage.Generation, quota, claudeUsageNow); err == nil {
		t.Fatal("an observation was applied without a claimed read")
	}
	if err := account.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeUsageSettleReadKeepsEvidenceAndValidatesBeforeMutation(t *testing.T) {
	usage := linkedClaudeUsage()
	account := claudeUsageAccount(t, usage)
	if err := usage.BeginRead(claudeUsageNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	quota := ClaudeUsageQuota{ObservedAt: claudeUsageNow.Add(-time.Hour), Windows: []ClaudeUsageWindow{{ID: ClaudeUsageSevenDay, Remaining: 0.5}}}
	if _, err := ApplyClaudeUsageObservation(&account, usage.Generation, quota, claudeUsageNow); err != nil {
		t.Fatal(err)
	}
	before := *usage.LastSuccessAt
	if err := usage.BeginRead(claudeUsageNow); err != nil {
		t.Fatal(err)
	}
	until := claudeUsageNow.Add(5 * time.Minute)
	if err := usage.SettleRead(ClaudeUsageFailureMalformed, &until); err == nil {
		t.Fatal("a backoff was accepted for a non-backoff failure")
	}
	if usage.Refresh != ClaudeUsageReading || usage.Failure != ClaudeUsageFailureNone {
		t.Fatalf("a rejected settlement mutated the section: %+v", usage)
	}
	if err := usage.SettleRead(ClaudeUsageFailureRateLimited, &until); err != nil {
		t.Fatal(err)
	}
	if usage.Refresh != ClaudeUsageIdle || usage.Failure != ClaudeUsageFailureRateLimited || usage.BackoffUntil == nil || !usage.LastSuccessAt.Equal(before) || len(usage.Windows) != 1 {
		t.Fatalf("failure did not keep the evidence: %+v", usage)
	}
	if usage.SettleRead(ClaudeUsageFailureEmpty, nil) == nil {
		t.Fatal("an idle section accepted a settlement")
	}
	validateLifecycle(t, "settle", usage)
}

func TestClaudeUsageRenewalRotatesCredentialThroughDurableIntent(t *testing.T) {
	usage := linkedClaudeUsage()
	old := usage.CredentialID
	if usage.BeginRenewal() == nil {
		t.Fatal("a renewal began without a claimed read")
	}
	if err := usage.BeginRead(claudeUsageNow); err != nil {
		t.Fatal(err)
	}
	if usage.RecordRenewalIntent(NewID()) == nil {
		t.Fatal("an intent was recorded before the renewal began")
	}
	if err := usage.BeginRenewal(); err != nil {
		t.Fatal(err)
	}
	if !usage.ProfileCleanup || usage.Refresh != ClaudeUsageRenewing {
		t.Fatalf("renewal claim = %+v", usage)
	}
	if usage.BeginRenewal() == nil {
		t.Fatal("a second renewal was claimed")
	}
	validateLifecycle(t, "renewing", usage)
	next := NewID()
	if usage.PublishRenewal(next, true) == nil {
		t.Fatal("a credential without an intent was published")
	}
	if usage.RecordRenewalIntent(old) == nil {
		t.Fatal("the current credential was recorded as an intent")
	}
	if err := usage.RecordRenewalIntent(next); err != nil {
		t.Fatal(err)
	}
	if usage.RecordRenewalIntent(next) == nil {
		t.Fatal("an intent was recorded twice")
	}
	validateLifecycle(t, "intent", usage)
	if err := usage.PublishRenewal(next, true); err != nil {
		t.Fatal(err)
	}
	if usage.CredentialID != next || usage.Refresh != ClaudeUsageReading || usage.ProfileCleanup {
		t.Fatalf("publication = %+v", usage)
	}
	if len(usage.RetiredCredentials) != 1 || usage.RetiredCredentials[0] != old {
		t.Fatalf("the superseded credential is not owed for deletion: %v", usage.RetiredCredentials)
	}
	validateLifecycle(t, "published", usage)
}

func TestClaudeUsageRequireReconnectRetiresAndRetainsRecovery(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		usage := linkedClaudeUsage()
		account := claudeUsageAccount(t, usage)
		credential := usage.CredentialID
		if err := usage.BeginRead(claudeUsageNow.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		quota := ClaudeUsageQuota{ObservedAt: claudeUsageNow.Add(-time.Hour), Windows: []ClaudeUsageWindow{{ID: ClaudeUsageFiveHour, Remaining: 0.25}}}
		if _, err := ApplyClaudeUsageObservation(&account, usage.Generation, quota, claudeUsageNow); err != nil {
			t.Fatal(err)
		}
		if err := usage.BeginRead(claudeUsageNow); err != nil {
			t.Fatal(err)
		}
		if err := usage.BeginRenewal(); err != nil {
			t.Fatal(err)
		}
		if err := usage.RequireReconnect(ClaudeUsageFailureUnauthorized, uncertain); err != nil {
			t.Fatal(err)
		}
		if usage.Connection != ClaudeUsageReconnectRequired || usage.Published || usage.CredentialID != "" || usage.Reading() || usage.ReadAllowed(claudeUsageNow.Add(time.Hour)) {
			t.Fatalf("reads were not revoked: %+v", usage)
		}
		if len(usage.RetiredCredentials) != 1 || usage.RetiredCredentials[0] != credential || len(usage.Windows) != 1 || usage.Failure != ClaudeUsageFailureUnauthorized {
			t.Fatalf("evidence or cleanup owed was lost: %+v", usage)
		}
		wantPhase := ClaudeUsageIdle
		if uncertain {
			wantPhase = ClaudeUsageRotationUncertain
		}
		if usage.Refresh != wantPhase || !usage.ProfileCleanup {
			t.Fatalf("recovery state = %q cleanup %v, want %q with cleanup owed", usage.Refresh, usage.ProfileCleanup, wantPhase)
		}
		if err := account.Validate(); err != nil {
			t.Fatal(err)
		}
		if usage.RequireReconnect(ClaudeUsageFailureUnauthorized, false) == nil {
			t.Fatal("a reconnect-required section required a reconnect again")
		}
	}
}

func TestClaudeUsageFenceInterruptedReadNeverRepeatsARenewal(t *testing.T) {
	usage := linkedClaudeUsage()
	if usage.FenceInterruptedRead() {
		t.Fatal("an idle section changed")
	}
	if err := usage.BeginRead(claudeUsageNow); err != nil {
		t.Fatal(err)
	}
	if !usage.FenceInterruptedRead() || usage.Refresh != ClaudeUsageIdle || usage.Connection != ClaudeUsageLinked || usage.LastAttemptAt == nil {
		t.Fatalf("an interrupted read was not released: %+v", usage)
	}
	if err := usage.BeginRead(claudeUsageNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := usage.BeginRenewal(); err != nil {
		t.Fatal(err)
	}
	if !usage.FenceInterruptedRead() {
		t.Fatal("an interrupted renewal was not fenced")
	}
	if usage.Connection != ClaudeUsageReconnectRequired || usage.Refresh != ClaudeUsageRotationUncertain || usage.Published || !usage.ProfileCleanup {
		t.Fatalf("an interrupted renewal kept authority: %+v", usage)
	}
	validateLifecycle(t, "fenced renewal", usage)
	if usage.FenceInterruptedRead() {
		t.Fatal("a fenced section changed again")
	}
}

func TestParseClaudeUsageRetryAfterSeparatesUsableFromMalformedValues(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for _, value := range []string{"", "soon", "-5", "1.5", strings.Repeat("9x", 40)} {
		if delay, ok := ParseClaudeUsageRetryAfter(value, now); ok || delay != 0 {
			t.Fatalf("%q was treated as usable: %v", value, delay)
		}
		if got := ClaudeUsageBackoffDelay(value, now); got != ClaudeUsageDefaultBackoff {
			t.Fatalf("%q did not select the default backoff: %v", value, got)
		}
	}
	if delay, ok := ParseClaudeUsageRetryAfter("120", now); !ok || delay != 2*time.Minute {
		t.Fatalf("seconds: %v %v", delay, ok)
	}
	if delay, ok := ParseClaudeUsageRetryAfter("999999999999", now); !ok || delay != ClaudeUsageMaxBackoff {
		t.Fatalf("huge seconds: %v %v", delay, ok)
	}
	date := now.Add(10 * time.Minute).Format(http.TimeFormat)
	if delay, ok := ParseClaudeUsageRetryAfter(date, now); !ok || delay != 10*time.Minute {
		t.Fatalf("date: %v %v", delay, ok)
	}
}
