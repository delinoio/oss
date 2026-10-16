// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseClaudeLimitNormalizesRemainingFraction(t *testing.T) {
	at := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	body := `{"five_hour":{"utilization":25,"resets_at":"2026-10-16T12:00:00Z"},"seven_day":{"utilization":50,"resets_at":"2026-10-20T12:00:00Z"},"unrelated":{"utilization":99}}`
	observation, err := ParseClaudeLimit([]byte(body), at)
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Windows) != 2 || !observation.ObservedAt.Equal(at) {
		t.Fatalf("unexpected observation %+v", observation)
	}
	five, seven := observation.Windows[0], observation.Windows[1]
	if five.ID != ClaudeLimitFiveHour || five.Remaining != 0.75 || five.ResetAt == nil || !five.ResetAt.Equal(time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("five_hour %+v", five)
	}
	if seven.ID != ClaudeLimitSevenDay || seven.Remaining != 0.5 || seven.ResetAt == nil || !seven.ResetAt.Equal(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("seven_day %+v", seven)
	}
}

func TestParseClaudeLimitBoundariesAndUnavailableWindows(t *testing.T) {
	at := time.Now().UTC()
	cases := []struct {
		name      string
		body      string
		windows   int
		remaining []float64
	}{
		{"zero and full utilization", `{"five_hour":{"utilization":0},"seven_day":{"utilization":100,"resets_at":null}}`, 2, []float64{1, 0}},
		{"null seven day is unavailable", `{"five_hour":{"utilization":10.5,"resets_at":null},"seven_day":null}`, 1, []float64{0.895}},
		{"missing five hour is unavailable", `{"seven_day":{"utilization":40}}`, 1, []float64{0.6}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			observation, err := ParseClaudeLimit([]byte(tc.body), at)
			if err != nil {
				t.Fatal(err)
			}
			if len(observation.Windows) != tc.windows {
				t.Fatalf("windows %+v", observation.Windows)
			}
			for i, want := range tc.remaining {
				if got := observation.Windows[i].Remaining; got < want-1e-9 || got > want+1e-9 {
					t.Fatalf("window %d remaining %v want %v", i, got, want)
				}
			}
		})
	}
}

func TestParseClaudeLimitRejectsMalformedRecognizedWindowsAtomically(t *testing.T) {
	at := time.Now().UTC()
	bad := map[string]string{
		"string utilization":     `{"five_hour":{"utilization":"25"}}`,
		"NaN literal":            `{"five_hour":{"utilization":NaN}}`,
		"quoted NaN":             `{"five_hour":{"utilization":"NaN"}}`,
		"infinity":               `{"five_hour":{"utilization":Infinity}}`,
		"negative":               `{"five_hour":{"utilization":-1}}`,
		"above one hundred":      `{"five_hour":{"utilization":100.1}}`,
		"missing utilization":    `{"five_hour":{"resets_at":"2026-10-16T12:00:00Z"}}`,
		"bad reset":              `{"five_hour":{"utilization":1,"resets_at":"tomorrow"}}`,
		"numeric reset":          `{"five_hour":{"utilization":1,"resets_at":12}}`,
		"empty reset":            `{"five_hour":{"utilization":1,"resets_at":""}}`,
		"one good one bad":       `{"five_hour":{"utilization":10},"seven_day":{"utilization":500}}`,
		"window not object":      `{"five_hour":5}`,
		"duplicate window":       `{"five_hour":{"utilization":1},"five_hour":{"utilization":2}}`,
		"duplicate inner field":  `{"five_hour":{"utilization":1,"utilization":2}}`,
		"not an object":          `[1]`,
		"trailing data":          `{"five_hour":{"utilization":1}} {}`,
		"truncated":              `{"five_hour":{"utilization":1`,
		"empty body":             ``,
		"year out of range":      `{"five_hour":{"utilization":1,"resets_at":"10000-01-01T00:00:00Z"}}`,
		"unix epoch not allowed": `{"five_hour":{"utilization":1,"resets_at":"1970-01-01T00:00:00Z"}}`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := ParseClaudeLimit([]byte(body), at)
			if got := ClaudeLimitFailureOf(err); got != ClaudeLimitFailureMalformed {
				t.Fatalf("failure %q err %v", got, err)
			}
		})
	}
}

func TestParseClaudeLimitEmptyAndOversizedNeverCountAsObservations(t *testing.T) {
	at := time.Now().UTC()
	for _, body := range []string{`{}`, `{"five_hour":null,"seven_day":null}`, `{"other":{"utilization":3}}`} {
		if _, err := ParseClaudeLimit([]byte(body), at); ClaudeLimitFailureOf(err) != ClaudeLimitFailureEmpty {
			t.Fatalf("%s: %v", body, err)
		}
	}
	oversized := `{"five_hour":{"utilization":1},"padding":"` + strings.Repeat("x", ClaudeLimitMaxResponse) + `"}`
	if _, err := ParseClaudeLimit([]byte(oversized), at); ClaudeLimitFailureOf(err) != ClaudeLimitFailureOversized {
		t.Fatalf("oversized: %v", err)
	}
}

func TestParseClaudeRetryAfterHonorsSecondsDateDefaultAndCap(t *testing.T) {
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		value string
		want  time.Duration
	}{
		{"", ClaudeLimitDefaultBackoff},
		{"not-a-delay", ClaudeLimitDefaultBackoff},
		{"-5", ClaudeLimitDefaultBackoff},
		{"90", 90 * time.Second},
		{"0", time.Second},
		{"99999999", ClaudeLimitMaxBackoff},
		{"3600", ClaudeLimitMaxBackoff},
		{now.Add(10 * time.Minute).Format(http.TimeFormat), 10 * time.Minute},
		{now.Add(5 * time.Hour).Format(http.TimeFormat), ClaudeLimitMaxBackoff},
		{now.Add(-time.Hour).Format(http.TimeFormat), time.Second},
	}
	for _, tc := range cases {
		if got := ParseClaudeRetryAfter(tc.value, now); got != tc.want {
			t.Fatalf("%q: got %v want %v", tc.value, got, tc.want)
		}
	}
}

func linkedClaudeLimitState(at time.Time) *ClaudeLimitState {
	linked := at
	return &ClaudeLimitState{
		Generation:   NewID(),
		Identity:     &ClaudeLimitIdentity{Email: "alex@example.com", Organization: "org", Plan: "max", Commitment: strings.Repeat("a", 64)},
		LinkedAt:     &linked,
		CredentialID: NewID(),
	}
}

func TestClaudeLimitDueBoundaryIsFourMinutes(t *testing.T) {
	base := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	state := linkedClaudeLimitState(base)
	state.Read.LastAttemptAt = &base
	if state.DueAt(base.Add(4*time.Minute - time.Millisecond)) {
		t.Fatal("3m59.999s must not be due")
	}
	if !state.DueAt(base.Add(4 * time.Minute)) {
		t.Fatal("4m must be due")
	}
	never := linkedClaudeLimitState(base)
	if !never.DueAt(base) {
		t.Fatal("a never-attempted connection is due")
	}
}

func TestClaudeLimitBackoffSuppressesDueAndManualReads(t *testing.T) {
	base := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	state := linkedClaudeLimitState(base)
	state.RecordFailure(ClaudeLimitFailureRateLimited, base, 10*time.Minute)
	state.Read.LastAttemptAt = &base
	if state.DueAt(base.Add(9*time.Minute)) || state.ManualAllowedAt(base.Add(9*time.Minute)) {
		t.Fatal("backoff must suppress both automatic and manual reads")
	}
	if !state.ManualAllowedAt(base.Add(10 * time.Minute)) {
		t.Fatal("a read is allowed once the backoff ends")
	}
	if !state.DueAt(base.Add(10 * time.Minute)) {
		t.Fatal("automatic cadence resumes after backoff")
	}
}

func TestClaudeLimitFailurePreservesLastSuccess(t *testing.T) {
	base := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	state := linkedClaudeLimitState(base)
	observation, err := ParseClaudeLimit([]byte(`{"five_hour":{"utilization":25}}`), base)
	if err != nil {
		t.Fatal(err)
	}
	state.RecordSuccess(observation)
	state.RecordFailure(ClaudeLimitFailureTimeout, base.Add(time.Hour), 0)
	if state.LastSuccess == nil || !state.LastSuccess.Equal(base) || len(state.Windows) != 1 || state.Windows[0].Remaining != 0.75 {
		t.Fatalf("failure changed accepted evidence: %+v", state)
	}
	if state.Failure != ClaudeLimitFailureTimeout || state.FailureAt == nil || state.ReconnectRequired {
		t.Fatalf("failure not recorded: %+v", state)
	}
	state.RecordFailure(ClaudeLimitFailureRefreshRejected, base.Add(2*time.Hour), 0)
	if !state.ReconnectRequired {
		t.Fatal("a rejected refresh requires an explicit reconnect")
	}
}

func claudeUsageAccount(state *ClaudeLimitState) Account {
	return Account{Alias: "Claude", Type: SubscriptionAccount, SubscriptionService: SubscriptionClaude, Health: AccountReady, Enabled: true, Quota: []QuotaWindow{}, Connection: &AccountConnection{ID: NewID(), Authentication: SubscriptionAuth, ConnectedAt: time.Now().UTC()}, Subscription: &SubscriptionState{ClaudeCredential: ClaudeCredentialServerToken, Generation: NewID(), IdentityCommitment: strings.Repeat("b", 64), ClaudeUsage: state}}
}

func TestProjectClaudeLimitsIsInformationalAndNeverRoutingEvidence(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	state := linkedClaudeLimitState(base)
	reset := base.Add(2 * time.Hour)
	state.RecordSuccess(ClaudeLimitObservation{ObservedAt: base, Windows: []ClaudeLimitWindow{{ID: ClaudeLimitFiveHour, Remaining: 0.75, ResetAt: &reset}, {ID: ClaudeLimitSevenDay, Remaining: 0.5}}})
	account := claudeUsageAccount(state)
	account.Quota = []QuotaWindow{{ID: "other", ComparisonGroup: "chatgpt", Blocking: true, State: Observed}}
	account.ProjectClaudeLimits()
	if err := account.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(account.Quota) != 3 {
		t.Fatalf("quota %+v", account.Quota)
	}
	for _, w := range account.Quota[1:] {
		if w.Blocking || w.ComparisonGroup != "claude" || w.State != Observed || w.Remaining == nil || !w.ObservedAt.Equal(base) {
			t.Fatalf("window %+v", w)
		}
	}
	if account.Quota[1].ID != "five_hour" || *account.Quota[1].Remaining != 0.75 || account.Quota[1].ResetAt == nil || !account.Quota[1].ResetAt.Equal(reset) {
		t.Fatalf("five hour %+v", account.Quota[1])
	}
	if account.Quota[2].ResetAt != nil || *account.Quota[2].Remaining != 0.5 {
		t.Fatalf("seven day %+v", account.Quota[2])
	}
	// Claude windows never become routing evidence: only blocking windows count.
	only := account
	only.Quota = account.Quota[1:]
	if got, score, _, _ := quotaEvidence(only.Quota, base); got != ObservationUnknown || score != nil {
		t.Fatalf("Claude windows influenced routing evidence: %v %v", got, score)
	}
	// Unlinking removes the projection and leaves unrelated windows.
	account.Subscription.ClaudeUsage.LinkedAt = nil
	account.Subscription.ClaudeUsage.CredentialID = ""
	account.Subscription.ClaudeUsage.ClearEvidence()
	account.ProjectClaudeLimits()
	if len(account.Quota) != 1 || account.Quota[0].ID != "other" {
		t.Fatalf("unlinked quota %+v", account.Quota)
	}
}

func TestClaudeLimitStateValidationBindsAccountAndAssociation(t *testing.T) {
	base := time.Now().UTC()
	state := linkedClaudeLimitState(base)
	account := claudeUsageAccount(state)
	if err := account.Validate(); err != nil {
		t.Fatal(err)
	}
	unlinkedWithCredential := *state
	unlinkedWithCredential.LinkedAt = nil
	account = claudeUsageAccount(&unlinkedWithCredential)
	if account.Validate() == nil {
		t.Fatal("a credential without the explicit association must be rejected")
	}
	chatgpt := claudeUsageAccount(state)
	chatgpt.SubscriptionService = SubscriptionChatGPT
	chatgpt.Subscription.ClaudeCredential = ""
	if chatgpt.Validate() == nil {
		t.Fatal("usage state is Claude-only")
	}
	bad := *state
	bad.Failure = "native body leaked"
	if claudeUsageAccount(&bad).Validate() == nil {
		t.Fatal("failure codes are a closed vocabulary")
	}
}
