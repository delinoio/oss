package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type firstDispatchFixture struct {
	*accountFixture
	service                 *Service
	selection               domain.CreateSession
	agent, account, machine *pb.Resource
	change                  *pb.SessionChange
	request                 *pb.CreateSessionRequest
}

// Public configuration/account/session APIs and authenticated Worker reports
// establish readiness here. The reported installation is a protocol fixture;
// ordinary tests never execute an installed harness or request inference.
func newFirstDispatchFixture(t *testing.T) *firstDispatchFixture {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	service := &Service{Store: db, Identity: security.Identity{ServerID: domain.NewID(), Token: "temporary-dispatch-owner"}, logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	server := httptest.NewServer(service.Handler(nil, true))
	t.Cleanup(func() { service.executionAuthority.cancel(); server.Close(); service.executionAuthority.close() })
	base := &accountFixture{t: t, endpoint: Endpoint{URL: server.URL, ServerID: service.Identity.ServerID}, identity: service.Identity}
	base.config = delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, server.URL)
	base.accounts = delidevv1connect.NewAccountServiceClient(http.DefaultClient, server.URL)
	base.resources = delidevv1connect.NewResourceServiceClient(http.DefaultClient, server.URL)
	identity, paired := pairedWorker(t, context.Background(), base.endpoint, base.identity)
	f := &firstDispatchFixture{accountFixture: base, service: service, machine: paired.Machine}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" || r.Header.Get("Authorization") != "" {
			t.Error("dispatch fixture performed unexpected provider work")
			http.Error(w, "unsupported", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"id":"fixture-model","object":"model"}]}`)
	}))
	t.Cleanup(upstream.Close)
	provider := base.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "Fixture", Endpoint: upstream.URL, Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth})
	account := base.save(pb.EntityKind_ENTITY_KIND_ACCOUNT, domain.Account{Alias: "Fixture", ProviderID: domain.ID(provider.Id), Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected})
	connected, err := connectAccount(base, account, domain.NewID(), "", true)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := base.accounts.ValidateAccount(context.Background(), ownerRequest(base.identity, &pb.ValidateAccountRequest{Mutation: acctMutation(connected.Msg.Account, domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	f.account = validated.Msg.Account
	model := base.save(pb.EntityKind_ENTITY_KIND_MODEL, domain.Model{Name: "Fixture", NativeID: "fixture-model", ProviderID: domain.ID(provider.Id), Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared})
	routing := domain.RoundRobin
	f.agent = base.save(pb.EntityKind_ENTITY_KIND_AGENT, domain.Agent{Name: "Fixture", Harness: domain.Codex, ModelID: domain.ID(model.Id), Accounts: []domain.WeightedAccount{{ID: domain.ID(account.Id), Weight: 1}}, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly}, Routing: &routing})
	f.selection = domain.CreateSession{Name: "Fixture", AgentID: domain.ID(f.agent.Id), MachineID: domain.ID(f.machine.Id), Workspace: domain.GeneralChat, Prompt: "first retained input", Mode: domain.PlanMode, Source: domain.ExternalCLISession}
	ctx, client, instance, stream := workspaceStream(t, base, identity, domain.ID(f.machine.Id))
	f.machine = currentCatalogResource(t, base, f.machine)
	selections, _ := json.Marshal(domain.ExecutableSelections{Executables: []domain.ExecutableSelection{{Harness: domain.Codex, Path: "/fixture/codex"}}})
	_, err = client.DiscoverHarnesses(ctx, ownerRequest(base.identity, &pb.DiscoverHarnessesRequest{Mutation: acctMutation(f.machine, domain.NewID()), SelectionsJson: selections, VerifyProtocol: true}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() || stream.Msg().Job == nil {
		t.Fatal("missing discovery", stream.Err())
	}
	discovery := stream.Msg().Job
	var job domain.Job
	if domain.Decode(discovery.DocumentJson, &job) != nil {
		t.Fatal("invalid discovery")
	}
	var discoveryInput domain.HarnessDiscoveryInput
	if domain.Decode(job.Input, &discoveryInput) != nil {
		t.Fatal("invalid discovery input")
	}
	observed := domain.HarnessDiscoveryOutput{Installations: discoveryInput.Selections.Installations()}
	for i := range observed.Installations {
		v := &observed.Installations[i]
		v.State = domain.InstallationMissing
		if v.Harness == domain.Codex {
			v.State = domain.InstallationDetected
			v.ResolvedPath = "/fixture/codex"
			v.Version = domain.CodexProtocolVersion
			v.ProtocolVerified = true
			v.Protocol = &domain.ProtocolObservation{Protocol: domain.CodexAppServer, State: domain.ProtocolVerified}
		}
	}
	raw, _ := json.Marshal(observed)
	if _, err = client.ReportWork(ctx, ownerRequest(identity, &pb.ReportWorkRequest{Mutation: acctMutation(discovery, domain.NewID()), MachineId: f.machine.Id, InstanceId: instance, OutputJson: raw})); err != nil {
		t.Fatal(err)
	}
	f.machine = currentCatalogResource(t, base, f.machine)
	f.request, f.change = createSessionFixture(t, base, f.selection)
	if !stream.Receive() || stream.Msg().Job == nil || stream.Msg().Job.Id != f.change.WorkspaceJob.Id {
		t.Fatal("missing preparation", stream.Err())
	}
	preparation := stream.Msg().Job
	if domain.Decode(preparation.DocumentJson, &job) != nil {
		t.Fatal("invalid workspace job")
	}
	var request workspace.PrepareRequest
	if domain.Decode(job.Input, &request) != nil {
		t.Fatal("invalid preparation")
	}
	manager := workspace.Manager{Root: filepath.Join(t.TempDir(), "worker")}
	manifest, err := manager.Prepare(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if _, err = client.ReportWork(ctx, ownerRequest(identity, &pb.ReportWorkRequest{Mutation: acctMutation(preparation, domain.NewID()), MachineId: f.machine.Id, InstanceId: instance, OutputJson: raw})); err != nil {
		t.Fatal(err)
	}
	f.refresh(t)
	return f
}
func (f *firstDispatchFixture) refresh(t *testing.T) store.Record {
	t.Helper()
	record, err := f.service.Store.Get(context.Background(), domain.SessionKind, domain.ID(f.change.Session.Id))
	if err != nil {
		t.Fatal(err)
	}
	return record
}
func (f *firstDispatchFixture) mutateAgent(t *testing.T, edit func(*domain.Agent)) {
	t.Helper()
	f.agent = currentCatalogResource(t, f.accountFixture, f.agent)
	var value domain.Agent
	if domain.Decode(f.agent.DocumentJson, &value) != nil {
		t.Fatal("invalid Agent")
	}
	edit(&value)
	raw, _ := json.Marshal(value)
	response, err := f.config.SaveConfiguration(context.Background(), ownerRequest(f.identity, &pb.SaveConfigurationRequest{Mutation: acctMutation(f.agent, domain.NewID()), Kind: pb.EntityKind_ENTITY_KIND_AGENT, SchemaVersion: 1, DocumentJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	f.agent = response.Msg.Resource
}
func TestInitialDispatchAtomicConfigurationRollbackAndCurrentReceipt(t *testing.T) {
	f := newFirstDispatchFixture(t)
	ctx := context.Background()
	f.mutateAgent(t, func(a *domain.Agent) { a.Options.MaxConcurrency = 2 })
	before := f.refresh(t)
	if err := f.service.dispatchInitial(ctx, before); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("unsupported option dispatched", err)
	}
	blocked := f.refresh(t)
	state, _ := store.Decode[domain.Session](blocked)
	input := inputBody(t, currentCatalogResource(t, f.accountFixture, f.change.Input))
	if state.InitialExecution != nil || state.ActiveExecutionID != "" || input.Delivery != domain.InputQueued || input.ExecutionID != "" || state.PendingInputs != 1 || state.Problem.Code != domain.Unsupported {
		t.Fatal("failed preflight consumed execution ownership")
	}
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		r, _, err := tx.Routing(f.selection.AgentID)
		if r.ID != "" {
			t.Error("failed preflight advanced routing")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_ = f.service.dispatchInitial(ctx, blocked)
	if f.refresh(t).Revision != blocked.Revision {
		t.Fatal("unchanged block created a revision loop")
	}
	f.mutateAgent(t, func(a *domain.Agent) { a.Options.MaxConcurrency = 0; a.Effort = "high" })
	edited, err := sessionClient(f.accountFixture).EditQueuedInput(ctx, ownerRequest(f.identity, &pb.EditQueuedInputRequest{Mutation: acctMutation(f.change.Input, domain.NewID()), SessionId: f.change.Session.Id, Prompt: "latest input before claim"}))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.dispatchInitial(ctx, f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	accepted := f.refresh(t)
	state, _ = store.Decode[domain.Session](accepted)
	if state.InitialExecution == nil || state.InitialExecution.Configuration.Effort != "high" || state.Dispatch != domain.DispatchClaimed || state.PendingInputs != 1 || state.Problem != nil {
		t.Fatal("incorrect first immutable snapshot", state)
	}
	replay, err := sessionClient(f.accountFixture).CreateSession(ctx, ownerRequest(f.identity, f.request))
	if err != nil || !replay.Msg.Change.Replayed || replay.Msg.Change.ExecutionJob == nil || replay.Msg.Change.Session.Revision != accepted.Revision {
		t.Fatal("receipt did not join current execution", err)
	}
	var job domain.Job
	if domain.Decode(replay.Msg.Change.ExecutionJob.DocumentJson, &job) != nil {
		t.Fatal("invalid assignment")
	}
	var assigned domain.ExecutionJobInput
	if domain.Decode(job.Input, &assigned) != nil || assigned.Validate() != nil || assigned.Input.Prompt != "latest input before claim" || assigned.Input.Mode != domain.PlanMode || assigned.InputID != domain.ID(edited.Msg.Change.Input.Id) || assigned.ExecutionID != state.InitialExecution.ID || assigned.ConfigurationDigest != state.InitialExecution.ConfigurationDigest {
		t.Fatal("assignment differs from exact claimed input/configuration")
	}
	f.mutateAgent(t, func(a *domain.Agent) { a.Effort = "low" })
	if err = f.service.dispatchInitial(ctx, accepted); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("claimed work re-dispatched", err)
	}
	var after domain.Job
	if domain.Decode(currentCatalogResource(t, f.accountFixture, replay.Msg.Change.ExecutionJob).DocumentJson, &after) != nil || !bytes.Equal(job.Input, after.Input) {
		t.Fatal("later configuration rewrote immutable job")
	}
	candidates, more, err := f.service.Store.InitialExecutionCandidates(ctx, "", 1)
	if err != nil || more || len(candidates) != 0 {
		t.Fatal("claimed execution remains an automatic candidate")
	}
}

func TestInitialDispatchStopAndResumeAreSerialized(t *testing.T) {
	for _, action := range []pb.SessionAction{pb.SessionAction_SESSION_ACTION_STOP, pb.SessionAction_SESSION_ACTION_ARCHIVE} {
		t.Run(action.String(), func(t *testing.T) {
			f := newFirstDispatchFixture(t)
			ctx := context.Background()
			original := f.refresh(t)
			var wg sync.WaitGroup
			var dispatchErr, controlErr error
			var control *pb.SessionChange
			request := &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(original.ID), ExpectedRevision: original.Revision}, Action: action}
			wg.Add(2)
			go func() { defer wg.Done(); dispatchErr = f.service.dispatchInitial(ctx, original) }()
			go func() {
				defer wg.Done()
				r, e := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, request))
				controlErr = e
				if e == nil {
					control = r.Msg.Change
				}
			}()
			wg.Wait()
			if (dispatchErr == nil) == (controlErr == nil) {
				t.Fatalf("claim/control did not serialize: %v %v", dispatchErr, controlErr)
			}
			if dispatchErr == nil {
				current := f.refresh(t)
				request = &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(current.ID), ExpectedRevision: current.Revision}, Action: action}
				r, e := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, request))
				if e != nil {
					t.Fatal(e)
				}
				control = r.Msg.Change
				// The Worker stream may already own the immutable job. Cancellation must
				// remain targeted; neither race outcome permits an automatic replacement.
				state := sessionBody(t, control.Session)
				if state.Dispatch != domain.DispatchPaused {
					t.Fatal("control failed to pause claimed work")
				}
			} else {
				if domain.SafeError(dispatchErr).Code != domain.Conflict {
					t.Fatal(dispatchErr)
				}
				if action == pb.SessionAction_SESSION_ACTION_ARCHIVE {
					r, e := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(control.Session, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_RESTORE}))
					if e != nil {
						t.Fatal(e)
					}
					control = r.Msg.Change
				}
				if err := f.service.dispatchInitial(ctx, f.refresh(t)); domain.SafeError(err).Code != domain.Conflict {
					t.Fatal("paused/restored work auto-dispatched", err)
				}
				resume := &pb.ControlSessionRequest{Mutation: acctMutation(control.Session, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_RESUME}
				r, e := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, resume))
				if e != nil {
					t.Fatal(e)
				}
				if r.Msg.Change.ExecutionJob == nil {
					t.Fatal("explicit first Resume did not create assignment")
				}
				retry, e := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, resume))
				if e != nil || !retry.Msg.Change.Replayed || retry.Msg.Change.ExecutionJob.Id != r.Msg.Change.ExecutionJob.Id {
					t.Fatal("Resume receipt replaced assignment", e)
				}
			}
			candidates, _, e := f.service.Store.InitialExecutionCandidates(ctx, "", 50)
			if e != nil || len(candidates) != 0 {
				t.Fatal("paused or claimed work became automatic")
			}
		})
	}
}

func TestInitialDispatchRequiresCurrentEvidence(t *testing.T) {
	for _, failure := range []string{"worker-stale", "installation-unverified", "connection-changed", "validation-failed"} {
		t.Run(failure, func(t *testing.T) {
			f := newFirstDispatchFixture(t)
			ctx := context.Background()
			_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.invalidate-first-evidence", failure, func(tx *store.Tx) (any, error) {
				switch failure {
				case "worker-stale":
					instance, _, err := tx.WorkerInstance(f.selection.MachineID)
					if err != nil {
						return nil, err
					}
					return nil, tx.SetWorkerInstance(f.selection.MachineID, instance, time.Now().Add(-time.Hour))
				case "installation-unverified":
					r, m, err := activeMachine(tx, f.selection.MachineID)
					if err != nil {
						return nil, err
					}
					for i := range m.Installations {
						m.Installations[i].ProtocolVerified = false
					}
					return tx.Put(r.Kind, r.ID, r.Revision, "", "", m)
				default:
					r, a, err := accountFromTx(tx, domain.ID(f.account.Id), 0)
					if err != nil {
						return nil, err
					}
					if failure == "connection-changed" {
						a.Validation.ConnectionID = domain.NewID()
					} else {
						a.Validation.State = domain.ObservationFailed
					}
					return tx.Put(r.Kind, r.ID, r.Revision, "", "", a)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = f.service.dispatchInitial(ctx, f.refresh(t)); err == nil {
				t.Fatal("invalidated readiness dispatched")
			}
			state, _ := store.Decode[domain.Session](f.refresh(t))
			if state.InitialExecution != nil || state.ActiveExecutionID != "" || state.PendingInputs != 1 {
				t.Fatal("invalidated readiness consumed the input")
			}
		})
	}
}

func TestInitialResumeFailurePreservesPauseAndRemovalOrder(t *testing.T) {
	f := newFirstDispatchFixture(t)
	ctx := context.Background()
	current := currentCatalogResource(t, f.accountFixture, f.change.Session)
	stop := &pb.ControlSessionRequest{Mutation: acctMutation(current, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_STOP}
	stopped, err := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, stop))
	if err != nil {
		t.Fatal(err)
	}
	f.mutateAgent(t, func(a *domain.Agent) { a.Options.MaxConcurrency = 2 })
	resume := &pb.ControlSessionRequest{Mutation: acctMutation(stopped.Msg.Change.Session, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_RESUME}
	_, err = sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, resume))
	wantAccountCode(t, err, domain.Unsupported)
	unchanged := f.refresh(t)
	state, _ := store.Decode[domain.Session](unchanged)
	if unchanged.Revision != stopped.Msg.Change.Session.Revision || state.Dispatch != domain.DispatchPaused || state.InitialExecution != nil {
		t.Fatal("failed explicit Resume changed paused state")
	}
	f.mutateAgent(t, func(a *domain.Agent) { a.Options.MaxConcurrency = 0 })
	second, err := sessionClient(f.accountFixture).EnqueueInput(ctx, ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: f.change.Session.Id, DocumentJson: []byte(`{"prompt":"second original input","mode":"execute"}`)}))
	if err != nil {
		t.Fatal(err)
	}
	removed, err := sessionClient(f.accountFixture).RemoveQueuedInput(ctx, ownerRequest(f.identity, &pb.RemoveQueuedInputRequest{Mutation: acctMutation(f.change.Input, domain.NewID()), SessionId: f.change.Session.Id}))
	if err != nil {
		t.Fatal(err)
	}
	page, _, err := f.service.Store.InitialExecutionCandidates(ctx, "", 1)
	if err != nil || len(page) != 0 {
		t.Fatal("paused input was an automatic candidate")
	}
	accepted, err := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(removed.Msg.Change.Session, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_RESUME}))
	if err != nil {
		t.Fatal(err)
	}
	state = sessionBody(t, accepted.Msg.Change.Session)
	if state.InitialExecution == nil || state.InitialExecution.InputID != domain.ID(second.Msg.Change.Input.Id) || state.PendingInputs != 1 {
		t.Fatal("first Resume reused removed head or consumed queued capacity")
	}
	old, err := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, stop))
	if err != nil || !old.Msg.Change.Replayed || sessionBody(t, old.Msg.Change.Session).Dispatch != domain.DispatchClaimed || old.Msg.Change.ExecutionJob.Id != accepted.Msg.Change.ExecutionJob.Id {
		t.Fatal("old Stop replay canceled new ownership", err)
	}
}

func TestInitialDispatchCandidatePagesExcludeOtherLifecycleStates(t *testing.T) {
	f := newFirstDispatchFixture(t)
	ctx := context.Background()
	source := f.refresh(t)
	original, _ := store.Decode[domain.Session](source)
	eligible := []domain.ID{source.ID}
	for _, variant := range []string{"eligible", "paused", "archived", "recovery", "preparing", "no-input", "completed", "active"} {
		value := original
		switch variant {
		case "paused":
			value.Dispatch = domain.DispatchPaused
		case "archived":
			value.Archive = domain.Archived
		case "recovery":
			value.Recovery = domain.NeedsRecovery
		case "preparing":
			prep := *value.Preparation
			prep.State = domain.PreparationPending
			value.Preparation = &prep
		case "no-input":
			value.PendingInputs = 0
		case "completed":
			value.Outcome = domain.ExecutionSucceeded
		case "active":
			value.ActiveExecutionID = domain.NewID()
		}
		id := domain.NewID()
		_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.dispatch-candidate", id, func(tx *store.Tx) (any, error) { return tx.Put(domain.SessionKind, id, 0, id, "", value) })
		if err != nil {
			t.Fatal(err)
		}
		if variant == "eligible" {
			eligible = append(eligible, id)
		}
	}
	first, more, err := f.service.Store.InitialExecutionCandidates(ctx, "", 1)
	if err != nil || !more || len(first) != 1 || first[0].ID != eligible[0] {
		t.Fatal("wrong first bounded candidate page")
	}
	second, more, err := f.service.Store.InitialExecutionCandidates(ctx, first[0].ID, 1)
	if err != nil || more || len(second) != 1 || second[0].ID != eligible[1] {
		t.Fatal("unsafe lifecycle candidate or wrong continuation")
	}
}
