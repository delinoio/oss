// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
)

func batchFailedAccount(t *testing.T, cleared bool) *subscriptionFixture {
	t.Helper()
	f, operation := legacyFailedServerLogin(t, false)
	excludeUnrelatedBatchFixture(t, f)
	if cleared {
		if err := f.service.recoverFailedServerLogin(failedLoginContext(), f.input.AccountID, operation); err != nil {
			t.Fatal(err)
		}
	}
	return f
}
func acceptFailedCleanup(t *testing.T, f *subscriptionFixture, ctx context.Context, request domain.ID) *pb.CleanupFailedSubscriptionsResponse {
	t.Helper()
	r, err := f.service.CleanupFailedSubscriptions(ctx, connect.NewRequest(&pb.CleanupFailedSubscriptionsRequest{RequestId: string(request)}))
	if err != nil {
		t.Fatal(err)
	}
	return r.Msg
}
func runFailedCleanup(t *testing.T, f *subscriptionFixture, id string) {
	t.Helper()
	r, err := f.service.Store.Get(failedLoginContext(), domain.JobKind, domain.ID(id))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.runFailedSubscriptionCleanup(failedLoginContext(), r); err != nil {
		t.Fatal(err)
	}
}
func readFailedCleanup(t *testing.T, f *subscriptionFixture, id, page string) *pb.GetFailedSubscriptionCleanupResponse {
	t.Helper()
	r, err := f.service.GetFailedSubscriptionCleanup(failedLoginContext(), connect.NewRequest(&pb.GetFailedSubscriptionCleanupRequest{JobId: id, PageToken: page}))
	if err != nil {
		t.Fatal(err)
	}
	return r.Msg
}

func TestFailedSubscriptionBatchCleanup(t *testing.T) {
	for _, cleared := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending-cleanup", true: "already-cleaned"}[cleared], func(t *testing.T) {
			f := batchFailedAccount(t, cleared)
			request := domain.NewID()
			accepted := acceptFailedCleanup(t, f, failedLoginContext(), request)
			if accepted.Job.Total != 1 {
				t.Fatal(accepted)
			}
			replay := acceptFailedCleanup(t, f, failedLoginContext(), request)
			if !replay.Replayed || replay.Job.Id != accepted.Job.Id {
				t.Fatal("admission replay allocated work")
			}
			_, err := f.service.CleanupFailedSubscriptions(failedLoginContext(), connect.NewRequest(&pb.CleanupFailedSubscriptionsRequest{RequestId: string(domain.NewID())}))
			if connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("concurrent batch admitted", err)
			}
			runFailedCleanup(t, f, accepted.Job.Id)
			result := readFailedCleanup(t, f, accepted.Job.Id, "")
			if result.Job.Deleted != 1 || result.Job.Retained != 0 || result.Job.State != pb.FailedSubscriptionCleanupState_FAILED_SUBSCRIPTION_CLEANUP_STATE_COMPLETED || len(result.Results) != 1 {
				t.Fatal(result)
			}
			if _, err := f.service.Store.Get(failedLoginContext(), domain.AccountKind, f.input.AccountID); domain.SafeError(err).Code != domain.NotFound {
				t.Fatal("account remains", err)
			}
			// Both lost admission and lost deletion responses observe original receipts.
			replayed := acceptFailedCleanup(t, f, failedLoginContext(), request)
			if !replayed.Replayed || !proto.Equal(replayed.Job, result.Job) {
				t.Fatal("receipt lost completed result")
			}
			runFailedCleanup(t, f, accepted.Job.Id)
			if !proto.Equal(readFailedCleanup(t, f, accepted.Job.Id, ""), result) {
				t.Fatal("completed work was repeated")
			}
		})
	}
}

func TestFailedSubscriptionCleanupEmptyAndExcludedOwnership(t *testing.T) {
	f := unreferencedInitialSubscription(t)
	excludeUnrelatedBatchFixture(t, f)
	zero := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID())
	if zero.Job.Total != 1 {
		t.Fatal("disconnected account excluded", zero)
	}
	runFailedCleanup(t, f, zero.Job.Id)
	if result := readFailedCleanup(t, f, zero.Job.Id, ""); result.Job.Deleted != 1 {
		t.Fatal(result)
	}
	f = unreferencedInitialSubscription(t)
	excludeUnrelatedBatchFixture(t, f)
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if a := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID()); a.Job.Total != 0 {
		t.Fatal("active login included")
	}
	_, original := f.record()
	original.Subscription.ServerOperation.State = domain.SubscriptionFailed
	original.Subscription.Pending = nil
	original.Health = domain.AccountFailed
	if !failedCleanupCandidate(original) {
		t.Fatal("failed initial login excluded")
	}
	for _, mutate := range []func(*domain.Account){
		func(a *domain.Account) { a.Subscription.ServerOperation.State = domain.SubscriptionSucceeded },
		func(a *domain.Account) { a.Subscription.ServerOperation.Action = domain.SubscriptionLogout },
		func(a *domain.Account) {
			a.Subscription.Generation = domain.NewID()
			a.Subscription.RecoveryRequired = true
		},
		func(a *domain.Account) { a.Subscription.OwnerMachineID = domain.NewID() },
		func(a *domain.Account) { a.Subscription.ServerOperation = nil },
		func(a *domain.Account) { a.Subscription.Lease = &domain.SubscriptionLease{} },
		func(a *domain.Account) { a.Connection = &domain.AccountConnection{} },
	} {
		raw, _ := json.Marshal(original)
		var a domain.Account
		_ = json.Unmarshal(raw, &a)
		mutate(&a)
		if failedCleanupCandidate(a) {
			t.Fatal("independent ownership included", op.OperationId)
		}
	}
}

func TestFailedSubscriptionCleanupRetainsChangedAndReferencedAccounts(t *testing.T) {
	for _, scenario := range []string{"changed", "referenced", "vault", "protected-intent", "native", "authorization"} {
		t.Run(scenario, func(t *testing.T) {
			f := batchFailedAccount(t, scenario == "changed" || scenario == "referenced" || scenario == "protected-intent" || scenario == "authorization")
			ctx := failedLoginContext()
			var client domain.ID
			if scenario == "authorization" {
				client = domain.NewID()
				_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.cleanup.client", nil, func(tx *store.Tx) (any, error) {
					return tx.Put(domain.DeviceKind, client, 0, "", "", domain.Device{Name: "Cleanup client", Type: domain.ClientDevice, PairedAt: time.Now().UTC()})
				})
				if err != nil {
					t.Fatal(err)
				}
				ctx = domain.WithPrincipal(ctx, domain.Principal{Type: domain.ClientDevice, DeviceID: client})
			}
			if scenario == "native" {
				f.changeFailedLogin(func(a *domain.Account) {
					a.Subscription.ServerOperation.NativeStarted = true
					a.Subscription.Pending = operationPlaceholder(a.Subscription.ServerOperation)
					a.Subscription.Pending.Phase = domain.SubscriptionClaimed
				})
			}
			accepted := acceptFailedCleanup(t, f, ctx, domain.NewID())
			want := pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_CLEANUP_UNCONFIRMED
			switch scenario {
			case "changed":
				f.changeFailedLogin(func(a *domain.Account) { a.Alias = "Edited after acceptance" })
				want = pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_CHANGED
			case "referenced":
				_, err := f.service.Store.Mutate(failedLoginContext(), domain.NewID(), "fixture.cleanup.reference", nil, func(tx *store.Tx) (any, error) {
					r, err := tx.Get(domain.AgentKind, f.input.Configuration.AgentID)
					if err != nil {
						return nil, err
					}
					a, err := store.Decode[domain.Agent](r)
					if err != nil {
						return nil, err
					}
					a.Accounts = append(a.Accounts, domain.WeightedAccount{ID: f.input.AccountID, Weight: 1})
					return tx.Put(domain.AgentKind, r.ID, r.Revision, "", "", a)
				})
				if err != nil {
					t.Fatal(err)
				}
				want = pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_REFERENCED
			case "vault":
				f.secrets.referenceError = domain.Fail(domain.Unavailable, "Fixture vault unavailable.", "Inspect cleanup.")
			case "protected-intent":
				_, err := f.secrets.Put(ctx, credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}, []byte("synthetic-protected"))
				if err != nil {
					t.Fatal(err)
				}
			case "native":
			case "authorization":
				_, err := f.service.Store.Mutate(failedLoginContext(), domain.NewID(), "fixture.cleanup.revoke", nil, func(tx *store.Tx) (any, error) {
					r, err := tx.Get(domain.DeviceKind, client)
					if err != nil {
						return nil, err
					}
					d, err := store.Decode[domain.Device](r)
					if err != nil {
						return nil, err
					}
					d.Revoked = true
					now := time.Now().UTC()
					d.RevokedAt = &now
					return tx.Put(domain.DeviceKind, r.ID, r.Revision, "", "", d)
				})
				if err != nil {
					t.Fatal(err)
				}
				want = pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_AUTHORIZATION
			}
			runFailedCleanup(t, f, accepted.Job.Id)
			result := readFailedCleanup(t, f, accepted.Job.Id, "")
			if result.Job.Deleted != 0 || result.Job.Retained != 1 || result.Results[0].Reason != want {
				t.Fatal(result)
			}
			if _, err := f.service.Store.Get(failedLoginContext(), domain.AccountKind, f.input.AccountID); err != nil {
				t.Fatal("retained account deleted", err)
			}
			f.secrets.referenceError = nil
			runFailedCleanup(t, f, accepted.Job.Id)
			if !proto.Equal(result, readFailedCleanup(t, f, accepted.Job.Id, "")) {
				t.Fatal("failed account automatically retried")
			}
		})
	}
}

func TestFailedSubscriptionCleanupCompleteInventoryAndPartialSuccess(t *testing.T) {
	f := batchFailedAccount(t, true)
	_, account := f.record()
	_, err := f.service.Store.Mutate(failedLoginContext(), domain.NewID(), "fixture.cleanup.inventory", nil, func(tx *store.Tx) (any, error) {
		for i := 0; i < 51; i++ {
			id := domain.NewID()
			target := account
			if i%2 == 0 {
				target.Subscription = nil
				target.SubscriptionService = []domain.SubscriptionService{domain.SubscriptionChatGPT, domain.SubscriptionClaude, domain.SubscriptionGrok}[i%3]
			} else {
				original := *account.Subscription
				operation := *original.ServerOperation
				operation.ID = domain.NewID()
				original.ServerOperation = &operation
				target.Subscription = &original
			}
			if _, err := tx.Put(domain.AccountKind, id, 0, "", "", target); err != nil {
				return nil, err
			}
		}
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID())
	if accepted.Job.Total != 52 {
		t.Fatal("inventory truncated", accepted)
	}
	f.changeFailedLogin(func(a *domain.Account) { a.Alias = "Kept after acceptance" })
	runFailedCleanup(t, f, accepted.Job.Id)
	first := readFailedCleanup(t, f, accepted.Job.Id, "")
	second := readFailedCleanup(t, f, accepted.Job.Id, first.NextPageToken)
	if first.Job.Deleted != 51 || first.Job.Retained != 1 || len(first.Results) != 50 || len(second.Results) != 2 || second.NextPageToken != "" {
		t.Fatal(first.Job, len(first.Results), len(second.Results))
	}
	other := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID())
	_, err = f.service.GetFailedSubscriptionCleanup(failedLoginContext(), connect.NewRequest(&pb.GetFailedSubscriptionCleanupRequest{JobId: other.Job.Id, PageToken: first.NextPageToken}))
	if err == nil {
		t.Fatal("cursor escaped original batch")
	}
}

func TestFailedSubscriptionCleanupRestartUsesOnlyOriginalCheckpoint(t *testing.T) {
	for _, scenario := range []struct {
		name                                string
		nativeConfirmed, credentialsStarted bool
	}{
		{"interrupted-unconfirmed", false, false},
		{"native-confirmed", true, false},
		{"interrupted-vault", true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := batchFailedAccount(t, false)
			accepted := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID())
			parent := domain.ID(accepted.Job.Id)
			_, err := f.service.Store.Mutate(failedLoginContext(), domain.NewID(), "fixture.cleanup.checkpoint", nil, func(tx *store.Tx) (any, error) {
				children, err := tx.Jobs("", parent, "", "", 50)
				if err != nil || len(children) != 1 {
					return nil, failedCleanupUnavailable()
				}
				r := children[0]
				j, in, out, err := decodeFailedCleanupAccount(r, parent)
				if err != nil {
					return nil, err
				}
				out.Started, out.CredentialsStarted = true, scenario.credentialsStarted
				if scenario.nativeConfirmed {
					ar, a, err := subscriptionAccount(tx, in.AccountID, out.Revision)
					if err != nil {
						return nil, err
					}
					a.Subscription.ServerOperation.CleanupPhase = domain.SubscriptionNativeCleanupConfirmed
					a.Subscription.ServerOperation.State = domain.SubscriptionFailed
					updated, err := tx.Put(domain.AccountKind, ar.ID, ar.Revision, "", "", a)
					if err != nil {
						return nil, err
					}
					out.Revision = updated.Revision
				}
				j.Output, _ = json.Marshal(out)
				_, err = tx.PutJob(r.ID, r.Revision, "", "", j)
				return struct{}{}, err
			})
			if err != nil {
				t.Fatal(err)
			}
			root := f.service.Store.Root()
			if err := f.service.Store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(failedLoginContext(), root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			f.service.Store = reopened
			f.service.subscriptionEpoch = domain.NewID()
			if err := f.service.initializeServerSubscriptions(failedLoginContext()); err != nil {
				t.Fatal(err)
			}
			runFailedCleanup(t, f, accepted.Job.Id)
			result := readFailedCleanup(t, f, accepted.Job.Id, "")
			if scenario.nativeConfirmed && !scenario.credentialsStarted {
				if result.Job.Deleted != 1 {
					t.Fatal("confirmed original cleanup did not resume", result)
				}
			} else {
				if result.Job.Retained != 1 || result.Results[0].Reason != pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_CLEANUP_UNCONFIRMED {
					t.Fatal("uncertain attempt repeated", result)
				}
				_, a := f.record()
				if err := f.service.recoverFailedServerLogin(failedLoginContext(), f.input.AccountID, a.Subscription.ServerOperation.ID); err != nil {
					t.Fatal(err)
				}
				_, a = f.record()
				if a.Subscription.ServerOperation.CleanupPhase == domain.SubscriptionCredentialCleanupConfirmed {
					t.Fatal("automatic maintenance repeated explicit failure")
				}
				fresh := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID())
				runFailedCleanup(t, f, fresh.Job.Id)
				if readFailedCleanup(t, f, fresh.Job.Id, "").Job.Deleted != 1 {
					t.Fatal("fresh explicit retry could not clean up")
				}
			}
		})
	}
}

func TestFailedSubscriptionCleanupRPC(t *testing.T) {
	f := unreferencedInitialSubscription(t)
	excludeUnrelatedBatchFixture(t, f)
	f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	request := &pb.CleanupFailedSubscriptionsRequest{RequestId: string(domain.NewID())}
	_, err := f.client.CleanupFailedSubscriptions(context.Background(), connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("cleanup admitted an unauthenticated request", err)
	}
	accepted, err := f.client.CleanupFailedSubscriptions(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Msg.Job.Total != 0 || accepted.Msg.Job.State != pb.FailedSubscriptionCleanupState_FAILED_SUBSCRIPTION_CLEANUP_STATE_COMPLETED {
		t.Fatal(accepted.Msg)
	}
	read := &pb.GetFailedSubscriptionCleanupRequest{JobId: accepted.Msg.Job.Id}
	_, err = f.client.GetFailedSubscriptionCleanup(context.Background(), connect.NewRequest(read))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("cleanup status allowed an unauthenticated read", err)
	}
	status, err := f.client.GetFailedSubscriptionCleanup(context.Background(), subscriptionRequest(f.service.Identity.Token, read))
	if err != nil || !proto.Equal(status.Msg.Job, accepted.Msg.Job) {
		t.Fatal("generated RPC did not preserve the admitted job", status, err)
	}
	replay, err := f.client.CleanupFailedSubscriptions(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil || !replay.Msg.Replayed || !proto.Equal(replay.Msg.Job, accepted.Msg.Job) {
		t.Fatal("generated RPC lost the original receipt", replay, err)
	}
}

// The shared subscription fixture includes a second, referenced disconnected
// account. These original-login tests isolate it; mixed-inventory tests below
// explicitly cover referenced disconnected accounts.
func excludeUnrelatedBatchFixture(t *testing.T, f *subscriptionFixture) {
	t.Helper()
	_, err := f.service.Store.Mutate(failedLoginContext(), domain.NewID(), "fixture.cleanup.exclude", nil, func(tx *store.Tx) (any, error) {
		rows, err := all(tx, domain.AccountKind)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if r.ID == f.input.AccountID {
				continue
			}
			a, err := store.Decode[domain.Account](r)
			if err != nil {
				return nil, err
			}
			a.Health = domain.AccountFailed
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
		}
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionCleanupDisconnectedServices(t *testing.T) {
	for _, service := range []domain.SubscriptionService{domain.SubscriptionChatGPT, domain.SubscriptionClaude, domain.SubscriptionGrok} {
		t.Run(string(service), func(t *testing.T) {
			f := unreferencedInitialSubscription(t)
			// The original referenced account is also selected, but must be retained.
			f.changeFailedLogin(func(a *domain.Account) {
				a.SubscriptionService = service
				a.Subscription = nil
			})
			accepted := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID())
			if accepted.Job.Total != 2 {
				t.Fatal(accepted)
			}
			runFailedCleanup(t, f, accepted.Job.Id)
			result := readFailedCleanup(t, f, accepted.Job.Id, "")
			if result.Job.Deleted != 1 || result.Job.Retained != 1 {
				t.Fatal(result)
			}
			for _, item := range result.Results {
				if item.AccountId != string(f.input.AccountID) && item.Reason != pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_REFERENCED {
					t.Fatal(item)
				}
			}
		})
	}
}

func TestDisconnectedSubscriptionCleanupOwnershipAndVault(t *testing.T) {
	f := unreferencedInitialSubscription(t)
	excludeUnrelatedBatchFixture(t, f)
	_, original := f.record()
	if !disconnectedSubscription(original) {
		t.Fatal("empty disconnected account excluded")
	}
	for _, change := range []func(*domain.Account){
		func(a *domain.Account) { a.Type = domain.APIAccount },
		func(a *domain.Account) { a.Health = domain.AccountReady },
		func(a *domain.Account) { a.Subscription = &domain.SubscriptionState{RecoveryRequired: true} },
		func(a *domain.Account) {
			a.Subscription = &domain.SubscriptionState{Generation: domain.NewID(), RecoveryRequired: true}
		},
		func(a *domain.Account) {
			a.Subscription = &domain.SubscriptionState{Lease: &domain.SubscriptionLease{}}
		},
		func(a *domain.Account) {
			a.Subscription = &domain.SubscriptionState{Pending: &domain.SubscriptionOperation{}}
		},
	} {
		raw, _ := json.Marshal(original)
		var a domain.Account
		_ = json.Unmarshal(raw, &a)
		change(&a)
		if disconnectedSubscription(a) {
			t.Fatal("independent ownership included")
		}
	}
	ref := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	if _, err := f.secrets.Put(context.Background(), ref, []byte("synthetic-staged-token")); err != nil {
		t.Fatal(err)
	}
	accepted := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID())
	runFailedCleanup(t, f, accepted.Job.Id)
	result := readFailedCleanup(t, f, accepted.Job.Id, "")
	if result.Job.Deleted != 0 || result.Job.Retained != 1 {
		t.Fatal("status bypassed credential proof", result)
	}
	if _, err := f.secrets.Get(context.Background(), ref); err != nil {
		t.Fatal("disconnected metadata deleted protected credentials", err)
	}
}

func TestCleanupVersionOneChildRemainsReadable(t *testing.T) {
	f := batchFailedAccount(t, false)
	accepted := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID())
	_, err := f.service.Store.Mutate(failedLoginContext(), domain.NewID(), "fixture.cleanup.legacy", nil, func(tx *store.Tx) (any, error) {
		rows, err := tx.Jobs("", domain.ID(accepted.Job.Id), "", "", 2)
		if err != nil || len(rows) != 1 {
			return nil, failedCleanupUnavailable()
		}
		j, in, _, err := decodeFailedCleanupAccount(rows[0], domain.ID(accepted.Job.Id))
		if err != nil {
			return nil, err
		}
		in.Version = 1
		j.Input, _ = json.Marshal(in)
		_, err = tx.PutJob(rows[0].ID, rows[0].Revision, "", "", j)
		return struct{}{}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	runFailedCleanup(t, f, accepted.Job.Id)
	if result := readFailedCleanup(t, f, accepted.Job.Id, ""); result.Job.Deleted != 1 {
		t.Fatal(result)
	}
}

func TestSubscriptionCleanupCompletedLogout(t *testing.T) {
	f := batchFailedAccount(t, true)
	f.changeFailedLogin(func(a *domain.Account) {
		a.Subscription.ServerOperation.Action = domain.SubscriptionLogout
		a.Subscription.ServerOperation.State = domain.SubscriptionSucceeded
		a.Subscription.ServerOperation.CleanupPhase = ""
	})
	accepted := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID())
	if accepted.Job.Total != 1 {
		t.Fatal("completed logout excluded", accepted)
	}
	runFailedCleanup(t, f, accepted.Job.Id)
	if result := readFailedCleanup(t, f, accepted.Job.Id, ""); result.Job.Deleted != 1 {
		t.Fatal(result)
	}
}

func TestSubscriptionCleanupCompletedWorkerLogout(t *testing.T) {
	f := unreferencedInitialSubscription(t)
	raw := f.login()
	clear(raw)
	op := f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	if err != nil {
		t.Fatal(err)
	}
	clear(lease.Bundle)
	if _, err := f.finish(lease, nil, true, false, true); err != nil {
		t.Fatal(err)
	}
	accepted := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID())
	if accepted.Job.Total != 2 {
		t.Fatal("confirmed Worker logout excluded", accepted)
	}
	runFailedCleanup(t, f, accepted.Job.Id)
	result := readFailedCleanup(t, f, accepted.Job.Id, "")
	if result.Job.Deleted != 1 || result.Job.Retained != 1 {
		t.Fatal(result)
	}
}

func TestSubscriptionCleanupLegacyDisconnectedFailure(t *testing.T) {
	f := batchFailedAccount(t, true)
	f.changeFailedLogin(func(a *domain.Account) { a.Subscription.ServerOperation.CleanupPhase = "" })
	accepted := acceptFailedCleanup(t, f, failedLoginContext(), domain.NewID())
	runFailedCleanup(t, f, accepted.Job.Id)
	if result := readFailedCleanup(t, f, accepted.Job.Id, ""); result.Job.Deleted != 1 {
		t.Fatal("disconnected legacy failure required a replacement cleanup", result)
	}
}
