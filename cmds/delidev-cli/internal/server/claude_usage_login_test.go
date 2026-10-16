// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestClaudeUsageLoginLinksOnlyAfterConfirmationAndPreservesExecution(t *testing.T) {
	f := newUsageFixture(t)
	_, before := f.record()
	executionGeneration, executionHealth := before.Subscription.Generation, before.Health
	executionRefs := f.executionRefs()
	if len(executionRefs) != 1 || len(f.usageRefs()) != 0 {
		t.Fatalf("fixture references: %d execution, %d usage", len(executionRefs), len(f.usageRefs()))
	}
	r, _ := f.record()
	started, err := f.start("")
	if err != nil {
		t.Fatal(err)
	}
	if started.Replayed || started.OperationId == "" || started.UsageGeneration != started.OperationId {
		t.Fatalf("start response %+v", started)
	}
	// A replay of the original request returns the original operation and
	// launches nothing.
	replay, err := f.client.StartClaudeUsage(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.StartClaudeUsageRequest{Mutation: &pb.Mutation{RequestId: started.OperationId, Id: string(r.ID), ExpectedRevision: r.Revision}}))
	if err != nil || !replay.Msg.Replayed || replay.Msg.OperationId != started.OperationId {
		t.Fatalf("replay %v %v", replay, err)
	}
	if _, err := f.start(""); err == nil {
		t.Fatal("a second start was admitted while the first login is active")
	}
	if p, err := f.progress(started.OperationId, started.OperationId); err != nil || p.Url != "" || p.State != pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_PREPARING {
		t.Fatalf("progress before the child produced an address: %v %v", p, err)
	}
	done := f.runLogin()
	// Polling and reopening never relaunch native work.
	close(f.native.login.ready)
	waiting := f.awaitUsageState(started.OperationId, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	if waiting.Url != usageFixtureURL() || waiting.LoginMethod != pb.SubscriptionLoginMethod_SUBSCRIPTION_LOGIN_METHOD_BROWSER_CODE || !waiting.CodeRequested || waiting.Identity != nil {
		t.Fatalf("waiting presentation %+v", waiting)
	}
	for i := 0; i < 3; i++ {
		if _, err := f.progress(started.OperationId, started.OperationId); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.submit(started.OperationId, usageFixtureCode); err != nil {
		t.Fatal(err)
	}
	if err := f.submit(started.OperationId, "second#attempt"); err == nil {
		t.Fatal("a second approval input was admitted")
	}
	select {
	case code := <-f.native.login.codes:
		if string(code) != usageFixtureCode {
			t.Fatal("the original approval code changed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("approval code never reached the original child")
	}
	if len(f.service.claudeUsage.codes[domain.ID(started.OperationId)]) != 0 {
		t.Fatal("the approval code stayed in server memory after consumption")
	}
	f.native.store.set(usageNativeRecord(usageFixtureAccess, usageFixtureRefresh, time.Now().Add(time.Hour)))
	close(f.native.login.finish)
	awaitServerFixture(t, done)

	confirming := f.awaitUsageState(started.OperationId, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_CONFIRMING)
	if confirming.Identity == nil || confirming.Identity.Email != "alex@example.com" || confirming.Identity.Organization != "Example Org" || confirming.Identity.Plan != "max" || confirming.Url != "" {
		t.Fatalf("identity review %+v", confirming)
	}
	usage := f.usage()
	// Nothing is published or scheduled before the explicit link.
	if usage.Linked() || usage.LinkedAt != nil || usage.CredentialID != "" || len(usage.Windows) != 0 || usage.Generation != "" || usage.DueAt(time.Now().Add(time.Hour)) {
		t.Fatalf("usage was attached before confirmation: %+v", usage)
	}
	if _, a := f.record(); len(a.Quota) != 0 {
		t.Fatal("quota windows were projected before confirmation")
	}
	if f.native.login.closeCalls.Load() == 0 || f.native.closes.Load() == 0 {
		t.Fatal("the original child or its profile lease was not released")
	}
	if present, _ := f.native.store.Present(context.Background()); present {
		t.Fatal("native credentials stayed in the profile after capture")
	}
	refs := f.usageRefs()
	if len(refs) != 1 || refs[0].ID != usage.Operation.CredentialID || refs[0].Purpose != credentials.ClaudeUsage {
		t.Fatalf("the staged credential is not a distinct claude-usage reference: %+v", refs)
	}
	if got := f.executionRefs(); len(got) != 1 || got[0] != executionRefs[0] {
		t.Fatal("the execution reference changed")
	}
	f.secrets.mu.Lock()
	raw := f.secrets.values[refs[0]]
	f.secrets.mu.Unlock()
	bundle, err := subscription.OpenClaudeUsage(raw, f.service.Identity.ServerID, f.input.AccountID, domain.ID(started.OperationId), refs[0].ID, time.Now())
	if err != nil || string(bundle.RefreshToken) != usageFixtureRefresh {
		t.Fatalf("the vault envelope is not bound to server, account and usage generation: %v", err)
	}
	bundle.Clear()
	if _, err := subscription.OpenClaudeToken(raw, f.service.Identity.ServerID, f.input.AccountID, domain.ID(started.OperationId)); err == nil {
		t.Fatal("a usage credential opened as an execution token")
	}

	confirmed, err := f.confirm(started.OperationId)
	if err != nil || confirmed.Replayed {
		t.Fatal(err)
	}
	linked := f.usage()
	if !linked.Linked() || linked.Generation != domain.ID(started.OperationId) || linked.CredentialID != refs[0].ID || linked.Identity.Email != "alex@example.com" || linked.Identity.Plan != "max" || len(linked.Identity.Commitment) != 64 {
		t.Fatalf("linked state %+v", linked)
	}
	if linked.Operation.State != domain.ClaudeLimitLoginLinked || linked.Operation.Identity != nil || linked.Operation.CredentialID != "" {
		t.Fatalf("operation after link %+v", linked.Operation)
	}
	if _, a := f.record(); a.Subscription.Generation != executionGeneration || a.Health != executionHealth || a.Connection == nil {
		t.Fatal("linking usage changed the execution generation or readiness")
	}
	// The confirmed link is idempotent for the original request only.
	if again, err := f.confirm(started.OperationId); err == nil && again.Replayed {
		t.Fatal("a fresh request replayed")
	}
	if p, err := f.progress(started.OperationId, started.OperationId); err != nil || p.State != pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_LINKED {
		t.Fatalf("linked progress %v %v", p, err)
	}
	if f.native.loginStarts.Load() != 1 {
		t.Fatalf("exactly one native sign-in is allowed, saw %d", f.native.loginStarts.Load())
	}
}

func TestClaudeUsageCancelAndBackBeforeConfirmationPublishNothing(t *testing.T) {
	t.Run("back at the identity review", func(t *testing.T) {
		f := newUsageFixture(t)
		started, _ := f.start("")
		done := f.runLogin()
		close(f.native.login.ready)
		f.awaitUsageState(started.OperationId, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
		if err := f.submit(started.OperationId, usageFixtureCode); err != nil {
			t.Fatal(err)
		}
		<-f.native.login.codes
		f.native.store.set(usageNativeRecord(usageFixtureAccess, usageFixtureRefresh, time.Now().Add(time.Hour)))
		close(f.native.login.finish)
		awaitServerFixture(t, done)
		f.awaitUsageState(started.OperationId, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_CONFIRMING)
		if len(f.usageRefs()) != 1 {
			t.Fatal("the credential was not staged")
		}
		if _, err := f.cancel(started.OperationId); err != nil {
			t.Fatal(err)
		}
		usage := f.usage()
		if usage.Linked() || usage.Operation.State != domain.ClaudeLimitLoginCanceled || usage.Operation.Identity != nil || usage.Operation.CredentialID != "" || usage.CleanupPending || len(usage.RetiredCredentials) != 0 {
			t.Fatalf("cancel left state %+v", usage)
		}
		if len(f.usageRefs()) != 0 {
			t.Fatal("the unconfirmed credential was not discarded")
		}
		if _, err := f.confirm(started.OperationId); err == nil {
			t.Fatal("a canceled login confirmed")
		}
		if _, a := f.record(); len(a.Quota) != 0 {
			t.Fatal("canceled login published quota")
		}
	})
	t.Run("cancel while the original child is live", func(t *testing.T) {
		f := newUsageFixture(t)
		started, _ := f.start("")
		done := f.runLogin()
		close(f.native.login.ready)
		f.awaitUsageState(started.OperationId, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
		f.native.store.set(usageNativeRecord(usageFixtureAccess, usageFixtureRefresh, time.Now().Add(time.Hour)))
		if _, err := f.cancel(started.OperationId); err != nil {
			t.Fatal(err)
		}
		awaitServerFixture(t, done)
		usage := f.usage()
		if usage.Operation.State != domain.ClaudeLimitLoginCanceled || usage.Linked() || len(f.usageRefs()) != 0 || !f.native.login.closed.Load() {
			t.Fatalf("live cancel: state %+v refs %d closed %v", usage.Operation, len(f.usageRefs()), f.native.login.closed.Load())
		}
		if present, _ := f.native.store.Present(context.Background()); present {
			t.Fatal("unconfirmed native credentials were not discarded")
		}
	})
	t.Run("cancel before the runner claims", func(t *testing.T) {
		f := newUsageFixture(t)
		started, _ := f.start("")
		if _, err := f.cancel(started.OperationId); err != nil {
			t.Fatal(err)
		}
		f.service.runClaudeUsageLogin(f.owner(), f.input.AccountID)
		if f.native.opens.Load() != 0 || f.usage().Operation.State != domain.ClaudeLimitLoginCanceled {
			t.Fatal("a canceled operation launched native work")
		}
	})
}

func TestClaudeUsageLoginFailuresNeverLinkOrLeakCredentials(t *testing.T) {
	scenarios := []struct {
		name    string
		mutate  func(*usageFixture)
		state   domain.ClaudeLimitLoginState
		failure domain.ClaudeLimitFailure
	}{
		{"localhost callback", func(f *usageFixture) {
			f.native.login.method = domain.SubscriptionBrowserCallback
		}, domain.ClaudeLimitLoginUnsupported, domain.ClaudeLimitFailureLoginNotInteractive},
		{"login child failure", func(f *usageFixture) { f.native.login.waitErr = domain.Fail(domain.Unavailable, "exit", "") }, domain.ClaudeLimitLoginFailed, domain.ClaudeLimitFailureLoginRejected},
		{"identity cannot be validated", func(f *usageFixture) { f.native.identityErr = errors.New("status") }, domain.ClaudeLimitLoginFailed, domain.ClaudeLimitFailureLoginRejected},
		{"pre-existing native storage", func(f *usageFixture) {
			f.native.store.set(usageNativeRecord(usageFixtureAccess, usageFixtureRefresh, time.Now().Add(time.Hour)))
		}, domain.ClaudeLimitLoginFailed, domain.ClaudeLimitFailureProfileUnsafe},
		{"unreadable storage", func(f *usageFixture) {
			f.native.store.readErr = domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureStorage, 0)
		}, domain.ClaudeLimitLoginFailed, domain.ClaudeLimitFailureStorage},
		{"malformed credential record", func(f *usageFixture) {}, domain.ClaudeLimitLoginFailed, domain.ClaudeLimitFailureCredentials},
		{"uncertain child cleanup", func(f *usageFixture) { f.native.login.closeErr = errors.New("still running") }, domain.ClaudeLimitLoginRecovery, domain.ClaudeLimitFailureCleanupUncertain},
		{"storage removal fails", func(f *usageFixture) { f.native.store.removeErr = errors.New("busy") }, domain.ClaudeLimitLoginRecovery, domain.ClaudeLimitFailureCleanupUncertain},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			f := newUsageFixture(t)
			scenario.mutate(f)
			started, err := f.start("")
			if err != nil {
				t.Fatal(err)
			}
			done := f.runLogin()
			close(f.native.login.ready)
			if scenario.name == "localhost callback" || scenario.name == "pre-existing native storage" {
				// Refused before any address is presented or any code is accepted.
			} else {
				f.awaitUsageState(started.OperationId, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
				if err := f.submit(started.OperationId, usageFixtureCode); err != nil {
					t.Fatal(err)
				}
				<-f.native.login.codes
				if scenario.name != "pre-existing native storage" && scenario.name != "malformed credential record" {
					f.native.store.set(usageNativeRecord(usageFixtureAccess, usageFixtureRefresh, time.Now().Add(time.Hour)))
				}
				if scenario.name == "malformed credential record" {
					f.native.store.set([]byte(`{"claudeAiOauth":{"accessToken":"nope"}}`))
				}
				close(f.native.login.finish)
			}
			awaitServerFixture(t, done)
			usage := f.usage()
			if usage.Linked() || usage.Operation == nil || usage.Operation.State != scenario.state || usage.Operation.Failure != scenario.failure {
				t.Fatalf("state %+v", usage.Operation)
			}
			if len(f.usageRefs()) != 0 {
				t.Fatal("a failed login staged a credential")
			}
			if scenario.state == domain.ClaudeLimitLoginRecovery {
				if !usage.CleanupPending {
					t.Fatal("uncertain cleanup is not a durable obligation")
				}
			} else if present, _ := f.native.store.Present(context.Background()); present && scenario.name != "pre-existing native storage" {
				t.Fatal("native credentials stayed in the profile")
			}
			if scenario.name == "pre-existing native storage" && f.native.store.removals != 0 {
				t.Fatal("pre-existing storage was deleted instead of refused")
			}
			if _, a := f.record(); a.Subscription.Generation == "" || a.Health != domain.AccountReady {
				t.Fatal("a failed usage login changed execution readiness")
			}
			if usage.Operation.State == domain.ClaudeLimitLoginFailed || usage.Operation.State == domain.ClaudeLimitLoginUnsupported {
				if _, err := f.start(""); err != nil {
					t.Fatalf("a failed login must allow a new explicit start: %v", err)
				}
			}
		})
	}
}

func TestClaudeUsageStartIsFencedByGenerationAndAccountState(t *testing.T) {
	f := newUsageFixture(t)
	if _, err := f.start(string(domain.NewID())); err == nil {
		t.Fatal("a stale observed usage generation started a login")
	}
	if _, err := f.start("not-an-id"); err == nil {
		t.Fatal("an invalid usage generation started a login")
	}
	generation := f.link()
	if _, err := f.start(""); err == nil {
		t.Fatal("a reconnect with a stale (empty) observed generation started")
	}
	if _, err := f.start(generation); err != nil {
		t.Fatalf("an explicit reconnect with the observed generation: %v", err)
	}
	// Other principals and accounts cannot drive the lane.
	r, _ := f.record()
	if _, err := f.client.StartClaudeUsage(context.Background(), subscriptionRequest(f.workerToken, &pb.StartClaudeUsageRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}})); err == nil {
		t.Fatal("a Worker started a usage login")
	}
}

func TestClaudeUsageReconnectReplacesGenerationAndClearsEvidence(t *testing.T) {
	f := newUsageFixture(t)
	first := f.link()
	f.mutateUsage(func(s *domain.ClaudeLimitState) {
		now := time.Now().UTC()
		s.RecordSuccess(domain.ClaudeLimitObservation{ObservedAt: now, Windows: []domain.ClaudeLimitWindow{{ID: domain.ClaudeLimitFiveHour, Remaining: 0.4}}})
	})
	if _, a := f.record(); len(a.Quota) != 1 {
		t.Fatal("seeded evidence is not projected")
	}
	oldRefs := f.usageRefs()
	f.native.login = newFakeUsageProcess()
	started, err := f.start(first)
	if err != nil {
		t.Fatal(err)
	}
	// While the reconnect is pending the previous evidence and credential stay.
	if usage := f.usage(); usage.Generation != domain.ID(first) || len(usage.Windows) != 1 || usage.Readable() {
		t.Fatalf("reconnect start must keep the old evidence but pause reads: %+v", usage)
	}
	done := f.runLogin()
	close(f.native.login.ready)
	f.awaitUsageState(started.OperationId, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	_ = f.submit(started.OperationId, usageFixtureCode)
	<-f.native.login.codes
	f.native.identity.Email = "other@example.com"
	f.native.store.set(usageNativeRecord(usageFixtureAccess, usageFixtureRefresh, time.Now().Add(time.Hour)))
	close(f.native.login.finish)
	awaitServerFixture(t, done)
	review := f.awaitUsageState(started.OperationId, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_CONFIRMING)
	if review.Identity.Email != "other@example.com" {
		t.Fatal("the new identity was not presented for confirmation")
	}
	if usage := f.usage(); usage.Identity.Email != "alex@example.com" || len(usage.Windows) != 1 {
		t.Fatal("the linked identity changed before confirmation")
	}
	if _, err := f.confirm(started.OperationId); err != nil {
		t.Fatal(err)
	}
	usage := f.usage()
	if usage.Generation != domain.ID(started.OperationId) || usage.Identity.Email != "other@example.com" || len(usage.Windows) != 0 || usage.LastSuccess != nil {
		t.Fatalf("reconnect kept previous evidence: %+v", usage)
	}
	if _, a := f.record(); len(a.Quota) != 0 {
		t.Fatal("previous windows are still projected")
	}
	// The superseded credential is removed once the new link is confirmed.
	refs := f.usageRefs()
	if len(refs) != 1 || refs[0] == oldRefs[0] || len(usage.RetiredCredentials) != 0 {
		t.Fatalf("superseded credential not retired: %+v retired %v", refs, usage.RetiredCredentials)
	}
}

func TestClaudeUsageMutationsRejectWrongOperationGenerationAndActor(t *testing.T) {
	f := newUsageFixture(t)
	started, _ := f.start("")
	if _, err := f.progress(started.OperationId, string(domain.NewID())); err == nil {
		t.Fatal("progress accepted another generation")
	}
	if _, err := f.progress(string(domain.NewID()), started.OperationId); err == nil {
		t.Fatal("progress accepted another operation")
	}
	r, _ := f.record()
	for name, call := range map[string]func() error{
		"submit before waiting": func() error { return f.submit(started.OperationId, usageFixtureCode) },
		"confirm before review": func() error { _, err := f.confirm(started.OperationId); return err },
		"foreign generation": func() error {
			_, err := f.client.CancelClaudeUsage(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.CancelClaudeUsageRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: started.OperationId, UsageGeneration: string(domain.NewID())}))
			return err
		},
	} {
		if call() == nil {
			t.Fatalf("%s was admitted", name)
		}
	}
	if bytes.Contains(mustJSON(t, f.usage()), []byte(usageFixtureCode)) {
		t.Fatal("the approval code entered the stored state")
	}
}
