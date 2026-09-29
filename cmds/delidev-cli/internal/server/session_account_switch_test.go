package server

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func accountSwitchFixture(t *testing.T, mode domain.NativeHistoryMode, settle bool) (*continuationFixture, *pb.Resource, string) {
	t.Helper()
	base := newFirstDispatchFixture(t)
	var a domain.Account
	if domain.Decode(base.account.DocumentJson, &a) != nil {
		t.Fatal("invalid fixture account")
	}
	b := base.save(pb.EntityKind_ENTITY_KIND_ACCOUNT, domain.Account{Alias: "Compatible B", ProviderID: a.ProviderID, Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected})
	connected, err := connectAccount(base.accountFixture, b, domain.NewID(), "", true)
	if err != nil {
		t.Fatal(err)
	}
	b = connected.Msg.Account
	base.mutateAgent(t, func(agent *domain.Agent) {
		agent.Accounts = append(agent.Accounts, domain.WeightedAccount{ID: domain.ID(b.Id), Weight: 1})
	})
	f := &continuationFixture{firstDispatchFixture: base, thread: domain.NewID()}
	if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	f.claim(t)
	if f.input.AccountID != domain.ID(base.account.Id) {
		t.Fatal("unvalidated B was selected for initial execution")
	}
	validated, err := base.accounts.ValidateAccount(context.Background(), ownerRequest(base.identity, &pb.ValidateAccountRequest{Mutation: acctMutation(b, domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	b = validated.Msg.Account
	token := f.grant(t)
	f.publish(t, domain.ExecutionThreadBound, 1, "")
	f.publish(t, domain.ExecutionInputAccepted, 2, "")
	if mode != "" {
		lease, err := f.service.executionAuthority.Acquire(context.Background(), token)
		if err != nil {
			t.Fatal(err)
		}
		if err := lease.ObserveHistory(context.Background(), mode == domain.AccountBoundHistory); err != nil {
			t.Fatal(err)
		}
		// Account-bound use is sticky even if a later native request sends history.
		if mode == domain.AccountBoundHistory {
			if err := lease.ObserveHistory(context.Background(), false); err != nil {
				t.Fatal(err)
			}
		}
		lease.Release()
	}
	if settle {
		f.finish(t, domain.ExecutionStopped)
	}
	return f, b, token
}

func switchRequest(f *continuationFixture, b *pb.Resource, t *testing.T) *pb.SwitchSessionAccountRequest {
	r := f.refresh(t)
	return &pb.SwitchSessionAccountRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, AccountId: b.Id}
}

func TestStoppedAccountSwitchRetainsHistoryAndRequiresExplicitResume(t *testing.T) {
	f, b, oldToken := accountSwitchFixture(t, domain.FullNativeHistory, true)
	before := f.refresh(t)
	original, _ := store.Decode[domain.Session](before)
	initial, _ := json.Marshal(original.InitialExecution)
	job, err := f.service.Store.Get(context.Background(), domain.JobKind, domain.ID(f.job.Id))
	if err != nil {
		t.Fatal(err)
	}
	jobBytes := append([]byte(nil), job.Data...)
	req := switchRequest(f, b, t)
	client := sessionClient(f.accountFixture)
	response, err := client.SwitchSessionAccount(context.Background(), ownerRequest(f.identity, req))
	if err != nil {
		t.Fatal(err)
	}
	selected, _ := store.Decode[domain.Session](f.refresh(t))
	retained, _ := json.Marshal(selected.InitialExecution)
	if !bytes.Equal(initial, retained) || selected.ExecutionSelection() != original.ExecutionSelection() || selected.Dispatch != domain.DispatchPaused || selected.NextExecutionIntent != "" || len(selected.AccountChanges) != 1 || selected.AccountChanges[0].Revision != before.Revision+1 || selected.AccountChanges[0].AfterExecutionID != f.input.ExecutionID {
		t.Fatal("selection changed historical execution or resumed input")
	}
	if response.Msg.Change.ExecutionJob.Id != f.job.Id || !bytes.Equal(jobBytes, response.Msg.Change.ExecutionJob.DocumentJson) {
		t.Fatal("selection rewrote old assignment")
	}
	// Losing the acknowledgement replays one selection event, even after Resume.
	replay, err := client.SwitchSessionAccount(context.Background(), ownerRequest(f.identity, req))
	if err != nil || !replay.Msg.Change.Replayed || replay.Msg.Change.Session.Revision != response.Msg.Change.Session.Revision {
		t.Fatal("switch replay duplicated history", err)
	}
	if _, err := f.service.executionAuthority.Acquire(context.Background(), oldToken); err == nil {
		t.Fatal("old execution scope survived terminal cleanup")
	}
	next := f.enqueue(t, "new B input", domain.ExecuteMode)
	if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err == nil {
		t.Fatal("selection permitted automatic fallback")
	}
	f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
	previous := f.input
	f.claim(t)
	if f.input.InputID != domain.ID(next.Id) || f.input.AccountID != domain.ID(b.Id) || f.input.Continuation.PreviousAccountID != previous.AccountID || f.input.Continuation.PreviousConnectionID != previous.ConnectionID || f.input.ExecutionID == previous.ExecutionID || f.input.ConfigurationDigest != previous.ConfigurationDigest {
		t.Fatal("fresh assignment lost new or previous account ownership")
	}
	newToken := f.grant(t)
	lease, err := f.service.executionAuthority.Acquire(context.Background(), newToken)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if lease.Scope.AccountID != domain.ID(b.Id) || newToken == oldToken {
		t.Fatal("new execution scope is not B")
	}
	// Even a reference registered under B cannot reintroduce account-bound state
	// after switching. No prior response/conversation identifier is transferable.
	for _, kind := range []domain.NativeReferenceKind{domain.NativeResponseReference, domain.NativeConversationReference} {
		_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.owned-reference", kind, func(tx *store.Tx) (any, error) {
			return nil, tx.ObserveExecutionReference(store.ExecutionReference{SessionID: f.input.SessionID, AccountID: f.input.AccountID, ConnectionID: f.input.ConnectionID, ModelID: f.input.Configuration.ModelID, Kind: kind, NativeID: "original-account-state"})
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := lease.AuthorizeReference(context.Background(), kind, "original-account-state"); err == nil {
			t.Fatal("account-switched scope authorized remote history")
		}
	}
	if _, err := client.SwitchSessionAccount(context.Background(), ownerRequest(f.identity, req)); err != nil {
		t.Fatal("exact old receipt did not retain current-state replay", err)
	}
}

func TestAccountSwitchRejectsUncertainOrIneligibleSelectionsAtomically(t *testing.T) {
	for _, scenario := range []string{"active", "unknown-history", "account-bound", "cleanup", "stale", "disabled-B", "outside-snapshot", "provider-off", "archived", "unconfirmed", "worker", "project-restricted", "title-uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			mode, settle := domain.FullNativeHistory, true
			if scenario == "active" {
				settle = false
			}
			if scenario == "unknown-history" {
				mode = ""
			}
			if scenario == "account-bound" {
				mode = domain.AccountBoundHistory
			}
			f, b, _ := accountSwitchFixture(t, mode, settle)
			if scenario == "disabled-B" {
				var account domain.Account
				domain.Decode(b.DocumentJson, &account)
				account.Enabled = false
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.disable-B", nil, func(tx *store.Tx) (any, error) {
					return tx.Put(domain.AccountKind, domain.ID(b.Id), b.Revision, "", "", account)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "provider-off" {
				provider, err := f.service.Store.Get(context.Background(), domain.ProviderKind, f.input.Configuration.ProviderID)
				if err != nil {
					t.Fatal(err)
				}
				value, _ := store.Decode[domain.Provider](provider)
				off := false
				value.Enabled = &off
				_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.provider-off", nil, func(tx *store.Tx) (any, error) {
					return tx.Put(domain.ProviderKind, provider.ID, provider.Revision, "", "", value)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "cleanup" || scenario == "archived" || scenario == "unconfirmed" || scenario == "project-restricted" || scenario == "title-uncertain" {
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.unsafe-selection", nil, func(tx *store.Tx) (any, error) {
					sr, session, err := sessionRecord(tx, f.input.SessionID)
					if err != nil {
						return nil, err
					}
					switch scenario {
					case "cleanup":
						session.Execution.CleanupVerified = false
					case "archived":
						session.Archive = domain.Archived
					case "unconfirmed":
						session.Execution.UnconfirmedResponses = 1
					case "title-uncertain":
						session.TitleState = domain.TitleUncertain
					case "project-restricted":
						session.ProjectID = domain.NewID()
						if _, err := tx.Put(domain.ProjectKind, session.ProjectID, 0, "", "", domain.Project{Name: "Restricted", Agents: domain.Restriction{}, Accounts: domain.Restriction{Configured: true, IDs: []domain.ID{f.input.AccountID}}}); err != nil {
							return nil, err
						}
					}
					return tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, session.ProjectID, session)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			req := switchRequest(f, b, t)
			if scenario == "stale" {
				req.Mutation.ExpectedRevision--
			}
			if scenario == "outside-snapshot" {
				req.AccountId = string(domain.NewID())
			}
			identity := f.identity
			if scenario == "worker" {
				identity = f.workerIdentity
			}
			before := f.refresh(t)
			_, err := sessionClient(f.accountFixture).SwitchSessionAccount(context.Background(), ownerRequest(identity, req))
			if err == nil {
				t.Fatal("unsafe switch succeeded")
			}
			after := f.refresh(t)
			if before.Revision != after.Revision || !bytes.Equal(before.Data, after.Data) {
				t.Fatal("rejected selection changed session history")
			}
		})
	}
}
