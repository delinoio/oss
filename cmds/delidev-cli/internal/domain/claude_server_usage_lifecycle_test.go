// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"slices"
	"testing"
	"time"
)

func lifecycleOperation() ClaudeUsageOperation {
	return ClaudeUsageOperation{ID: NewID(), Epoch: NewID(), Actor: Principal{Type: OwnerDevice}, State: SubscriptionPreparing, StartedAt: claudeUsageNow, ExpiresAt: claudeUsageNow.Add(10 * time.Minute)}
}

func validateLifecycle(t *testing.T, name string, usage *ClaudeServerUsageState) {
	t.Helper()
	account := claudeUsageAccount(t, usage)
	if err := account.Validate(); err != nil {
		t.Fatalf("%s produced an invalid section: %v", name, err)
	}
}

func TestClaudeUsageLoginLifecycleReachesLinkedOnlyThroughReview(t *testing.T) {
	usage, err := NewClaudeUsageLogin(nil, NewID(), lifecycleOperation())
	if err != nil {
		t.Fatal(err)
	}
	validateLifecycle(t, "start", usage)
	if !usage.ProfileCleanup || usage.Reading() {
		t.Fatal("a new login must owe profile cleanup and grant no read authority")
	}
	if err := usage.MarkWaiting(); err != nil {
		t.Fatal(err)
	}
	if usage.MarkWaiting() == nil {
		t.Fatal("waiting was recorded twice")
	}
	claim := NewID()
	if usage.ConsumeCode() == nil {
		t.Fatal("a code was consumed before it was claimed")
	}
	if err := usage.ClaimCode(claim); err != nil {
		t.Fatal(err)
	}
	if usage.ClaimCode(NewID()) == nil {
		t.Fatal("a second code claim was accepted")
	}
	if err := usage.ConsumeCode(); err != nil {
		t.Fatal(err)
	}
	if usage.ConsumeCode() == nil {
		t.Fatal("a code was consumed twice")
	}
	validateLifecycle(t, "waiting", usage)
	credential := NewID()
	identity := ClaudeUsageIdentity{Email: "person@example.com", Plan: "max", Commitment: claudeUsageCommitment}
	if usage.Stage(identity, credential, true, claudeUsageNow) == nil {
		t.Fatal("a credential without a recorded intent was staged")
	}
	if err := usage.RecordCredentialIntent(credential); err != nil {
		t.Fatal(err)
	}
	validateLifecycle(t, "intent", usage)
	if err := usage.Stage(identity, credential, true, claudeUsageNow); err != nil {
		t.Fatal(err)
	}
	validateLifecycle(t, "stage", usage)
	if usage.Connection != ClaudeUsageConfirming || usage.Published || usage.Reading() || slices.Contains(usage.RetiredCredentials, credential) || usage.ProfileCleanup {
		t.Fatalf("staging linked or retained the credential: %+v", usage)
	}
	if err := usage.Link(claudeUsageNow); err != nil {
		t.Fatal(err)
	}
	validateLifecycle(t, "link", usage)
	if !usage.Reading() || usage.Link(claudeUsageNow) == nil {
		t.Fatal("link did not grant exactly one publication")
	}
}

func TestClaudeUsageStageKeepsProfileCleanupWhenNativeRemovalIsUnproven(t *testing.T) {
	usage, _ := NewClaudeUsageLogin(nil, NewID(), lifecycleOperation())
	_ = usage.MarkWaiting()
	credential := NewID()
	_ = usage.RecordCredentialIntent(credential)
	if err := usage.Stage(ClaudeUsageIdentity{Email: "person@example.com", Commitment: claudeUsageCommitment}, credential, false, claudeUsageNow); err != nil {
		t.Fatal(err)
	}
	if !usage.ProfileCleanup || !usage.OwesCleanup() {
		t.Fatal("unproven native removal was forgotten")
	}
	usage.ConfirmCleanup(nil, true)
	if usage.ProfileCleanup {
		t.Fatal("confirmed removal was kept")
	}
}

func TestClaudeUsageSettleAndDiscardKeepOwedCleanup(t *testing.T) {
	staged := func() *ClaudeServerUsageState {
		usage, _ := NewClaudeUsageLogin(nil, NewID(), lifecycleOperation())
		_ = usage.MarkWaiting()
		return usage
	}
	usage := staged()
	intent := NewID()
	_ = usage.RecordCredentialIntent(intent)
	if err := usage.Settle(SubscriptionCanceled, nil); err != nil {
		t.Fatal(err)
	}
	validateLifecycle(t, "settle", usage)
	if usage.Connection != ClaudeUsageReconnectRequired || usage.Operation.State != SubscriptionCanceled || !slices.Contains(usage.RetiredCredentials, intent) || !usage.ProfileCleanup {
		t.Fatalf("settle lost owed cleanup: %+v", usage)
	}
	for _, state := range []SubscriptionLoginState{SubscriptionSucceeded, SubscriptionPreparing, SubscriptionWaiting, SubscriptionRecovery} {
		fresh := staged()
		if fresh.Settle(state, nil) == nil {
			t.Fatalf("settle accepted %s", state)
		}
	}
	usage.ConfirmCleanup([]ID{intent}, true)
	if usage.OwesCleanup() {
		t.Fatal("confirmed cleanup stayed owed")
	}
	validateLifecycle(t, "cleaned", usage)

	review, _ := NewClaudeUsageLogin(nil, NewID(), lifecycleOperation())
	_ = review.MarkWaiting()
	credential := NewID()
	_ = review.RecordCredentialIntent(credential)
	_ = review.Stage(ClaudeUsageIdentity{Email: "person@example.com", Commitment: claudeUsageCommitment}, credential, true, claudeUsageNow)
	if err := review.DiscardConfirmation(SubscriptionCanceled); err != nil {
		t.Fatal(err)
	}
	if review.DiscardConfirmation(SubscriptionExpired) == nil {
		t.Fatal("a discarded review was discarded again")
	}
	validateLifecycle(t, "discard", review)
	if review.Identity != nil || review.CredentialID != "" || !slices.Contains(review.RetiredCredentials, credential) || review.Operation.State != SubscriptionCanceled {
		t.Fatalf("discard kept an unconfirmed credential: %+v", review)
	}
}

func TestClaudeUsageRecoveryFencesActiveLoginOnce(t *testing.T) {
	usage, _ := NewClaudeUsageLogin(nil, NewID(), lifecycleOperation())
	if !usage.MarkRecovery() || usage.MarkRecovery() {
		t.Fatal("recovery fencing is not exactly once")
	}
	validateLifecycle(t, "recovery", usage)
	if usage.Settle(SubscriptionFailed, &NativeSubscriptionDiagnostic{RequiredVersion: ClaudeProtocolVersion, Phase: NativeSubscriptionCleanup, Code: RecoveryRequired, CorrelationID: usage.Operation.ID}) != nil {
		t.Fatal("a recovery login could not be settled")
	}
	validateLifecycle(t, "recovery settle", usage)
}

func TestClaudeUsageNewLoginRequiresSettledCleanSection(t *testing.T) {
	settled := func(edit func(*ClaudeServerUsageState)) *ClaudeServerUsageState {
		usage, _ := NewClaudeUsageLogin(nil, NewID(), lifecycleOperation())
		_ = usage.Settle(SubscriptionFailed, nil)
		usage.ProfileCleanup = false
		if edit != nil {
			edit(usage)
		}
		return usage
	}
	if _, err := NewClaudeUsageLogin(settled(nil), NewID(), lifecycleOperation()); err != nil {
		t.Fatalf("a settled section could not reconnect: %v", err)
	}
	owed := NewID()
	next, err := NewClaudeUsageLogin(settled(func(u *ClaudeServerUsageState) { u.RetiredCredentials = []ID{owed} }), NewID(), lifecycleOperation())
	if err != nil || !slices.Equal(next.RetiredCredentials, []ID{owed}) {
		t.Fatalf("owed references were not carried over: %v", err)
	}
	if _, err := NewClaudeUsageLogin(settled(func(u *ClaudeServerUsageState) { u.ProfileCleanup = true }), NewID(), lifecycleOperation()); err == nil {
		t.Fatal("a leftover native credential was admitted")
	}
	if _, err := NewClaudeUsageLogin(linkedClaudeUsage(), NewID(), lifecycleOperation()); err == nil {
		t.Fatal("a linked connection was replaced without disconnecting")
	}
	live, _ := NewClaudeUsageLogin(nil, NewID(), lifecycleOperation())
	if _, err := NewClaudeUsageLogin(live, NewID(), lifecycleOperation()); err == nil {
		t.Fatal("a second login started beside a live one")
	}
}

func TestClaudeUsageDisconnectClearsEvidenceAndKeepsCleanup(t *testing.T) {
	usage := linkedClaudeUsage()
	credential := usage.CredentialID
	observed := claudeUsageNow.Add(-time.Minute)
	usage.LastSuccessAt = &observed
	usage.Windows = []ClaudeUsageWindowEvidence{{ID: ClaudeUsageFiveHour, Remaining: 0.5, ObservedAt: observed}}
	usage.Refresh = ClaudeUsageRotationUncertain
	operation := lifecycleOperation()
	operation.State = SubscriptionSucceeded
	usage.Operation = &operation
	if err := usage.Disconnect(); err != nil {
		t.Fatal(err)
	}
	validateLifecycle(t, "disconnect", usage)
	if usage.Reading() || usage.Identity != nil || len(usage.Windows) != 0 || usage.LastSuccessAt != nil || usage.Refresh != ClaudeUsageIdle || !slices.Contains(usage.RetiredCredentials, credential) || usage.Operation.State != SubscriptionCanceled {
		t.Fatalf("disconnect kept authority or evidence: %+v", usage)
	}
	full := linkedClaudeUsage()
	for len(full.RetiredCredentials) < MaxClaudeUsageRetiredCredentials {
		full.RetiredCredentials = append(full.RetiredCredentials, NewID())
	}
	before := *full
	if full.Disconnect() == nil || full.Connection != before.Connection || full.CredentialID != before.CredentialID {
		t.Fatal("a full retirement list was not fail-closed without mutation")
	}
}
