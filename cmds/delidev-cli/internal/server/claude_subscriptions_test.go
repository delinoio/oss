// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func newClaudeSubscriptionFixture(t *testing.T) *subscriptionFixture {
	f := newSubscriptionFixture(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claude", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.SubscriptionService = domain.SubscriptionClaude
		if _, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		r, m, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		m.WorkerCapabilities = []domain.WorkerCapability{domain.NativeClaudeSubscriptionsV1}
		i := m.Installations[0]
		i.Harness = domain.ClaudeCode
		i.Version = domain.ClaudeProtocolVersion
		i.ResolvedPath = "/controlled/native/claude"
		i.Protocol.Protocol = domain.ProtocolFor(domain.ClaudeCode)
		m.Installations = []domain.Installation{i}
		_, err = tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func claudeStart(t *testing.T, f *subscriptionFixture, action pb.SubscriptionAction) *pb.RequestSubscriptionResponse {
	t.Helper()
	r, _ := f.record()
	response, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: action}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg
}
func claudeFixtureURL() string {
	q := url.Values{"client_id": {"9d1c250a-e61b-44d9-88ed-5944d1962f5e"}, "response_type": {"code"}, "redirect_uri": {"https://platform.claude.com/oauth/code/callback"}, "scope": {"user:profile user:inference user:sessions:claude_code"}, "code_challenge": {strings.Repeat("a", 43)}, "code_challenge_method": {"S256"}, "state": {strings.Repeat("b", 32)}, "code": {"true"}}
	return "https://claude.com/cai/oauth/authorize?" + q.Encode()
}
func TestClaudeCodeApprovalOnceAndMetadataOnly(t *testing.T) {
	f := newClaudeSubscriptionFixture(t)
	ctx := context.Background()
	op := claudeStart(t, f, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	if len(lease.Bundle) != 0 || domain.ID(lease.NativeProfileId).Validate() != nil {
		t.Fatal("native lease exposed credentials or omitted profile")
	}
	_, err = f.client.PublishSubscriptionProgress(ctx, subscriptionRequest(f.workerToken, &pb.PublishSubscriptionProgressRequest{AccountId: string(f.input.AccountID), LeaseId: lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), State: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING, LoginMethod: pb.SubscriptionLoginMethod_SUBSCRIPTION_LOGIN_METHOD_BROWSER_CODE, Url: claudeFixtureURL()}))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := f.record()
	code := "approval-fixture-sentinel#original-state"
	submit := &pb.SubmitSubscriptionLoginCodeRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: op.OperationId, Code: []byte(code)}
	if _, err = f.client.SubmitSubscriptionLoginCode(ctx, subscriptionRequest(f.service.Identity.Token, submit)); err != nil {
		t.Fatal(err)
	}
	take := &pb.TakeSubscriptionLoginCodeRequest{AccountId: string(r.ID), LeaseId: lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: op.OperationId}
	response, err := f.client.TakeSubscriptionLoginCode(ctx, subscriptionRequest(f.workerToken, take))
	if err != nil || string(response.Msg.Code) != code {
		t.Fatal("original one-time delivery failed", err)
	}
	clear(response.Msg.Code)
	if _, err = f.client.TakeSubscriptionLoginCode(ctx, subscriptionRequest(f.workerToken, take)); err == nil {
		t.Fatal("code delivered twice")
	}
	submit.Code = []byte(code)
	if _, err = f.client.SubmitSubscriptionLoginCode(ctx, subscriptionRequest(f.service.Identity.Token, submit)); err != nil {
		t.Fatal("original receipt replay changed", err)
	}
	if len(f.service.claudeLoginCodes[domain.ID(op.OperationId)]) != 0 {
		t.Fatal("replayed submit replenished code")
	}
	for _, name := range []string{"state.sqlite", "state.sqlite-wal", "state.sqlite-shm"} {
		raw, err := os.ReadFile(filepath.Join(f.service.Store.Root(), name))
		if err == nil && (bytes.Contains(raw, []byte(code)) || bytes.Contains(raw, []byte(claudeFixtureURL()))) {
			t.Fatal("sensitive login presentation persisted")
		}
	}
	finish := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), CleanupConfirmed: true, Succeeded: true, NativeIdentity: &pb.NativeSubscriptionIdentity{ProfileId: lease.NativeProfileId, IdentityCommitment: strings.Repeat("ab", 32)}}
	if _, err = f.client.FinishSubscription(ctx, subscriptionRequest(f.workerToken, finish)); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if a.Connection == nil || a.Health != domain.AccountReady || a.Subscription.OwnerMachineID != f.input.MachineID || a.Subscription.Lease != nil {
		t.Fatal("native ownership was not committed")
	}
	logout := claudeStart(t, f, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	ending, err := f.take(logout, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.finish(ending, nil, true, false, true); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	if a.Connection != nil || a.Subscription.NativeProfileID != "" || a.Subscription.OwnerMachineID != "" || a.Subscription.Generation != "" {
		t.Fatal("confirmed logout retained native authority")
	}
}
func TestClaudeLostLeaseRequiresOriginalCleanup(t *testing.T) {
	f := newClaudeSubscriptionFixture(t)
	op := claudeStart(t, f, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.markSubscriptionRecovery(f.input.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.finish(lease, nil, true, false, true); err == nil {
		t.Fatal("recovery accepted authentication success")
	}
	if _, err = f.finish(lease, nil, false, false, true); err != nil {
		t.Fatal("positive original cleanup rejected", err)
	}
	_, a := f.record()
	if a.Subscription.RecoveryRequired || a.Subscription.Lease != nil || a.Subscription.Pending != nil || a.Subscription.NativeProfileID != "" {
		t.Fatal("cleanup failed to retire original ownership")
	}
}

func TestClaudeCancellationCannotPublishAuthentication(t *testing.T) {
	f := newClaudeSubscriptionFixture(t)
	ctx := context.Background()
	op := claudeStart(t, f, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := f.record()
	_, err = f.client.CancelSubscription(ctx, subscriptionRequest(f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}}))
	if err != nil {
		t.Fatal(err)
	}
	finish := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), CleanupConfirmed: true, Succeeded: true, NativeIdentity: &pb.NativeSubscriptionIdentity{ProfileId: lease.NativeProfileId, IdentityCommitment: strings.Repeat("ab", 32)}}
	if _, err = f.client.FinishSubscription(ctx, subscriptionRequest(f.workerToken, finish)); err == nil {
		t.Fatal("canceled operation published native authentication")
	}
	_, a := f.record()
	if a.Connection != nil || a.Subscription.Lease == nil {
		t.Fatal("rejected success released native ownership")
	}
	if _, err = f.finish(lease, nil, false, false, true); err != nil {
		t.Fatal(err)
	}
	progress, err := f.client.GetSubscriptionProgress(ctx, subscriptionRequest(f.service.Identity.Token, &pb.GetSubscriptionProgressRequest{AccountId: string(r.ID), OperationId: op.OperationId}))
	if err != nil || progress.Msg.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_CANCELED {
		t.Fatal("original cleanup did not settle cancellation", err)
	}
}

func TestClaudeLostCodeNeverReplenishesOriginalSubmission(t *testing.T) {
	f := newClaudeSubscriptionFixture(t)
	ctx := context.Background()
	op := claudeStart(t, f, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	publish := &pb.PublishSubscriptionProgressRequest{AccountId: string(f.input.AccountID), LeaseId: lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), State: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING, LoginMethod: pb.SubscriptionLoginMethod_SUBSCRIPTION_LOGIN_METHOD_BROWSER_CODE, Url: claudeFixtureURL()}
	if _, err = f.client.PublishSubscriptionProgress(ctx, subscriptionRequest(f.workerToken, publish)); err != nil {
		t.Fatal(err)
	}
	r, _ := f.record()
	for range 3 {
		if _, err = f.client.PublishSubscriptionProgress(ctx, subscriptionRequest(f.workerToken, publish)); err != nil {
			t.Fatal(err)
		}
		if _, err = f.client.TakeSubscriptionLoginCode(ctx, subscriptionRequest(f.workerToken, &pb.TakeSubscriptionLoginCodeRequest{AccountId: string(r.ID), OperationId: op.OperationId, LeaseId: lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance)})); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := f.record()
	if after.Revision != r.Revision {
		t.Fatal("idle native polling mutated account")
	}
	claim := &pb.SubmitSubscriptionLoginCodeRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: op.OperationId, Code: []byte("fictional-original#state")}
	if _, err = f.client.SubmitSubscriptionLoginCode(ctx, subscriptionRequest(f.service.Identity.Token, claim)); err != nil {
		t.Fatal(err)
	}
	f.service.clearClaudeLoginInput(domain.ID(op.OperationId))
	claim.Code = []byte("fictional-original#state")
	if _, err = f.client.SubmitSubscriptionLoginCode(ctx, subscriptionRequest(f.service.Identity.Token, claim)); err != nil {
		t.Fatal("original metadata receipt was lost", err)
	}
	if _, err = f.client.TakeSubscriptionLoginCode(ctx, subscriptionRequest(f.workerToken, &pb.TakeSubscriptionLoginCodeRequest{AccountId: string(r.ID), OperationId: op.OperationId, LeaseId: lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance)})); err == nil {
		t.Fatal("lost original code was replenished")
	}
	r, _ = f.record()
	claim.Mutation = &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}
	claim.Code = []byte("replacement#state")
	if _, err = f.client.SubmitSubscriptionLoginCode(ctx, subscriptionRequest(f.service.Identity.Token, claim)); err == nil {
		t.Fatal("a second approval input was admitted")
	}
}

func TestClaudeUnsupportedWorkerCannotCreateNativeOwnership(t *testing.T) {
	f := newClaudeSubscriptionFixture(t)
	ctx := context.Background()
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.old-worker", nil, func(tx *store.Tx) (any, error) {
		r, m, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		m.WorkerCapabilities = []domain.WorkerCapability{domain.ManagedCodexSubscriptionsV1}
		_, err = tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := f.record()
	if _, err = f.client.RequestSubscription(ctx, subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN})); err == nil {
		t.Fatal("old Codex-only Worker admitted Claude login")
	}
	_, a := f.record()
	if a.Subscription != nil && (a.Subscription.NativeProfileID != "" || a.Subscription.Pending != nil) {
		t.Fatal("unsupported request created native ownership")
	}
}
