// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"net/url"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestGrokUnacceptedProfileCannotSendOAuthAndStillPermitsCleanup(t *testing.T) {
	f := newGrokOAuthFixture(t)
	f.service.grokSubscriptionAccepted = false
	for _, device := range []bool{false, true} {
		r, _ := f.record()
		_, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, DeviceCode: device}))
		if domain.SafeError(rpc.ClientError(err)).Code != domain.Unsupported {
			t.Fatal("unaccepted browser/device profile obtained authentication authority")
		}
		_, a := f.record()
		if a.Subscription != nil || f.exchanges.Load() != 0 {
			t.Fatal("unaccepted profile allocated an operation or sent OAuth")
		}
	}
	f.service.grokSubscriptionAccepted = true
	f.browserLogin()
	f.service.grokSubscriptionAccepted = false
	r, before := f.record()
	_, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH}))
	if domain.SafeError(rpc.ClientError(err)).Code != domain.Unsupported {
		t.Fatal("unaccepted profile acquired refresh authority")
	}
	_, after := f.record()
	if after.Subscription.Generation != before.Subscription.Generation || after.Subscription.Pending != nil {
		t.Fatal("unaccepted refresh changed original ownership")
	}
	logout := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	awaitServerFixture(t, f.run())
	_, after = f.record()
	refs, err := f.secrets.UnremovedReferences(context.Background(), r.ID)
	if err != nil || len(refs) != 0 || after.Connection != nil || after.Subscription.Generation != "" || f.progressFor(logout.OperationId).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || f.exchanges.Load() != 1 {
		t.Fatal("closed admission blocked cleanup or sent another OAuth request")
	}
}

func (f *grokOAuthFixture) browserLogin() {
	f.t.Helper()
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	done := f.run()
	u, err := url.Parse(f.waiting(op.OperationId).Url)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.callback(op.OperationId, u.Query().Get("state")); err != nil {
		f.t.Fatal(err)
	}
	awaitServerFixture(f.t, done)
	if f.progressFor(op.OperationId).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
		f.t.Fatal("original browser login did not succeed")
	}
}

func TestGrokUncertainRefreshKeepsOriginalGenerationAndNeverResends(t *testing.T) {
	f := newGrokOAuthFixture(t)
	f.browserLogin()
	r, before := f.record()
	ref := credentials.Ref{Owner: r.ID, ID: before.Subscription.Generation, Purpose: credentials.AccountLogin}
	original, err := f.secrets.Get(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(original)
	f.uncertain = true
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	awaitServerFixture(t, f.run())
	_, after := f.record()
	p := f.progressFor(op.OperationId)
	if !after.Subscription.RecoveryRequired || after.Subscription.Pending == nil || after.Subscription.Generation != before.Subscription.Generation || after.Subscription.IdentityCommitment != before.Subscription.IdentityCommitment || after.Subscription.ServerOperation.GrokOAuth.Phase != domain.GrokOAuthRefreshSending || !after.Subscription.ServerOperation.NativeStarted || p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED || p.Url != "" || p.UserCode != "" {
		t.Fatal("unknown refresh released or replaced the original credential owner")
	}
	retained, err := f.secrets.Get(context.Background(), ref)
	if err != nil || !bytes.Equal(retained, original) {
		t.Fatal("unknown refresh discarded the original protected generation")
	}
	clear(retained)
	awaitServerFixture(t, f.run())
	if f.exchanges.Load() != 2 {
		t.Fatal("recovery resent an unknown refresh")
	}
}

func TestGrokLogoutCleanupFailureRetainsProtectedOwnership(t *testing.T) {
	f := newGrokOAuthFixture(t)
	f.browserLogin()
	_, before := f.record()
	f.secrets.deleteError = domain.Fail(domain.Unavailable, "Fixture protected deletion unavailable.", "")
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	awaitServerFixture(t, f.run())
	_, after := f.record()
	p := f.progressFor(op.OperationId)
	refs, err := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID)
	if err != nil || len(refs) == 0 || !after.Subscription.RecoveryRequired || after.Subscription.Generation != before.Subscription.Generation || after.Subscription.IdentityCommitment != before.Subscription.IdentityCommitment || after.Subscription.Pending == nil || !after.Subscription.ServerOperation.NativeStarted || p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED || after.Health == domain.AccountReady {
		t.Fatal("failed logout claimed protected cleanup or permitted new execution")
	}
	if f.exchanges.Load() != 1 {
		t.Fatal("logout sent an OAuth exchange")
	}
}

func TestGrokAcceptedLoginRestartDoesNotAcquireExchangeAuthority(t *testing.T) {
	f := newGrokOAuthFixture(t)
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	unlock, err := f.service.lockAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.service.subscriptionEpoch = domain.NewID()
	unlock()
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.initializeServerSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, f.run())
	_, a := f.record()
	if f.exchanges.Load() != 0 || !a.Subscription.RecoveryRequired || a.Subscription.Pending == nil || a.Subscription.Pending.ID != domain.ID(op.OperationId) || f.progressFor(op.OperationId).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED {
		t.Fatal("restart adopted accepted login authority")
	}
}

func TestGrokDuplicateIdentityCannotAcquireAnotherOwner(t *testing.T) {
	f := newGrokOAuthFixture(t)
	f.browserLogin()
	_, original := f.record()
	other := domain.NewID()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok-second-account", nil, func(tx *store.Tx) (any, error) {
		a := original
		a.Alias = "Second Grok fixture"
		a.Connection, a.Subscription = nil, nil
		a.Health = domain.AccountDisconnected
		_, err := tx.Put(domain.AccountKind, other, 0, "", "", a)
		return struct{}{}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	f.input.AccountID = other
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	done := f.run()
	u, _ := url.Parse(f.waiting(op.OperationId).Url)
	if err := f.callback(op.OperationId, u.Query().Get("state")); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, done)
	_, duplicate := f.record()
	if duplicate.Connection != nil || duplicate.Subscription.Generation != "" || duplicate.Subscription.IdentityCommitment != "" || f.progressFor(op.OperationId).State == pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
		t.Fatal("duplicate identity acquired a second managed connection")
	}
}
