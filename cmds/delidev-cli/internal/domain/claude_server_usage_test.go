// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

const claudeUsageCommitment = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func claudeUsageAccount(t *testing.T, usage *ClaudeServerUsageState) Account {
	t.Helper()
	return Account{
		Alias: "claude", Type: SubscriptionAccount, SubscriptionService: SubscriptionClaude, Enabled: true, Health: AccountReady,
		Connection: &AccountConnection{ID: NewID(), Authentication: SubscriptionAuth, ConnectedAt: claudeUsageNow},
		Subscription: &SubscriptionState{
			ClaudeCredential: ClaudeCredentialServerToken, Generation: NewID(), IdentityCommitment: claudeUsageCommitment,
			ClaudeUsage: usage,
		},
	}
}

func linkedClaudeUsage() *ClaudeServerUsageState {
	linked := claudeUsageNow.Add(-time.Hour)
	return &ClaudeServerUsageState{
		Connection: ClaudeUsageLinked, Generation: NewID(), CredentialID: NewID(), Published: true, LinkedAt: &linked,
		Identity: &ClaudeUsageIdentity{Email: "person@example.com", Organization: "Example Org", Plan: "Max", Commitment: claudeUsageCommitment},
	}
}

func claudeUsageOperation(state SubscriptionLoginState) *ClaudeUsageOperation {
	return &ClaudeUsageOperation{ID: NewID(), Epoch: NewID(), Actor: Principal{Type: OwnerDevice}, State: state, StartedAt: claudeUsageNow, ExpiresAt: claudeUsageNow.Add(10 * time.Minute)}
}

func TestClaudeServerUsageStateValidatesClosedLifecycle(t *testing.T) {
	connecting := &ClaudeServerUsageState{Connection: ClaudeUsageConnecting, Generation: NewID(), Operation: claudeUsageOperation(SubscriptionWaiting)}
	confirming := &ClaudeServerUsageState{Connection: ClaudeUsageConfirming, Generation: NewID(), Operation: claudeUsageOperation(SubscriptionSucceeded), CredentialID: NewID(), Identity: linkedClaudeUsage().Identity}
	reconnect := &ClaudeServerUsageState{Connection: ClaudeUsageReconnectRequired, Generation: NewID(), Identity: linkedClaudeUsage().Identity, RetiredCredentials: []ID{NewID()}}
	for name, usage := range map[string]*ClaudeServerUsageState{"connecting": connecting, "confirming": confirming, "linked": linkedClaudeUsage(), "reconnect": reconnect} {
		account := claudeUsageAccount(t, usage)
		if err := account.Validate(); err != nil {
			t.Fatalf("%s state was rejected: %v", name, err)
		}
	}
}

func TestClaudeServerUsageStateRejectsInvalidShapes(t *testing.T) {
	mutate := func(edit func(*ClaudeServerUsageState)) *ClaudeServerUsageState {
		usage := linkedClaudeUsage()
		edit(usage)
		return usage
	}
	soon := claudeUsageNow.Add(time.Minute)
	for name, usage := range map[string]*ClaudeServerUsageState{
		"unknown connection":      mutate(func(u *ClaudeServerUsageState) { u.Connection = "paused" }),
		"missing generation":      mutate(func(u *ClaudeServerUsageState) { u.Generation = "" }),
		"unpublished link":        mutate(func(u *ClaudeServerUsageState) { u.Published = false }),
		"link without identity":   mutate(func(u *ClaudeServerUsageState) { u.Identity = nil }),
		"link without credential": mutate(func(u *ClaudeServerUsageState) { u.CredentialID = "" }),
		"link without time":       mutate(func(u *ClaudeServerUsageState) { u.LinkedAt = nil }),
		"bad commitment":          mutate(func(u *ClaudeServerUsageState) { u.Identity.Commitment = "ABC" }),
		"empty email":             mutate(func(u *ClaudeServerUsageState) { u.Identity.Email = "" }),
		"control in email":        mutate(func(u *ClaudeServerUsageState) { u.Identity.Email = "a@b.c\n" }),
		"long organization":       mutate(func(u *ClaudeServerUsageState) { u.Identity.Organization = strings.Repeat("o", 257) }),
		"unknown refresh":         mutate(func(u *ClaudeServerUsageState) { u.Refresh = "polling" }),
		"unknown failure":         mutate(func(u *ClaudeServerUsageState) { u.Failure = "boom" }),
		"backoff without failure": mutate(func(u *ClaudeServerUsageState) { u.BackoffUntil = &soon }),
		"backoff on auth failure": mutate(func(u *ClaudeServerUsageState) {
			u.Failure = ClaudeUsageFailureUnauthorized
			u.BackoffUntil = &soon
		}),
		"windows without success time": mutate(func(u *ClaudeServerUsageState) {
			u.Windows = []ClaudeUsageWindowEvidence{{ID: ClaudeUsageFiveHour, Remaining: 0.5, ObservedAt: claudeUsageNow}}
		}),
		"success time without windows": mutate(func(u *ClaudeServerUsageState) { u.LastSuccessAt = &soon }),
		"duplicate window": mutate(func(u *ClaudeServerUsageState) {
			u.LastSuccessAt = &soon
			w := ClaudeUsageWindowEvidence{ID: ClaudeUsageFiveHour, Remaining: 0.5, ObservedAt: claudeUsageNow}
			u.Windows = []ClaudeUsageWindowEvidence{w, w}
		}),
		"unknown window": mutate(func(u *ClaudeServerUsageState) {
			u.LastSuccessAt = &soon
			u.Windows = []ClaudeUsageWindowEvidence{{ID: "thirty_day", Remaining: 0.5, ObservedAt: claudeUsageNow}}
		}),
		"remaining over one": mutate(func(u *ClaudeServerUsageState) {
			u.LastSuccessAt = &soon
			u.Windows = []ClaudeUsageWindowEvidence{{ID: ClaudeUsageSevenDay, Remaining: 1.5, ObservedAt: claudeUsageNow}}
		}),
		"window newer than success": mutate(func(u *ClaudeServerUsageState) {
			u.LastSuccessAt = &claudeUsageNow
			u.Windows = []ClaudeUsageWindowEvidence{{ID: ClaudeUsageSevenDay, Remaining: 0.5, ObservedAt: soon}}
		}),
		"retired current credential": mutate(func(u *ClaudeServerUsageState) { u.RetiredCredentials = []ID{u.CredentialID} }),
		"duplicate retired":          mutate(func(u *ClaudeServerUsageState) { id := NewID(); u.RetiredCredentials = []ID{id, id} }),
		"too many retired": mutate(func(u *ClaudeServerUsageState) {
			for range MaxClaudeUsageRetiredCredentials + 1 {
				u.RetiredCredentials = append(u.RetiredCredentials, NewID())
			}
		}),
		"operation too long": mutate(func(u *ClaudeServerUsageState) {
			u.Operation = claudeUsageOperation(SubscriptionSucceeded)
			u.Operation.ExpiresAt = u.Operation.StartedAt.Add(MaxClaudeUsageLoginWindow + time.Second)
		}),
		"active operation when linked": mutate(func(u *ClaudeServerUsageState) { u.Operation = claudeUsageOperation(SubscriptionWaiting) }),
		"consumed code without id": mutate(func(u *ClaudeServerUsageState) {
			u.Operation = claudeUsageOperation(SubscriptionSucceeded)
			u.Operation.CodeConsumed = true
		}),
		"connecting with credential":  {Connection: ClaudeUsageConnecting, Generation: NewID(), Operation: claudeUsageOperation(SubscriptionWaiting), CredentialID: NewID()},
		"connecting without op":       {Connection: ClaudeUsageConnecting, Generation: NewID()},
		"connecting with canceled op": {Connection: ClaudeUsageConnecting, Generation: NewID(), Operation: claudeUsageOperation(SubscriptionCanceled)},
		"connecting with expired op":  {Connection: ClaudeUsageConnecting, Generation: NewID(), Operation: claudeUsageOperation(SubscriptionExpired)},
		"connecting with failed op":   {Connection: ClaudeUsageConnecting, Generation: NewID(), Operation: claudeUsageOperation(SubscriptionFailed)},
		"connecting with unsupported": {Connection: ClaudeUsageConnecting, Generation: NewID(), Operation: claudeUsageOperation(SubscriptionUnsupported)},
		"connecting with succeeded":   {Connection: ClaudeUsageConnecting, Generation: NewID(), Operation: claudeUsageOperation(SubscriptionSucceeded)},
		"confirming published":        {Connection: ClaudeUsageConfirming, Generation: NewID(), Operation: claudeUsageOperation(SubscriptionSucceeded), CredentialID: NewID(), Identity: linkedClaudeUsage().Identity, Published: true},
		"confirming with evidence":    {Connection: ClaudeUsageConfirming, Generation: NewID(), Operation: claudeUsageOperation(SubscriptionSucceeded), CredentialID: NewID(), Identity: linkedClaudeUsage().Identity, Failure: ClaudeUsageFailureUnavailable},
		"reconnect with credential":   {Connection: ClaudeUsageReconnectRequired, Generation: NewID(), CredentialID: NewID()},
		"reconnect with backoff":      {Connection: ClaudeUsageReconnectRequired, Generation: NewID(), BackoffUntil: &soon, Failure: ClaudeUsageFailureRateLimited},
	} {
		account := claudeUsageAccount(t, usage)
		if account.Subscription.Validate(account) == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestClaudeServerUsageStateRequiresManagedClaudeAndAccountConnection(t *testing.T) {
	chatgpt := claudeUsageAccount(t, linkedClaudeUsage())
	chatgpt.SubscriptionService = SubscriptionChatGPT
	chatgpt.Subscription.ClaudeCredential = ""
	legacy := claudeUsageAccount(t, linkedClaudeUsage())
	legacy.Subscription.ClaudeCredential = ""
	disconnected := claudeUsageAccount(t, linkedClaudeUsage())
	disconnected.Connection = nil
	disconnected.Health = AccountDisconnected
	disconnected.Subscription.Generation, disconnected.Subscription.IdentityCommitment = "", ""
	for name, account := range map[string]Account{"chatgpt": chatgpt, "device-owned claude": legacy, "disconnected account": disconnected} {
		if account.Subscription.Validate(account) == nil {
			t.Fatalf("%s kept live usage authority", name)
		}
	}
	// Only retired evidence may survive a disconnected execution account.
	disconnected.Subscription.ClaudeUsage = &ClaudeServerUsageState{Connection: ClaudeUsageReconnectRequired, Generation: NewID(), RetiredCredentials: []ID{NewID()}}
	if err := disconnected.Subscription.Validate(disconnected); err != nil {
		t.Fatalf("a retired usage connection was rejected: %v", err)
	}
}

func TestClaudeServerUsageStateIsOptionalAndRoundTrips(t *testing.T) {
	historical := claudeUsageAccount(t, nil)
	raw, err := json.Marshal(historical.Subscription)
	if err != nil || strings.Contains(string(raw), "claude_usage") {
		t.Fatalf("historical state gained a usage section: %s %v", raw, err)
	}
	account := claudeUsageAccount(t, linkedClaudeUsage())
	raw, err = json.Marshal(account.Subscription)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SubscriptionState
	if err := Decode(raw, &decoded); err != nil || decoded.ClaudeUsage == nil || decoded.ClaudeUsage.Generation != account.Subscription.ClaudeUsage.Generation {
		t.Fatalf("round trip failed: %v", err)
	}
	if err := decoded.Validate(account); err != nil {
		t.Fatal(err)
	}
}

func TestApplyClaudeUsageQuotaMergesSparseWindowsAndProjectsNonBlockingEvidence(t *testing.T) {
	account := claudeUsageAccount(t, linkedClaudeUsage())
	usage := account.Subscription.ClaudeUsage
	reset := claudeUsageNow.Add(2 * time.Hour)
	first, err := ParseClaudeUsageQuota([]byte(`{"five_hour":{"utilization":25,"resets_at":"`+reset.Format(time.RFC3339)+`"},"seven_day":{"utilization":50}}`), claudeUsageNow)
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := ApplyClaudeUsageQuota(&account, usage.Generation, first, claudeUsageNow); err != nil || !applied {
		t.Fatalf("first apply: %v %v", applied, err)
	}
	if err := account.Validate(); err != nil {
		t.Fatalf("projected state is invalid: %v", err)
	}
	if len(account.Quota) != 2 || account.Quota[0].ComparisonGroup != "claude" || account.Quota[0].Blocking || *account.Quota[0].Remaining != 0.75 || *account.Quota[1].Remaining != 0.5 || *account.Quota[0].DurationMinutes != 300 || *account.Quota[1].DurationMinutes != 10080 {
		t.Fatalf("unexpected projection %+v", account.Quota)
	}
	if state, score, _, _ := quotaEvidence(account.Quota, claudeUsageNow); state == Observed || score != nil {
		t.Fatal("usage evidence became routing evidence")
	}
	// A later sparse success refreshes only the window it reports.
	later := claudeUsageNow.Add(5 * time.Minute)
	sparse, err := ParseClaudeUsageQuota([]byte(`{"five_hour":{"utilization":60},"seven_day":null}`), later)
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := ApplyClaudeUsageQuota(&account, usage.Generation, sparse, later); err != nil || !applied {
		t.Fatalf("sparse apply: %v %v", applied, err)
	}
	if usage.Windows[0].Remaining != 0.4 || !usage.Windows[0].ObservedAt.Equal(later) || usage.Windows[1].Remaining != 0.5 || !usage.Windows[1].ObservedAt.Equal(claudeUsageNow) || !usage.LastSuccessAt.Equal(later) {
		t.Fatalf("sparse merge refreshed or erased evidence: %+v", usage.Windows)
	}
	if usage.Windows[0].ResetAt != nil {
		t.Fatal("a missing reset time kept a stale reset")
	}
	if err := account.Validate(); err != nil {
		t.Fatalf("merged state is invalid: %v", err)
	}
	// An older observation and a failure never rewind or advance evidence.
	if applied, err := ApplyClaudeUsageQuota(&account, usage.Generation, first, later); err != nil || applied || usage.Windows[0].Remaining != 0.4 {
		t.Fatalf("an older observation replaced newer evidence: %v %v", applied, err)
	}
	backoff := later.Add(5 * time.Minute)
	if err := usage.RecordFailure(ClaudeUsageFailureRateLimited, &backoff); err != nil {
		t.Fatal(err)
	}
	if !usage.LastSuccessAt.Equal(later) || len(usage.Windows) != 2 || usage.Failure != ClaudeUsageFailureRateLimited || usage.BackoffUntil == nil {
		t.Fatalf("failure changed last-success evidence: %+v", usage)
	}
	if err := account.Validate(); err != nil {
		t.Fatalf("failed state is invalid: %v", err)
	}
	// A later success clears the failure and backoff.
	fresh := ClaudeUsageQuota{ObservedAt: later.Add(6 * time.Minute), Windows: []ClaudeUsageWindow{{ID: ClaudeUsageSevenDay, Remaining: 0.1}}}
	if applied, err := ApplyClaudeUsageQuota(&account, usage.Generation, fresh, later.Add(6*time.Minute)); err != nil || !applied || usage.Failure != ClaudeUsageFailureNone || usage.BackoffUntil != nil {
		t.Fatalf("a success did not clear the failure: %v %v %+v", applied, err, usage)
	}
	ClearClaudeUsageQuota(&account)
	if len(account.Quota) != 0 {
		t.Fatal("projected evidence survived clear")
	}
}

func TestApplyClaudeUsageQuotaRejectsUnauthorizedOrInvalidObservations(t *testing.T) {
	valid := ClaudeUsageQuota{ObservedAt: claudeUsageNow, Windows: []ClaudeUsageWindow{{ID: ClaudeUsageFiveHour, Remaining: 0.5}}}
	build := func(edit func(*Account)) Account {
		account := claudeUsageAccount(t, linkedClaudeUsage())
		edit(&account)
		return account
	}
	for name, account := range map[string]Account{
		"connecting": build(func(a *Account) {
			a.Subscription.ClaudeUsage = &ClaudeServerUsageState{Connection: ClaudeUsageConnecting, Generation: a.Subscription.ClaudeUsage.Generation, Operation: claudeUsageOperation(SubscriptionWaiting)}
		}),
		"unpublished": build(func(a *Account) { a.Subscription.ClaudeUsage.Published = false }),
		"no usage":    build(func(a *Account) { a.Subscription.ClaudeUsage = nil }),
		"chatgpt":     build(func(a *Account) { a.SubscriptionService = SubscriptionChatGPT }),
	} {
		generation := ID("")
		if account.Subscription.ClaudeUsage != nil {
			generation = account.Subscription.ClaudeUsage.Generation
		}
		if applied, err := ApplyClaudeUsageQuota(&account, generation, valid, claudeUsageNow); err == nil || applied || len(account.Quota) != 0 {
			t.Fatalf("%s accepted usage evidence", name)
		}
	}
	account := claudeUsageAccount(t, linkedClaudeUsage())
	usage := account.Subscription.ClaudeUsage
	for name, observation := range map[string]struct {
		generation ID
		quota      ClaudeUsageQuota
	}{
		"other generation": {NewID(), valid},
		"empty":            {usage.Generation, ClaudeUsageQuota{ObservedAt: claudeUsageNow}},
		"future":           {usage.Generation, ClaudeUsageQuota{ObservedAt: claudeUsageNow.Add(time.Hour), Windows: valid.Windows}},
		"unknown window":   {usage.Generation, ClaudeUsageQuota{ObservedAt: claudeUsageNow, Windows: []ClaudeUsageWindow{{ID: "x", Remaining: 0.5}}}},
		"duplicate window": {usage.Generation, ClaudeUsageQuota{ObservedAt: claudeUsageNow, Windows: append(valid.Windows, valid.Windows...)}},
		"out of range":     {usage.Generation, ClaudeUsageQuota{ObservedAt: claudeUsageNow, Windows: []ClaudeUsageWindow{{ID: ClaudeUsageFiveHour, Remaining: 1.1}}}},
	} {
		if applied, err := ApplyClaudeUsageQuota(&account, observation.generation, observation.quota, claudeUsageNow); err == nil || applied || len(usage.Windows) != 0 || len(account.Quota) != 0 {
			t.Fatalf("%s observation changed state", name)
		}
	}
	if err := usage.RecordFailure(ClaudeUsageFailureNone, nil); err == nil {
		t.Fatal("an empty failure was recorded")
	}
	unlinked := &ClaudeServerUsageState{Connection: ClaudeUsageReconnectRequired, Generation: NewID()}
	if err := unlinked.RecordFailure(ClaudeUsageFailureUnavailable, nil); err == nil {
		t.Fatal("a failure was recorded on an unlinked connection")
	}
}

func TestClaudeServerUsageRecordFailureRejectsInvalidBackoffPairsBeforeMutation(t *testing.T) {
	future := claudeUsageNow.Add(5 * time.Minute)
	zero := time.Time{}
	for name, test := range map[string]struct {
		failure ClaudeUsageFailure
		backoff *time.Time
	}{
		"unauthorized with backoff":    {ClaudeUsageFailureUnauthorized, &future},
		"malformed with backoff":       {ClaudeUsageFailureMalformed, &future},
		"identity changed and backoff": {ClaudeUsageFailureIdentityChanged, &future},
		"empty with backoff":           {ClaudeUsageFailureEmpty, &future},
		"unsupported with backoff":     {ClaudeUsageFailureUnsupported, &future},
		"rate limited zero time":       {ClaudeUsageFailureRateLimited, &zero},
		"unavailable year overflow":    {ClaudeUsageFailureUnavailable, func() *time.Time { v := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC); return &v }()},
	} {
		usage := linkedClaudeUsage()
		prior := future.Add(time.Hour)
		usage.Failure, usage.BackoffUntil = ClaudeUsageFailureUnavailable, &prior
		if err := usage.RecordFailure(test.failure, test.backoff); err == nil {
			t.Fatalf("%s was recorded", name)
		}
		if usage.Failure != ClaudeUsageFailureUnavailable || usage.BackoffUntil == nil || !usage.BackoffUntil.Equal(prior) {
			t.Fatalf("%s mutated the state before failing: %+v", name, usage)
		}
	}
	for _, failure := range []ClaudeUsageFailure{ClaudeUsageFailureRateLimited, ClaudeUsageFailureForbidden, ClaudeUsageFailureUnavailable} {
		account := claudeUsageAccount(t, linkedClaudeUsage())
		if err := account.Subscription.ClaudeUsage.RecordFailure(failure, &future); err != nil || account.Validate() != nil {
			t.Fatalf("%s with a valid backoff was rejected: %v", failure, err)
		}
	}
	for _, failure := range []ClaudeUsageFailure{ClaudeUsageFailureUnauthorized, ClaudeUsageFailureMalformed, ClaudeUsageFailureEmpty, ClaudeUsageFailureIdentityChanged, ClaudeUsageFailureUnsupported} {
		account := claudeUsageAccount(t, linkedClaudeUsage())
		if err := account.Subscription.ClaudeUsage.RecordFailure(failure, nil); err != nil || account.Validate() != nil {
			t.Fatalf("%s without a backoff was rejected: %v", failure, err)
		}
	}
}

func TestApplyClaudeUsageQuotaPreservesOtherComparisonGroups(t *testing.T) {
	account := claudeUsageAccount(t, linkedClaudeUsage())
	other := 0.3
	account.Quota = []QuotaWindow{{ID: "five_hour", ComparisonGroup: "chatgpt", Blocking: true, Remaining: &other, ObservedAt: claudeUsageNow, State: Observed}}
	quota := ClaudeUsageQuota{ObservedAt: claudeUsageNow, Windows: []ClaudeUsageWindow{{ID: ClaudeUsageFiveHour, Remaining: 0.5}}}
	if _, err := ApplyClaudeUsageQuota(&account, account.Subscription.ClaudeUsage.Generation, quota, claudeUsageNow); err != nil {
		t.Fatal(err)
	}
	if len(account.Quota) != 2 || account.Quota[0].ComparisonGroup != "chatgpt" || *account.Quota[0].Remaining != 0.3 || account.Quota[1].ComparisonGroup != "claude" {
		t.Fatalf("projection disturbed unrelated evidence: %+v", account.Quota)
	}
	ClearClaudeUsageQuota(&account)
	if len(account.Quota) != 1 || account.Quota[0].ComparisonGroup != "chatgpt" {
		t.Fatalf("clear disturbed unrelated evidence: %+v", account.Quota)
	}
}

func TestRetireClaudeUsageRevokesAuthorityAndKeepsCleanupEvidence(t *testing.T) {
	observed := claudeUsageNow
	linkedWithEvidence := func() *ClaudeServerUsageState {
		usage := linkedClaudeUsage()
		usage.Operation = claudeUsageOperation(SubscriptionSucceeded)
		usage.Refresh = ClaudeUsageRenewing
		backoff := observed.Add(time.Hour)
		usage.Failure, usage.BackoffUntil = ClaudeUsageFailureRateLimited, &backoff
		usage.LastSuccessAt = &observed
		usage.Windows = []ClaudeUsageWindowEvidence{{ID: ClaudeUsageFiveHour, Remaining: 0.5, ObservedAt: observed}}
		usage.RetiredCredentials = []ID{NewID()}
		return usage
	}
	uncertain := linkedWithEvidence()
	uncertain.Refresh = ClaudeUsageRotationUncertain
	confirming := &ClaudeServerUsageState{Connection: ClaudeUsageConfirming, Generation: NewID(), Operation: claudeUsageOperation(SubscriptionSucceeded), CredentialID: NewID(), Identity: linkedClaudeUsage().Identity}
	for name, usage := range map[string]*ClaudeServerUsageState{
		"linked":       linkedWithEvidence(),
		"uncertain":    uncertain,
		"confirming":   confirming,
		"connecting":   {Connection: ClaudeUsageConnecting, Generation: NewID(), Operation: claudeUsageOperation(SubscriptionWaiting)},
		"preparing":    {Connection: ClaudeUsageConnecting, Generation: NewID(), Operation: claudeUsageOperation(SubscriptionPreparing)},
		"reconnecting": {Connection: ClaudeUsageReconnectRequired, Generation: NewID()},
	} {
		account := claudeUsageAccount(t, usage)
		account.Quota = projectClaudeUsageQuota(nil, usage.Windows)
		credential, retiredBefore, windows, refresh := usage.CredentialID, len(usage.RetiredCredentials), len(usage.Windows), usage.Refresh
		if err := RetireClaudeUsage(&account); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		account.Connection, account.Health = nil, AccountDisconnected
		account.Subscription.Generation, account.Subscription.IdentityCommitment = "", ""
		if err := account.Validate(); err != nil {
			t.Fatalf("%s retired state is invalid on a disconnected account: %v", name, err)
		}
		if usage.Reading() || usage.Connection != ClaudeUsageReconnectRequired || usage.Published || usage.CredentialID != "" || usage.BackoffUntil != nil || len(account.Quota) != 0 {
			t.Fatalf("%s kept live usage authority: %+v", name, usage)
		}
		if credential != "" && !slices.Contains(usage.RetiredCredentials, credential) || len(usage.RetiredCredentials) < retiredBefore {
			t.Fatalf("%s lost cleanup evidence: %+v", name, usage.RetiredCredentials)
		}
		if len(usage.Windows) != windows || refresh == ClaudeUsageRotationUncertain && usage.Refresh != refresh || refresh != ClaudeUsageRotationUncertain && usage.Refresh != ClaudeUsageIdle {
			t.Fatalf("%s changed evidence or recovery phase: %+v", name, usage)
		}
		if usage.Operation != nil && usage.Operation.Active() {
			t.Fatalf("%s kept an active login operation", name)
		}
		if err := RetireClaudeUsage(&account); err != nil || len(usage.RetiredCredentials) > retiredBefore+1 {
			t.Fatalf("%s retirement is not idempotent: %v", name, err)
		}
	}
	plain := claudeUsageAccount(t, nil)
	if err := RetireClaudeUsage(&plain); err != nil || plain.Subscription.ClaudeUsage != nil {
		t.Fatal("an account without usage changed")
	}
	full := linkedClaudeUsage()
	for len(full.RetiredCredentials) < MaxClaudeUsageRetiredCredentials {
		full.RetiredCredentials = append(full.RetiredCredentials, NewID())
	}
	before := *full
	if err := full.Retire(); err == nil || full.Connection != before.Connection || full.CredentialID != before.CredentialID || !full.Published {
		t.Fatal("a full retired list was exceeded or mutated")
	}
}
