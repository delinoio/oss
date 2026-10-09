// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestOpenCodeGoProtectedKeyLifecycle(t *testing.T) {
	f := newAccountFixture(t)
	initial := f.save(pb.EntityKind_ENTITY_KIND_ACCOUNT, domain.Account{Alias: "Go Plus", Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionOpenCodeGo, Enabled: true, Health: domain.AccountDisconnected})
	id := domain.NewID()
	response, err := connectAccount(f, initial, id, "fixture-opencode-go-key", false)
	if err != nil {
		t.Fatal(err)
	}
	account := accountBody(t, response.Msg.Account)
	if account.ProviderID != "" || account.Subscription != nil || account.Health != domain.AccountReady || account.Connection == nil || account.Connection.Authentication != domain.BearerAuth || account.Connection.APIFormat == nil || *account.Connection.APIFormat != domain.OpenCodeGoProvider().LegacyAPIFormat() {
		t.Fatal("key lifecycle acquired native login or arbitrary provider authority")
	}
	replay, err := connectAccount(f, initial, id, "fixture-opencode-go-key", false)
	if err != nil || !replay.Msg.Replayed {
		t.Fatalf("original connect receipt: %v", err)
	}
	_, err = connectAccount(f, initial, id, "different-key", false)
	wantAccountCode(t, err, domain.Conflict)
	puts, _, active := f.secrets.counts()
	if puts != 1 || active != 1 {
		t.Fatal("connection duplicated protected credential")
	}
	f.shutdown()
	f.start()
	replay, err = connectAccount(f, initial, id, "fixture-opencode-go-key", false)
	if err != nil || !replay.Msg.Replayed {
		t.Fatalf("restart receipt: %v", err)
	}
	removal := &pb.DisconnectAccountRequest{Mutation: acctMutation(response.Msg.Account, domain.NewID())}
	removed, err := f.accounts.DisconnectAccount(context.Background(), ownerRequest(f.identity, removal))
	if err != nil || len(removed.Msg.CleanupProblemJson) != 0 {
		t.Fatalf("cleanup: %v", err)
	}
	after := accountBody(t, removed.Msg.Account)
	if after.Connection != nil || after.Subscription != nil || after.Health != domain.AccountDisconnected {
		t.Fatal("disconnect retained active authority")
	}
	_, deletes, active := f.secrets.counts()
	if deletes != 1 || active != 0 {
		t.Fatal("cleanup did not remove original protected key")
	}
}

func TestOpenCodeGoOriginalNativeProofAndRevokedLease(t *testing.T) {
	f := newHarnessAuthorityFixture(t, domain.OpenCodeGoEndpoint, domain.OpenCode)
	f.input.Configuration.Subscription = true
	f.input.Configuration.SubscriptionService = domain.SubscriptionOpenCodeGo
	f.input.Configuration.ProviderID = ""
	digest, err := f.input.Configuration.Digest()
	if err != nil {
		t.Fatal(err)
	}
	f.input.ConfigurationDigest = digest
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.go-authority", f.job, func(tx *store.Tx) (any, error) {
		r, e := tx.Get(domain.AccountKind, f.input.AccountID)
		if e != nil {
			return nil, e
		}
		a, e := store.Decode[domain.Account](r)
		if e != nil {
			return nil, e
		}
		a.Type = domain.SubscriptionAccount
		a.ProviderID = ""
		a.SubscriptionService = domain.SubscriptionOpenCodeGo
		profile := domain.OpenCodeGoProvider().LegacyAPIFormat()
		a.Connection.APIFormat = &profile
		if _, e = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, a); e != nil {
			return nil, e
		}
		r, e = tx.Get(domain.ModelKind, f.input.Configuration.ModelID)
		if e != nil {
			return nil, e
		}
		m, e := store.Decode[domain.Model](r)
		if e != nil {
			return nil, e
		}
		m.ProviderID = ""
		m.SourceKind = domain.SubscriptionModel
		m.SubscriptionService = domain.SubscriptionOpenCodeGo
		if _, e = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, m); e != nil {
			return nil, e
		}
		r, e = tx.Get(domain.MachineKind, f.input.MachineID)
		if e != nil {
			return nil, e
		}
		machine, e := store.Decode[domain.Machine](r)
		if e != nil {
			return nil, e
		}
		machine.WorkerCapabilities = []domain.WorkerCapability{domain.OpenCodeGoSubscriptionsV1}
		if _, e = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, machine); e != nil {
			return nil, e
		}
		r, e = tx.Get(domain.SessionKind, f.input.SessionID)
		if e != nil {
			return nil, e
		}
		session, e := store.Decode[domain.Session](r)
		if e != nil {
			return nil, e
		}
		session.InitialExecution.Configuration = f.input.Configuration
		session.InitialExecution.ConfigurationDigest = digest
		if _, e = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, session); e != nil {
			return nil, e
		}
		r, e = tx.Get(domain.JobKind, f.job)
		if e != nil {
			return nil, e
		}
		job, e := store.Decode[domain.Job](r)
		if e != nil {
			return nil, e
		}
		job.Input, _ = json.Marshal(f.input)
		updated, e := tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
		if e == nil {
			f.register.Mutation.ExpectedRevision = updated.Revision
		}
		return updated, e
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.registerGrant(t).ProxyPath != "/api-proxy/v1" {
		t.Fatal("missing Go relay route")
	}
	if lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token); err == nil {
		lease.Release()
		t.Fatal("credential available before original native session")
	}
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.go-native-proof", f.job, func(tx *store.Tx) (any, error) {
		r, session, e := sessionRecord(tx, f.input.SessionID)
		if e != nil {
			return nil, e
		}
		session.Execution = &domain.ExecutionProgress{ExecutionID: f.input.ExecutionID, NativeThreadID: "ses_0123456789abABCDEFGHIJKLMN"}
		return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, session)
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Scope.OpenCodeSession != "ses_0123456789abABCDEFGHIJKLMN" || lease.Scope.Provider.Endpoint != domain.OpenCodeGoEndpoint {
		t.Fatal("scope lost original proof")
	}
	key, err := lease.Key(context.Background())
	if err != nil || len(key) == 0 {
		t.Fatalf("original protected key: %v", err)
	}
	for i := range key {
		key[i] = 0
	}
	done := make(chan error, 1)
	go func() { done <- f.service.executionAuthority.stopAccount(context.Background(), f.input.AccountID) }()
	select {
	case <-lease.Context.Done():
	case <-time.After(time.Second):
		t.Fatal("revocation did not cancel joined request")
	}
	lease.Release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestOpenCodeGoCleanupFailureRequiresOriginalExplicitRetry(t *testing.T) {
	f := newAccountFixture(t)
	initial := f.save(pb.EntityKind_ENTITY_KIND_ACCOUNT, domain.Account{Alias: "Go", Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionOpenCodeGo, Enabled: true, Health: domain.AccountDisconnected})
	_, err := connectAccount(f, initial, domain.NewID(), "", false)
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("empty-key admission: %v", err)
	}
	connected, err := connectAccount(f, initial, domain.NewID(), "fixture-go-cleanup-key", false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = connectAccount(f, initial, domain.NewID(), "new-key", false)
	wantAccountCode(t, err, domain.Conflict)
	f.secrets.mu.Lock()
	f.secrets.deleteError = domain.Fail(domain.ConfirmationRequired, "Fixture locked vault.", "")
	f.secrets.mu.Unlock()
	input := &pb.DisconnectAccountRequest{Mutation: acctMutation(connected.Msg.Account, domain.NewID())}
	removed, err := f.accounts.DisconnectAccount(context.Background(), ownerRequest(f.identity, input))
	if err != nil {
		t.Fatal(err)
	}
	account := accountBody(t, removed.Msg.Account)
	if account.Connection != nil || account.Removal == nil || len(removed.Msg.CleanupProblemJson) == 0 {
		t.Fatal("failed cleanup regained connection authority")
	}
	_, err = connectAccount(f, removed.Msg.Account, domain.NewID(), "replacement", false)
	wantAccountCode(t, err, domain.Conflict)
	_, err = f.config.DeleteConfiguration(context.Background(), ownerRequest(f.identity, &pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: acctMutation(removed.Msg.Account, domain.NewID())}))
	wantAccountCode(t, err, domain.Conflict)
	_, attempts, _ := f.secrets.counts()
	f.shutdown()
	f.start()
	_, afterRestart, _ := f.secrets.counts()
	if afterRestart != attempts {
		t.Fatal("restart retried terminal cleanup")
	}
	f.secrets.mu.Lock()
	f.secrets.deleteError = nil
	f.secrets.mu.Unlock()
	completed, err := f.accounts.DisconnectAccount(context.Background(), ownerRequest(f.identity, input))
	if err != nil || !completed.Msg.Replayed || len(completed.Msg.CleanupProblemJson) != 0 {
		t.Fatalf("original confirmed cleanup: %v", err)
	}
	deletion := &pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: acctMutation(completed.Msg.Account, domain.NewID())}
	if _, err = f.config.DeleteConfiguration(context.Background(), ownerRequest(f.identity, deletion)); err != nil {
		t.Fatal(err)
	}
	replay, err := f.config.DeleteConfiguration(context.Background(), ownerRequest(f.identity, deletion))
	if err != nil || !replay.Msg.Replayed {
		t.Fatalf("original deletion receipt: %v", err)
	}
}

func configureOpenCodeGoDispatch(t *testing.T, f *firstDispatchFixture) {
	t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.go-dispatch", nil, func(tx *store.Tx) (any, error) {
		r, e := tx.Get(domain.AccountKind, domain.ID(f.account.Id))
		if e != nil {
			return nil, e
		}
		a, e := store.Decode[domain.Account](r)
		if e != nil {
			return nil, e
		}
		a.Type = domain.SubscriptionAccount
		a.ProviderID = ""
		a.SubscriptionService = domain.SubscriptionOpenCodeGo
		a.Validation = nil
		a.Catalog = nil
		a.Connection.Authentication = domain.BearerAuth
		profile := domain.OpenCodeGoProvider().LegacyAPIFormat()
		a.Connection.APIFormat = &profile
		if _, e = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, a); e != nil {
			return nil, e
		}
		ar, e := tx.Get(domain.AgentKind, domain.ID(f.agent.Id))
		if e != nil {
			return nil, e
		}
		agent, e := store.Decode[domain.Agent](ar)
		if e != nil {
			return nil, e
		}
		r, e = tx.Get(domain.ModelKind, agent.ModelID)
		if e != nil {
			return nil, e
		}
		model, e := store.Decode[domain.Model](r)
		if e != nil {
			return nil, e
		}
		model.ProviderID = ""
		model.SourceKind = domain.SubscriptionModel
		model.SubscriptionService = domain.SubscriptionOpenCodeGo
		if _, e = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, model); e != nil {
			return nil, e
		}
		r, e = tx.Get(domain.MachineKind, domain.ID(f.machine.Id))
		if e != nil {
			return nil, e
		}
		machine, e := store.Decode[domain.Machine](r)
		if e != nil {
			return nil, e
		}
		machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.OpenCodeGoSubscriptionsV1, domain.OpenCodeGeneralChatForkV1)
		return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, machine)
	})
	if err != nil {
		t.Fatal(err)
	}
}
func newOpenCodeGoContinuation(t *testing.T) *continuationFixture {
	f := &continuationFixture{firstDispatchFixture: newFirstDispatchFixtureForHarness(t, domain.OpenCode, domain.ExecuteMode), thread: "ses_01960dcbe1faabcdefghijklmn", turn: "msg_01960dcbe1faABCDEFGHIJKLMN"}
	configureOpenCodeGoDispatch(t, f.firstDispatchFixture)
	if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	f.claim(t)
	f.turn = "msg_01960dcbe1faABCDEFGHIJKLMN"
	f.completeOpenCode(t, domain.ExecutionSucceeded)
	return f
}
func TestOpenCodeGoContinuationRetainsOriginalServiceAccountAndSession(t *testing.T) {
	f := newOpenCodeGoContinuation(t)
	original := f.input
	f.enqueue(t, "fixture follow-up", domain.PlanMode)
	if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	f.claim(t)
	if !f.input.Configuration.IsOpenCodeGo() || f.input.AccountID != original.AccountID || f.input.ConnectionID != original.ConnectionID || f.input.ConfigurationDigest != original.ConfigurationDigest || f.input.Continuation == nil || f.input.Continuation.Completion.NativeThreadID != domain.NativeIdentity(f.thread) {
		t.Fatal("continuation changed immutable source or native session")
	}
}
func TestOpenCodeGoIndependentForkHasNoSyntheticLoginGeneration(t *testing.T) {
	f := newOpenCodeGoContinuation(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.go-fork-transcript", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.MessageKind, domain.NewID(), 0, f.input.SessionID, "", domain.ExecutionMessage{ExecutionID: f.input.ExecutionID, NativeThreadID: string(f.thread), NativeTurnID: string(f.turn), NativeID: "prt_01960dcbe1fcabcdefghijklmn", NativeParentID: "msg_01960dcbe1fcABCDEFGHIJKLMN", Role: domain.AssistantMessage, Text: "Fixture original assistant.", State: domain.MessageComplete, FirstSequence: 5, LastSequence: 6})
	})
	if err != nil {
		t.Fatal(err)
	}
	source := f.refresh(t)
	response, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, &pb.ForkSessionRequest{Mutation: acctMutation(resourceForTest(source), domain.NewID()), ExpectedTurnId: string(f.turn), Name: "Independent Go child", Workspace: pb.ForkWorkspace_FORK_WORKSPACE_GENERAL_CHAT}))
	if err != nil {
		t.Fatal(err)
	}
	_, input := forkClaimFixture(t, f, response.Msg.Job.Id)
	if input.SubscriptionGeneration != "" || !input.SourceAssignment.Configuration.IsOpenCodeGo() || input.Snapshot.InitialAccountID != f.input.AccountID || input.ChildSessionID == input.SourceSessionID || input.OpenCode == nil {
		t.Fatal("fork changed account or acquired native login authority")
	}
}

func TestOpenCodeGoOlderWorkerCannotReceiveAssignment(t *testing.T) {
	f := newFirstDispatchFixtureForHarness(t, domain.OpenCode, domain.ExecuteMode)
	configureOpenCodeGoDispatch(t, f)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.old-go-worker", nil, func(tx *store.Tx) (any, error) {
		r, e := tx.Get(domain.MachineKind, domain.ID(f.machine.Id))
		if e != nil {
			return nil, e
		}
		machine, e := store.Decode[domain.Machine](r)
		if e != nil {
			return nil, e
		}
		machine.WorkerCapabilities = slices.DeleteFunc(machine.WorkerCapabilities, func(capability domain.WorkerCapability) bool { return capability == domain.OpenCodeGoSubscriptionsV1 })
		return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, machine)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.service.dispatchExecution(context.Background(), f.refresh(t))
	if domain.SafeError(err).Code != domain.Unsupported {
		t.Fatalf("old Worker dispatch: %v", err)
	}
	session, _ := store.Decode[domain.Session](f.refresh(t))
	if session.InitialExecution != nil || session.ActiveExecutionID != "" {
		t.Fatal("unsupported Worker gained immutable account assignment")
	}
}
