// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
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

// claudeStart admits one machine-bound Claude lifecycle operation. The public
// RPC retired fresh machine-bound LOGIN and REFRESH, so those two actions seed
// the state an earlier release accepted by running the retained admission under
// the same receipt; LOGOUT still goes through the public RPC.
func claudeStart(t *testing.T, f *subscriptionFixture, action pb.SubscriptionAction) *pb.RequestSubscriptionResponse {
	t.Helper()
	r, _ := f.record()
	request := &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: action}
	if action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT {
		response, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
		if err != nil {
			t.Fatal(err)
		}
		return response.Msg
	}
	return claudeSeedLegacyRequest(t, f, request)
}

// claudeSeedLegacyRequest replays a pre-retirement LOGIN or REFRESH admission.
func claudeSeedLegacyRequest(t *testing.T, f *subscriptionFixture, request *pb.RequestSubscriptionRequest) *pb.RequestSubscriptionResponse {
	t.Helper()
	response, err := claudeTryLegacyRequest(f, request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

// claudeTryLegacyRequest is claudeSeedLegacyRequest that reports a denial.
func claudeTryLegacyRequest(f *subscriptionFixture, request *pb.RequestSubscriptionRequest) (*pb.RequestSubscriptionResponse, error) {
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	unlock, err := f.service.lockAccounts(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	m := request.Mutation
	input := claudeLifecycleInput{domain.ID(m.Id), domain.ID(request.MachineId), m.ExpectedRevision, subscriptionAction(request.Action), domain.Principal{Type: domain.OwnerDevice}}
	requestID := domain.ID(m.RequestId)
	result, err := f.service.Store.Mutate(ctx, requestID, claudeLifecycleOperation, input, func(tx *store.Tx) (any, error) {
		return f.service.admitClaudeLifecycle(tx, input, requestID)
	})
	if err != nil {
		return nil, err
	}
	record, err := f.service.accountRecord(ctx, input.Account)
	if err != nil {
		return nil, err
	}
	return &pb.RequestSubscriptionResponse{Account: rpc.Resource(record), OperationId: m.RequestId, Replayed: result.Replayed}, nil
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
	connection, generation, profile := a.Connection.ID, a.Subscription.Generation, a.Subscription.NativeProfileID
	reauthentication := claudeStart(t, f, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	refreshed, err := f.take(reauthentication, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal(err)
	}
	finish.Mutation = &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: refreshed.LeaseRevision}
	finish.LeaseId, finish.GenerationId = refreshed.LeaseId, refreshed.GenerationId
	if _, err = f.client.FinishSubscription(ctx, subscriptionRequest(f.workerToken, finish)); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	if a.Connection == nil || a.Connection.ID != connection || a.Subscription.Generation == generation || a.Subscription.NativeProfileID != profile || a.Subscription.OwnerMachineID != f.input.MachineID || a.Subscription.Lease != nil {
		t.Fatal("reauthentication replaced the original profile or connection")
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
	if _, err = claudeTryLegacyRequest(f, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}); err == nil {
		t.Fatal("old Codex-only Worker admitted Claude login")
	}
	_, a := f.record()
	if a.Subscription != nil && (a.Subscription.NativeProfileID != "" || a.Subscription.Pending != nil) {
		t.Fatal("unsupported request created native ownership")
	}
}

func TestClaudeSubscriptionFirstInputRequiresOriginalRunnerBeforeClaim(t *testing.T) {
	for _, scenario := range []string{"original", "wrong-runner", "old-worker"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFirstDispatchFixtureForHarness(t, domain.ClaudeCode, domain.ExecuteMode)
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.native-subscription-execution", nil, func(tx *store.Tx) (any, error) {
				r, a, err := accountFromTx(tx, domain.ID(f.account.Id), 0)
				if err != nil {
					return nil, err
				}
				owner := domain.ID(f.machine.Id)
				if scenario == "wrong-runner" {
					owner = domain.NewID()
				}
				a.Type = domain.SubscriptionAccount
				a.ProviderID = ""
				a.SubscriptionService = domain.SubscriptionClaude
				a.Validation = nil
				a.Catalog = nil
				a.Connection.Authentication = domain.SubscriptionAuth
				a.Subscription = &domain.SubscriptionState{Generation: domain.NewID(), NativeProfileID: domain.NewID(), OwnerMachineID: owner, IdentityCommitment: strings.Repeat("ab", 32)}
				if _, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
					return nil, err
				}
				if _, err = fixtureInlineSource(tx, f.selection.AgentID, "", domain.SubscriptionClaude); err != nil {
					return nil, err
				}
				r, m, err := activeMachine(tx, domain.ID(f.machine.Id))
				if err != nil {
					return nil, err
				}
				if scenario != "old-worker" {
					m.WorkerCapabilities = append(m.WorkerCapabilities, domain.NativeClaudeSubscriptionsV1)
				}
				_, err = tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
				return nil, err
			})
			if err != nil {
				t.Fatal(err)
			}
			err = f.service.dispatchExecution(context.Background(), f.refresh(t))
			session, decode := store.Decode[domain.Session](f.refresh(t))
			if decode != nil {
				t.Fatal(decode)
			}
			queued := inputBody(t, currentCatalogResource(t, f.accountFixture, f.change.Input))
			if scenario == "original" {
				if err != nil || session.InitialExecution == nil || !session.InitialExecution.Configuration.Subscription || session.InitialExecution.Configuration.SubscriptionService != domain.SubscriptionClaude || queued.Delivery != domain.InputClaimed || queued.ExecutionID != session.ActiveExecutionID {
					t.Fatal("original native subscription selection did not dispatch", err)
				}
			} else if err == nil || session.InitialExecution != nil || session.ActiveExecutionID != "" || queued.Delivery != domain.InputQueued || queued.ExecutionID != "" {
				t.Fatal("rejected Runner consumed original input", err)
			}
		})
	}
}

// TestClaudeSubscriptionInstallationUsesBaselineWithGenericErrors proves that
// an installation at or above the Claude baseline passes the version gate, and
// that every unsupported-installation error stays free of fixed version numbers
// (a newer admitted installation may fail a later check).
func TestClaudeSubscriptionInstallationUsesBaselineWithGenericErrors(t *testing.T) {
	f := newClaudeSubscriptionFixture(t)
	for _, test := range []struct {
		name    string
		version string
		mutate  func(*domain.Installation)
		wantErr bool
	}{
		{"baseline", domain.ClaudeProtocolVersion, nil, false},
		{"newer patch", "2.1.237", nil, false},
		{"newer prerelease major", "3.0.0-rc.1", nil, false},
		{"newer without resolved path", "2.1.237", func(i *domain.Installation) { i.ResolvedPath = "" }, true},
		{"newer without verified protocol", "2.1.237", func(i *domain.Installation) { i.ProtocolVerified = false }, true},
		{"newer with failed protocol state", "2.1.237", func(i *domain.Installation) { i.Protocol.State = domain.ProtocolFailed }, true},
		{"lower", "2.1.235", nil, true},
		{"baseline prerelease", domain.ClaudeProtocolVersion + "-rc.1", nil, true},
		{"malformed", "not-a-version", nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claude-version", nil, func(tx *store.Tx) (any, error) {
				r, m, err := activeMachine(tx, f.input.MachineID)
				if err != nil {
					return nil, err
				}
				i := m.Installations[0]
				i.Version = test.version
				i.ResolvedPath = "/controlled/native/claude"
				i.ProtocolVerified = true
				i.Protocol.State = domain.ProtocolVerified
				if test.mutate != nil {
					test.mutate(&i)
				}
				m.Installations = []domain.Installation{i}
				if _, err = tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m); err != nil {
					return nil, err
				}
				_, got := claudeSubscriptionInstallation(tx, f.input.MachineID)
				if (got != nil) != test.wantErr {
					t.Errorf("claudeSubscriptionInstallation(%q) error = %v, wantErr %v", test.version, got, test.wantErr)
				}
				if got != nil {
					message := domain.SafeError(got).Message + " " + domain.SafeError(got).Guidance
					for _, fixed := range []string{domain.ClaudeProtocolVersion, "2.1.237", test.version} {
						if strings.Contains(message, fixed) {
							t.Errorf("error %q names fixed version %q", message, fixed)
						}
					}
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func claudeRunnerRequest(f *subscriptionFixture, r store.Record, action pb.SubscriptionAction, requestID domain.ID) (*pb.RequestSubscriptionResponse, error) {
	response, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(requestID), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: action}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func TestClaudeRunnerLoginAndRefreshAreRejectedWithoutStateChange(t *testing.T) {
	for _, action := range []pb.SubscriptionAction{pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH} {
		t.Run(action.String(), func(t *testing.T) {
			f := newClaudeSubscriptionFixture(t)
			beforeRecord, before := f.record()
			response, err := claudeRunnerRequest(f, beforeRecord, action, domain.NewID())
			problem := domain.SafeError(rpc.ClientError(err))
			if response != nil || problem.Code != domain.Unsupported || problem.Message == "" || problem.Guidance == "" {
				t.Fatalf("fresh Runner %s was not rejected as unsupported with guidance: %+v", action, problem)
			}
			afterRecord, after := f.record()
			if afterRecord.Revision != beforeRecord.Revision || !reflect.DeepEqual(before, after) {
				t.Fatal("a rejected Runner request changed account state")
			}
		})
	}
}

func TestClaudeCLIShapedRunnerLoginIsRejected(t *testing.T) {
	// A CLI-shaped login that names an existing machine and omits the device
	// code flag is rejected like any other fresh Runner login.
	f := newClaudeSubscriptionFixture(t)
	r, before := f.record()
	_, err := claudeRunnerRequest(f, r, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, domain.NewID())
	if domain.SafeError(rpc.ClientError(err)).Code != domain.Unsupported {
		t.Fatalf("CLI-shaped Runner login was not rejected as unsupported: %v", err)
	}
	if _, after := f.record(); after.Subscription != nil && (after.Subscription.Pending != nil || after.Subscription.NativeOperation != nil || after.Subscription.NativeProfileID != "") || !reflect.DeepEqual(before.Subscription, after.Subscription) && before.Subscription != nil {
		t.Fatal("a rejected Runner login created native ownership")
	}
}

func TestClaudeAcceptedRunnerRequestReplaysThroughOriginalReceipt(t *testing.T) {
	f := newClaudeSubscriptionFixture(t)
	r, _ := f.record()
	requestID := domain.NewID()
	accepted := claudeSeedLegacyRequest(t, f, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(requestID), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN})
	beforeRecord, before := f.record()
	// The same request replays through its receipt instead of being rejected.
	replay, err := claudeRunnerRequest(f, r, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, requestID)
	if err != nil {
		t.Fatalf("original accepted request did not replay: %v", err)
	}
	if !replay.Replayed || replay.OperationId != accepted.OperationId {
		t.Fatalf("replay did not return the original operation: %+v", replay)
	}
	afterRecord, after := f.record()
	if afterRecord.Revision != beforeRecord.Revision || !reflect.DeepEqual(before, after) || after.Subscription.Pending == nil || after.Subscription.Pending.ID != domain.ID(accepted.OperationId) {
		t.Fatal("replay admitted a new operation or changed the original one")
	}
	// A different request ID is a fresh request and is rejected.
	current, _ := f.record()
	if _, err = claudeRunnerRequest(f, current, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, domain.NewID()); domain.SafeError(rpc.ClientError(err)).Code != domain.Unsupported {
		t.Fatalf("a new Runner login request was not rejected: %v", err)
	}
}

// A lost device-owned lease settles through exactly one of two signals. Cleanup
// alone confirms a logout and disconnects the account. Cleanup together with
// success and no identity is the no-native settlement of an EXECUTE that never
// opened the profile; it keeps the account connected and is refused for every
// other action.
func TestClaudeRecoveryNoNativeSettlementKeepsOnlyAnExecuteAccount(t *testing.T) {
	for _, tc := range []struct {
		name      string
		action    domain.SubscriptionAction
		success   bool
		wantKept  bool
		wantError bool
	}{
		{"execute no-native settlement keeps the account", domain.SubscriptionExecute, true, true, false},
		{"execute cleanup-only confirmation is a logout", domain.SubscriptionExecute, false, false, false},
		{"login cannot claim no-native settlement", domain.SubscriptionLogin, true, false, true},
		{"refresh cannot claim no-native settlement", domain.SubscriptionRefresh, true, false, true},
		{"logout cannot claim no-native settlement", domain.SubscriptionLogout, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newClaudeSubscriptionFixture(t)
			ctx := context.Background()
			login := claudeStart(t, f, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			lease, err := f.take(login, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			if err != nil {
				t.Fatal(err)
			}
			r, _ := f.record()
			connect := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), CleanupConfirmed: true, Succeeded: true, NativeIdentity: &pb.NativeSubscriptionIdentity{ProfileId: lease.NativeProfileId, IdentityCommitment: strings.Repeat("ab", 32)}}
			if _, err = f.client.FinishSubscription(ctx, subscriptionRequest(f.workerToken, connect)); err != nil {
				t.Fatal(err)
			}
			_, connected := f.record()
			refresh := claudeStart(t, f, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
			held, err := f.take(refresh, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.claude-lease-action", nil, func(tx *store.Tx) (any, error) {
				r, a, err := accountFromTx(tx, f.input.AccountID, 0)
				if err != nil {
					return nil, err
				}
				a.Subscription.Lease.Action = tc.action
				_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
				return nil, err
			}); err != nil {
				t.Fatal(err)
			}
			if err = f.service.markSubscriptionRecovery(f.input.AccountID); err != nil {
				t.Fatal(err)
			}
			_, err = f.finish(held, nil, tc.success, false, true)
			if (err != nil) != tc.wantError {
				t.Fatalf("finish error = %v, want error %t", err, tc.wantError)
			}
			_, a := f.record()
			if tc.wantError {
				if !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil {
					t.Fatal("a refused settlement released the lost lease")
				}
				return
			}
			if a.Subscription.RecoveryRequired || a.Subscription.Lease != nil {
				t.Fatal("recovery did not retire the lost lease")
			}
			kept := a.Connection != nil && a.Health == domain.AccountReady && a.Subscription.Generation == connected.Subscription.Generation && a.Subscription.NativeProfileID == connected.Subscription.NativeProfileID && a.Subscription.IdentityCommitment == connected.Subscription.IdentityCommitment && a.Subscription.OwnerMachineID == connected.Subscription.OwnerMachineID
			if kept != tc.wantKept {
				t.Fatalf("account kept = %t, want %t", kept, tc.wantKept)
			}
			if !tc.wantKept && (a.Connection != nil || a.Subscription.NativeProfileID != "") {
				t.Fatal("cleanup-only confirmation did not disconnect the account")
			}
		})
	}
}
