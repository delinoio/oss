// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
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
		agent.Routes[0].Accounts = append(agent.Routes[0].Accounts, domain.WeightedAccount{ID: domain.ID(b.Id), Weight: 1})
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

func TestAccountSwitchRequiresOwnerOrClientBeforeMutation(t *testing.T) {
	f, b, _ := accountSwitchFixture(t, domain.FullNativeHistory, true)
	ctx := context.Background()
	digest := sha256.Sum256([]byte(f.workerIdentity.Token))
	worker, err := f.service.Store.Authenticate(ctx, digest[:])
	if err != nil || worker.Type != domain.WorkerDevice {
		t.Fatal("fixture Worker is not currently authorized", err)
	}
	client := sessionClient(f.accountFixture)
	req := switchRequest(f, b, t)
	before := f.refresh(t)
	count := historyReceiptCounter(t, f)
	receipts := count()
	changed := f.service.Store.Changed()
	assertDenied := func(err error) {
		t.Helper()
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("account selection did not reject unauthorized role", err)
		}
		after := f.refresh(t)
		if after.Revision != before.Revision || !bytes.Equal(after.Data, before.Data) || count() != receipts {
			t.Fatal("role rejection changed session or retained a receipt")
		}
		select {
		case <-changed:
			t.Fatal("role rejection woke store watchers")
		default:
		}
	}
	// HTTP already rejects Workers. The service must also enforce its role
	// boundary before mutation when invoked with an authenticated principal.
	_, err = client.SwitchSessionAccount(ctx, ownerRequest(f.workerIdentity, req))
	assertDenied(err)
	for _, actor := range []context.Context{
		domain.WithPrincipal(ctx, worker),
		ctx,
		domain.WithPrincipal(ctx, domain.Principal{}),
	} {
		_, err := f.service.SwitchSessionAccount(actor, connect.NewRequest(req))
		assertDenied(err)
	}
	// Denied calls cannot consume the owner's exact request identity.
	accepted, err := client.SwitchSessionAccount(ctx, ownerRequest(f.identity, req))
	if err != nil || accepted.Msg.Change.Replayed || count() != receipts+1 {
		t.Fatal("denied call consumed owner selection identity", err)
	}
}

func TestAccountSwitchPairedClientReplayRechecksRevocation(t *testing.T) {
	f, b, _ := accountSwitchFixture(t, domain.FullNativeHistory, true)
	ctx := context.Background()
	identity := localOriginClient(t, f.accountFixture)
	digest := sha256.Sum256([]byte(identity.Token))
	actor, err := f.service.Store.Authenticate(ctx, digest[:])
	if err != nil || actor.Type != domain.ClientDevice {
		t.Fatal("fixture client is not currently authorized", err)
	}
	client := sessionClient(f.accountFixture)
	req := switchRequest(f, b, t)
	accepted, err := client.SwitchSessionAccount(ctx, ownerRequest(identity, req))
	if err != nil || accepted.Msg.Change.Replayed {
		t.Fatal("paired client could not select account", err)
	}
	count := historyReceiptCounter(t, f)
	receipts := count()
	replay, err := client.SwitchSessionAccount(ctx, ownerRequest(identity, req))
	if err != nil || !replay.Msg.Change.Replayed || replay.Msg.Change.Session.Revision != accepted.Msg.Change.Session.Revision || count() != receipts {
		t.Fatal("paired client replay changed its selection", err)
	}
	if _, err := client.SwitchSessionAccount(ctx, ownerRequest(f.identity, req)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("another actor inherited the client receipt", err)
	}
	device, err := f.service.Store.Get(ctx, domain.DeviceKind, actor.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	devices := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, f.endpoint.URL)
	if _, err := devices.RevokeDevice(ctx, ownerRequest(f.identity, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(device.ID), ExpectedRevision: device.Revision}})); err != nil {
		t.Fatal(err)
	}
	before, receipts := f.refresh(t), count()
	_, err = client.SwitchSessionAccount(ctx, ownerRequest(identity, req))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("revoked client replayed over HTTP", err)
	}
	_, err = f.service.SwitchSessionAccount(domain.WithPrincipal(ctx, actor), connect.NewRequest(req))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("revoked client principal replayed in service", err)
	}
	after := f.refresh(t)
	if after.Revision != before.Revision || !bytes.Equal(after.Data, before.Data) || count() != receipts {
		t.Fatal("revoked replay changed session or receipt history")
	}
}

func TestAccountSwitchBackPreservesPredecessorConnection(t *testing.T) {
	for _, reconnect := range []bool{false, true} {
		name := "original-connection"
		if reconnect {
			name = "reconnected-account"
		}
		t.Run(name, func(t *testing.T) {
			f, b, oldToken := accountSwitchFixture(t, domain.FullNativeHistory, true)
			ctx := context.Background()
			client := sessionClient(f.accountFixture)
			previous := f.input
			if _, err := client.SwitchSessionAccount(ctx, ownerRequest(f.identity, switchRequest(f, b, t))); err != nil {
				t.Fatal(err)
			}
			a := currentCatalogResource(t, f.accountFixture, f.account)
			if reconnect {
				disconnected, err := f.accounts.DisconnectAccount(ctx, ownerRequest(f.identity, &pb.DisconnectAccountRequest{Mutation: acctMutation(a, domain.NewID())}))
				if err != nil {
					t.Fatal(err)
				}
				connected, err := connectAccount(f.accountFixture, disconnected.Msg.Account, domain.NewID(), "", true)
				if err != nil {
					t.Fatal(err)
				}
				validated, err := f.accounts.ValidateAccount(ctx, ownerRequest(f.identity, &pb.ValidateAccountRequest{Mutation: acctMutation(connected.Msg.Account, domain.NewID())}))
				if err != nil {
					t.Fatal(err)
				}
				a = validated.Msg.Account
			}
			connection := accountBody(t, a).Connection.ID
			if reconnect && connection == previous.ConnectionID {
				t.Fatal("reconnect retained the original connection")
			}
			if _, err := client.SwitchSessionAccount(ctx, ownerRequest(f.identity, switchRequest(f, a, t))); err != nil {
				t.Fatal(err)
			}
			f.enqueue(t, "explicit A continuation", domain.ExecuteMode)
			f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
			f.claim(t)
			if f.input.AccountID != previous.AccountID || f.input.ConnectionID != connection || f.input.Continuation.Previous.ExecutionID != previous.ExecutionID {
				t.Fatal("switch-back changed successor or predecessor ownership")
			}
			c := f.input.Continuation
			if reconnect {
				if c.PreviousAccountID != previous.AccountID || c.PreviousConnectionID != previous.ConnectionID {
					t.Fatal("reconnected account lost the original checkpoint connection")
				}
				// A previous scope must remain a complete, distinct pair and may
				// never authorize automatic or account-bound continuation.
				for _, invalid := range []string{"same-scope", "missing-connection", "automatic", "account-bound"} {
					copy := *c
					switch invalid {
					case "same-scope":
						copy.PreviousConnectionID = connection
					case "missing-connection":
						copy.PreviousConnectionID = ""
					case "automatic":
						copy.Intent = domain.ContinueAutomatically
					case "account-bound":
						copy.Previous.NativeHistory = domain.AccountBoundHistory
					}
					if copy.Validate(f.input) == nil {
						t.Fatalf("unsafe predecessor scope accepted: %s", invalid)
					}
				}
			} else if c.PreviousAccountID != "" || c.PreviousConnectionID != "" {
				t.Fatal("unchanged connection added a different predecessor scope")
			}
			lease, err := f.service.executionAuthority.Acquire(ctx, f.grant(t))
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			if lease.Scope.ConnectionID != connection {
				t.Fatal("successor grant reused the predecessor connection")
			}
			if _, err := f.service.executionAuthority.Acquire(ctx, oldToken); err == nil {
				t.Fatal("original execution grant survived the switch-back")
			}
		})
	}
}

func TestAccountSwitchRejectsUncertainOrIneligibleSelectionsAtomically(t *testing.T) {
	for _, scenario := range []string{"active", "unknown-history", "account-bound", "cleanup", "stale", "disabled-B", "outside-snapshot", "provider-off", "archived", "unconfirmed", "worker", "project-restricted", "title-uncertain", "contradictory-terminal"} {
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
			if scenario == "cleanup" || scenario == "archived" || scenario == "unconfirmed" || scenario == "project-restricted" || scenario == "title-uncertain" || scenario == "contradictory-terminal" {
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.unsafe-selection", nil, func(tx *store.Tx) (any, error) {
					sr, session, err := sessionRecord(tx, f.input.SessionID)
					if err != nil {
						return nil, err
					}
					switch scenario {
					case "contradictory-terminal":
						session.Outcome = domain.ExecutionRunning
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
			if scenario == "worker" && connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal("Worker rejection lost its role classification", err)
			}
			after := f.refresh(t)
			if before.Revision != after.Revision || !bytes.Equal(before.Data, after.Data) {
				t.Fatal("rejected selection changed session history")
			}
		})
	}
}

func TestAccountSwitchKeepsMeasuredUsageOnItsOriginalExecution(t *testing.T) {
	f, b, _ := accountSwitchFixture(t, domain.FullNativeHistory, false)
	ctx := context.Background()
	client := sessionClient(f.accountFixture)
	observe := func(digest string) domain.ID {
		t.Helper()
		id := domain.NewID()
		counts := reportedCounts(20)
		event := domain.ExecutionEvent{Version: 1, ExecutionID: f.input.ExecutionID, Sequence: 3, Kind: domain.ExecutionResponseUsageObserved, NativeThreadID: string(f.thread), NativeTurnID: string(f.turn), ObservationID: id, ResponseUsage: &domain.NativeResponseUsage{ResponseDigest: digest, Counts: &counts, CostEvidence: domain.UsageCostMissing}}
		raw, _ := json.Marshal(event)
		_, err := f.workerClient.PublishExecution(ctx, ownerRequest(f.workerIdentity, &pb.PublishExecutionRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, EventJson: raw}))
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	finish := func(outcome domain.ExecutionOutcome) {
		t.Helper()
		f.publish(t, domain.ExecutionTurnFinished, 4, outcome)
		completion := domain.ExecutionCompletion{Version: 2, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: domain.NativeIdentity(f.thread), NativeTurnID: domain.NativeIdentity(f.turn), LastSequence: 4, Outcome: outcome, CleanupVerified: true, NativeCheckpointDigest: strings.Repeat("ab", 32)}
		raw, _ := json.Marshal(completion)
		_, err := f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw}))
		if err != nil {
			t.Fatal(err)
		}
	}
	aID, aExecution := f.input.AccountID, f.input.ExecutionID
	aUsage := observe(strings.Repeat("a", 64))
	finish(domain.ExecutionStopped)
	if _, err := client.SwitchSessionAccount(ctx, ownerRequest(f.identity, switchRequest(f, b, t))); err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, "B measured response", domain.ExecuteMode)
	f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
	f.claim(t)
	f.grant(t)
	f.publish(t, domain.ExecutionThreadBound, 1, "")
	f.publish(t, domain.ExecutionInputAccepted, 2, "")
	bUsage := observe(strings.Repeat("b", 64))
	finish(domain.ExecutionSucceeded)
	for _, expected := range []struct{ id, account, execution domain.ID }{{aUsage, aID, aExecution}, {bUsage, domain.ID(b.Id), f.input.ExecutionID}} {
		record, err := f.service.Store.ResponseUsage(ctx, expected.id)
		if err != nil || record.Record.AccountID != expected.account || record.Record.ExecutionID != expected.execution || *record.Record.Usage.Counts.Total != 20 {
			t.Fatal("historical usage was relabeled", err)
		}
	}
}

func TestAccountSwitchCannotAlterSessionPendingPermanentDeletion(t *testing.T) {
	f, b, _ := accountSwitchFixture(t, domain.FullNativeHistory, true)
	ctx := context.Background()
	client := sessionClient(f.accountFixture)
	accepted := switchRequest(f, b, t)
	if _, err := client.SwitchSessionAccount(ctx, ownerRequest(f.identity, accepted)); err != nil {
		t.Fatal(err)
	}
	sr := f.refresh(t)
	deletion, err := client.DeleteSession(ctx, ownerRequest(f.identity, &pb.DeleteSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}}))
	if err != nil || deletion.Msg.Job.WorkersPending != 1 {
		t.Fatal("deletion did not retain original Worker cleanup", err)
	}
	before, err := f.service.Store.Get(ctx, domain.SessionKind, sr.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := historyReceiptCounter(t, f)
	receipts := count()
	// An accepted receipt is still an observation of current state; it must
	// neither revive selection authority nor repeat its historical append.
	replay, err := client.SwitchSessionAccount(ctx, ownerRequest(f.identity, accepted))
	if err != nil || !replay.Msg.Change.Replayed || replay.Msg.Change.Session.Revision != before.Revision {
		t.Fatal("accepted selection replay lost its current-state observation", err)
	}
	request := &pb.SwitchSessionAccountRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: before.Revision}, AccountId: f.account.Id}
	if _, err := client.SwitchSessionAccount(ctx, ownerRequest(f.identity, request)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("pending deletion allowed a fresh account selection", err)
	}
	after, err := f.service.Store.Get(ctx, domain.SessionKind, sr.ID)
	if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Data, before.Data) || count() != receipts {
		t.Fatal("denied selection changed pending deletion or receipt history", err)
	}
}
