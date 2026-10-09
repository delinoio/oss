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
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/testgit"
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
	workerIdentity          security.Identity
	workerClient            delidevv1connect.WorkerServiceClient
	workerInstance          string
	workerStream            *connect.ServerStreamForClient[pb.WatchWorkResponse]
	workerRoot              string
	workerDevice            domain.ID
}

// Public configuration/account/session APIs and authenticated Worker reports
// establish readiness here. The reported installation is a protocol fixture;
// ordinary tests never execute an installed harness or request inference.
func newFirstDispatchFixture(t *testing.T) *firstDispatchFixture {
	return newFirstDispatchFixtureForHarness(t, domain.Codex)
}

func newFirstDispatchFixtureForHarness(t *testing.T, harness domain.Harness, modes ...domain.SessionMode) *firstDispatchFixture {
	t.Helper()
	mode := domain.PlanMode
	if len(modes) > 1 {
		t.Fatal("fixture accepts one input mode")
	}
	if len(modes) == 1 {
		mode = modes[0]
	}
	return newFirstDispatchFixtureProfile(t, harness, mode, "/fixture/"+string(harness), "")
}

func newFirstDispatchFixtureProfile(t *testing.T, harness domain.Harness, mode domain.SessionMode, executable, upstreamURL string, nativeModels ...string) *firstDispatchFixture {
	t.Helper()
	nativeModel := "fixture-model"
	if len(nativeModels) == 1 {
		nativeModel = nativeModels[0]
	}
	return newFirstDispatchFixtureWorkspaceProfile(t, harness, mode, executable, upstreamURL, nativeModel, domain.GeneralChat)
}

func newFirstDispatchFixtureWorkspaceProfile(t *testing.T, harness domain.Harness, mode domain.SessionMode, executable, upstreamURL, nativeModel string, workspaceType domain.WorkspaceType, projects ...openCodeProjectFixtureKind) *firstDispatchFixture {
	t.Helper()
	protocol, permission, version := domain.OpenAIResponses, domain.PermissionReadOnly, domain.CodexProtocolVersion
	if harness == domain.OpenCode {
		protocol, permission, version = domain.OpenAIChat, domain.PermissionDefault, domain.OpenCodeProtocolVersion
	} else if harness == domain.ClaudeCode {
		protocol, permission, version = domain.AnthropicMessages, domain.PermissionDefault, domain.ClaudeProtocolVersion
	} else if harness == domain.GrokBuild {
		protocol, permission, version = domain.OpenAIChat, domain.PermissionDefault, domain.GrokProtocolVersion
	}
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
	f := &firstDispatchFixture{accountFixture: base, service: service, machine: paired.Machine, workerDevice: domain.ID(paired.Device.Id)}
	if upstreamURL == "" {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/models" || r.Header.Get("Authorization") != "" {
				t.Error("dispatch fixture performed unexpected provider work")
				http.Error(w, "unsupported", 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": nativeModel, "object": "model"}}})
		}))
		t.Cleanup(upstream.Close)
		upstreamURL = upstream.URL
	}
	provider := base.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "Fixture", Endpoint: upstreamURL, Protocol: protocol, Authentication: domain.KeylessAuth})
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
	modelValue := domain.Model{Name: "Fixture", NativeID: nativeModel, ProviderID: domain.ID(provider.Id), Harnesses: []domain.Harness{harness}, MetadataSource: domain.UserDeclared}
	if harness == domain.GrokBuild {
		limit := uint64(48000)
		modelValue.ContextLimit = &limit
	}
	model := base.save(pb.EntityKind_ENTITY_KIND_MODEL, modelValue)
	routing := domain.RoundRobin
	f.agent = base.save(pb.EntityKind_ENTITY_KIND_AGENT, domain.Agent{Name: "Fixture", Harness: harness, ModelID: domain.ID(model.Id), Accounts: []domain.WeightedAccount{{ID: domain.ID(account.Id), Weight: 1}}, Options: domain.AgentOptions{Permission: permission}, Routing: &routing})
	f.selection = domain.CreateSession{Name: "Fixture", AgentID: domain.ID(f.agent.Id), MachineID: domain.ID(f.machine.Id), Workspace: domain.GeneralChat, Prompt: "first retained input", Mode: mode, Source: domain.ExternalCLISession}
	// This primary stream spans discovery, native workspace preparation and the
	// later test operations. Ten seconds can expire during Windows setup before
	// a freshly bounded workspace read starts. Keep a separate aggregate fixture
	// lifetime; the production read and execution admission deadlines still apply.
	ctx, client, instance, stream := workspaceStreamWithLifetime(t, base, identity, domain.ID(f.machine.Id), time.Minute)
	f.workerIdentity, f.workerClient, f.workerInstance, f.workerStream = identity, client, instance, stream
	if harness == domain.OpenCode {
		if _, err := client.AttachWorker(ctx, ownerRequest(identity, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: f.machine.Id, InstanceId: instance, Version: rpc.Version, Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_FOREGROUND_SUBAGENTS_V1, pb.WorkerCapability_WORKER_CAPABILITY_REMOTE_WORKSPACE_CLONE_V1, pb.WorkerCapability_WORKER_CAPABILITY_EXECUTION_STARTUP_V1}})); err != nil {
			t.Fatal(err)
		}
	}
	f.machine = currentCatalogResource(t, base, f.machine)
	selections, _ := json.Marshal(domain.ExecutableSelections{Executables: []domain.ExecutableSelection{{Harness: harness, Path: executable}}})
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
		if v.Harness == harness {
			v.State = domain.InstallationDetected
			v.ResolvedPath = executable
			v.Version = version
			v.ProtocolVerified = true
			v.Protocol = &domain.ProtocolObservation{Protocol: domain.ProtocolFor(harness), State: domain.ProtocolVerified}
		}
	}
	raw, _ := json.Marshal(observed)
	if _, err = client.ReportWork(ctx, ownerRequest(identity, &pb.ReportWorkRequest{Mutation: acctMutation(discovery, domain.NewID()), MachineId: f.machine.Id, InstanceId: instance, OutputJson: raw})); err != nil {
		t.Fatal(err)
	}
	f.machine = currentCatalogResource(t, base, f.machine)
	if workspaceType != domain.GeneralChat {
		prepareOpenCodeProjectFixture(t, f, workspaceType, projects...)
	}
	if workspaceType == domain.Local {
		raw, _ := json.Marshal(f.selection)
		f.request = &pb.CreateSessionRequest{RequestId: string(domain.NewID()), DocumentJson: raw, LocalWorkerToken: identity.Token}
		response, err := sessionClient(base).CreateSession(ctx, ownerRequest(base.identity, f.request))
		if err != nil {
			t.Fatal(err)
		}
		f.change = response.Msg.Change
	} else {
		f.request, f.change = createSessionFixture(t, base, f.selection)
	}
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
	if workspaceType == domain.Worktree {
		sources := map[string]string{}
		for _, spec := range request.Repositories {
			row, err := service.Store.Get(ctx, domain.RepositoryKind, spec.ID)
			if err != nil {
				t.Fatal(err)
			}
			repo, err := store.Decode[domain.Repository](row)
			if err != nil {
				t.Fatal(err)
			}
			sources[repo.RemoteURL] = repo.Checkouts[0].Path
		}
		manager.Git.Executable = testgit.Executable(t, sources)
	}
	manifest, err := manager.Prepare(ctx, request)
	f.workerRoot = manager.Root
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
	f.mutateAgent(t, func(a *domain.Agent) { a.Options.ApprovalReviewModel = "unsupported" })
	before := f.refresh(t)
	if err := f.service.dispatchExecution(ctx, before); domain.SafeError(err).Code != domain.Unsupported || !strings.Contains(domain.SafeError(err).Message, "approval_review_model") {
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
	_ = f.service.dispatchExecution(ctx, blocked)
	if f.refresh(t).Revision != blocked.Revision {
		t.Fatal("unchanged block created a revision loop")
	}
	f.mutateAgent(t, func(a *domain.Agent) {
		a.Options.ApprovalReviewModel = ""
		a.Effort = "high"
		a.Options.ServiceTier = "fast"
	})
	edited, err := sessionClient(f.accountFixture).EditQueuedInput(ctx, ownerRequest(f.identity, &pb.EditQueuedInputRequest{Mutation: acctMutation(f.change.Input, domain.NewID()), SessionId: f.change.Session.Id, Prompt: "latest input before claim"}))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.dispatchExecution(ctx, f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	accepted := f.refresh(t)
	state, _ = store.Decode[domain.Session](accepted)
	if state.InitialExecution == nil || state.InitialExecution.Configuration.Effort != "high" || state.InitialExecution.Configuration.Options.ServiceTier != "fast" || state.Dispatch != domain.DispatchClaimed || state.PendingInputs != 1 || state.Problem != nil {
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
	f.mutateAgent(t, func(a *domain.Agent) { a.Effort = "low"; a.Options.ServiceTier = "" })
	if err = f.service.dispatchExecution(ctx, accepted); domain.SafeError(err).Code != domain.Conflict {
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

func TestInitialDispatchOffProviderPreservesQueuedInputAndRouting(t *testing.T) {
	f := newFirstDispatchFixture(t)
	ctx := context.Background()
	if err := disableFirstDispatchProvider(f); err != nil {
		t.Fatal(err)
	}
	before := f.refresh(t)
	err := f.service.dispatchExecution(ctx, before)
	if domain.SafeError(err).Code != domain.ProviderDisabled {
		t.Fatalf("initial dispatch did not report provider disabled: %v", err)
	}
	state, err := store.Decode[domain.Session](f.refresh(t))
	if err != nil || state.InitialExecution != nil || state.ActiveExecutionID != "" || state.PendingInputs != 1 {
		t.Fatalf("blocked provider consumed or replaced session ownership: %+v %v", state, err)
	}
	queued, err := f.service.Store.Get(ctx, domain.QueueKind, domain.ID(f.change.Input.Id))
	if err != nil {
		t.Fatal(err)
	}
	input, err := store.Decode[domain.QueuedInput](queued)
	if err != nil || input.Delivery != domain.InputQueued || input.ExecutionID != "" || input.Prompt != f.selection.Prompt {
		t.Fatalf("blocked provider consumed or rewrote the queued input: %+v %v", input, err)
	}
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		routing, _, err := tx.Routing(f.selection.AgentID)
		if err == nil && routing.ID != "" {
			t.Error("blocked provider advanced account routing")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func disableFirstDispatchProvider(f *firstDispatchFixture) error {
	var agent domain.Agent
	if err := domain.Decode(f.agent.DocumentJson, &agent); err != nil {
		return err
	}
	modelRecord, err := f.service.Store.Get(context.Background(), domain.ModelKind, agent.ModelID)
	if err != nil {
		return err
	}
	model, err := store.Decode[domain.Model](modelRecord)
	if err != nil {
		return err
	}
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.disable-provider", model.ProviderID, func(tx *store.Tx) (any, error) {
		record, err := tx.Get(domain.ProviderKind, model.ProviderID)
		if err != nil {
			return nil, err
		}
		provider, err := store.Decode[domain.Provider](record)
		if err != nil {
			return nil, err
		}
		provider.SetEnabled(false)
		return tx.Put(domain.ProviderKind, model.ProviderID, record.Revision, "", "", provider)
	})
	return err
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
			go func() { defer wg.Done(); dispatchErr = f.service.dispatchExecution(ctx, original) }()
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
				if err := f.service.dispatchExecution(ctx, f.refresh(t)); domain.SafeError(err).Code != domain.Conflict {
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
	for _, failure := range []string{"worker-stale", "old-worker", "connection-changed", "validation-failed"} {
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
				case "old-worker":
					r, m, err := activeMachine(tx, f.selection.MachineID)
					if err != nil {
						return nil, err
					}
					m.WorkerCapabilities = nil
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
			if err = f.service.dispatchExecution(ctx, f.refresh(t)); err == nil {
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
	f.mutateAgent(t, func(a *domain.Agent) { a.Options.ApprovalReviewModel = "unsupported" })
	resume := &pb.ControlSessionRequest{Mutation: acctMutation(stopped.Msg.Change.Session, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_RESUME}
	_, err = sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, resume))
	wantAccountCode(t, err, domain.Unsupported)
	unchanged := f.refresh(t)
	state, _ := store.Decode[domain.Session](unchanged)
	if unchanged.Revision != stopped.Msg.Change.Session.Revision || state.Dispatch != domain.DispatchPaused || state.InitialExecution != nil || !state.AutomaticRemediationStopped {
		t.Fatal("failed explicit Resume changed paused state")
	}
	f.mutateAgent(t, func(a *domain.Agent) { a.Options.ApprovalReviewModel = "" })
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
	if state.InitialExecution == nil || state.InitialExecution.InputID != domain.ID(second.Msg.Change.Input.Id) || state.PendingInputs != 1 || state.AutomaticRemediationStopped {
		t.Fatal("first Resume reused removed head or consumed queued capacity")
	}
	old, err := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, stop))
	if err != nil || !old.Msg.Change.Replayed || sessionBody(t, old.Msg.Change.Session).Dispatch != domain.DispatchClaimed || sessionBody(t, old.Msg.Change.Session).AutomaticRemediationStopped || old.Msg.Change.ExecutionJob.Id != accepted.Msg.Change.ExecutionJob.Id {
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
