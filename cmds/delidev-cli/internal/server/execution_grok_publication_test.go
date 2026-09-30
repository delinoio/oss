package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func newGrokPublicationFixture(t *testing.T, mode domain.SessionMode) *publicationFixture {
	t.Helper()
	f := publicationFixtureFromAuthority(t, newProfileAuthorityFixture(t, "http://127.0.0.1:1", domain.GrokBuild, domain.OpenAIChat, func(input *domain.ExecutionJobInput) { input.Input.Mode = mode }, false))
	f.thread, f.turn = domain.NewID(), "e5833c4a-d764-4428-8bd8-6c2968a34b1b"
	return f
}

func TestGrokPublicationRetainsOnlyOriginalBindings(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) {
			f := newGrokPublicationFixture(t, mode)
			for index, kind := range []domain.ExecutionEventKind{domain.ExecutionThreadBound, domain.ExecutionInputAccepted} {
				request := f.publish(t, f.event(kind, uint64(index+1)))
				if response, err := f.call(request); err != nil || !response.Msg.Replayed || response.Msg.AcknowledgedSequence != uint64(index+1) {
					t.Fatal("original Grok publication receipt was not retained exactly")
				}
			}
			before, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](before)
			if err != nil || session.Execution == nil || session.Execution.NativeThreadID != string(f.thread) || session.Execution.NativeTurnID != string(f.turn) || session.Execution.LastSequence != 2 || session.PendingInputs != 0 || session.PendingInputBytes != 0 || session.Execution.Observed.ValidateForInput(f.input.Configuration, mode) != nil {
				t.Fatal("Grok publication lost exact ownership/settings or duplicated queue accounting")
			}
			for _, kind := range []domain.ExecutionEventKind{domain.ExecutionNoticeObserved, domain.ExecutionTurnFinished} {
				event := f.event(kind, 3)
				if kind == domain.ExecutionNoticeObserved {
					event.Notice = domain.NativeWarning
				} else {
					event.Outcome = domain.ExecutionSucceeded
				}
				if err := event.Validate(); err != nil {
					t.Fatal(err)
				}
				expected := connect.CodeUnimplemented
				if kind == domain.ExecutionTurnFinished {
					expected = connect.CodeAborted
				}

				if _, err := f.call(f.requestEvent(t, event)); connect.CodeOf(err) != expected {
					t.Fatalf("binding support granted an unimplemented Grok event family: %v", err)
				}
			}
			after, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil || after.Revision != before.Revision {
				t.Fatal("rejected Grok events changed retained session state")
			}
		})
	}
}

func TestGrokBindingPublicationRejectsForeignSettingsAndOwners(t *testing.T) {
	for _, test := range []struct {
		name  string
		event func(*domain.ExecutionEvent)
		rpc   func(*pb.PublishExecutionRequest)
	}{
		{name: "mode", event: func(e *domain.ExecutionEvent) { e.Observed.GrokMode = domain.GrokPlanMode }},
		{name: "model", event: func(e *domain.ExecutionEvent) { e.Observed.Model = "foreign-model" }},
		{name: "sandbox", event: func(e *domain.ExecutionEvent) { e.Observed.Permission = domain.PermissionReadOnly }},
		{name: "thread-namespace", event: func(e *domain.ExecutionEvent) { e.NativeThreadID = "ses_01960dcbe1faabcdefghijklmn" }},
		{name: "execution", event: func(e *domain.ExecutionEvent) { e.ExecutionID = domain.NewID() }},
		{name: "sequence", event: func(e *domain.ExecutionEvent) { e.Sequence = 2 }},
		{name: "acceptance-before-binding", event: func(e *domain.ExecutionEvent) {
			e.Kind, e.Sequence, e.Observed, e.NativeTurnID = domain.ExecutionInputAccepted, 2, nil, "e5833c4a-d764-4428-8bd8-6c2968a34b1b"
		}},
		{name: "revision", rpc: func(r *pb.PublishExecutionRequest) { r.Mutation.ExpectedRevision++ }},
		{name: "worker-instance", rpc: func(r *pb.PublishExecutionRequest) { r.InstanceId = string(domain.NewID()) }},
		{name: "machine", rpc: func(r *pb.PublishExecutionRequest) { r.MachineId = string(domain.NewID()) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGrokPublicationFixture(t, domain.ExecuteMode)
			event := f.event(domain.ExecutionThreadBound, 1)
			if test.event != nil {
				test.event(&event)
			}
			request := f.requestEvent(t, event)
			if test.rpc != nil {
				test.rpc(request)
			}
			if _, err := f.call(request); err == nil {
				t.Fatal("foreign Grok binding was accepted")
			}
			r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](r)
			if err != nil || session.Execution != nil || session.PendingInputs != 1 || session.PendingInputBytes != uint64(len(f.input.Input.Prompt)) {
				t.Fatal("rejected binding partially committed session state")
			}
		})
	}
}

func TestLateGrokAcceptanceCannotClearRecoveryOrAuthorizeRelay(t *testing.T) {
	f := newGrokPublicationFixture(t, domain.ExecuteMode)
	f.registerGrant(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.stop-grok", f.job, func(tx *store.Tx) (any, error) {
		r, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		session.Recovery, session.Dispatch, session.Outcome = domain.NeedsRecovery, domain.DispatchPaused, domain.ExecutionStopped
		if err := tx.RequestJobCancellation(f.job); err != nil {
			return nil, err
		}
		return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, session)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](r)
	if err != nil || session.Recovery != domain.NeedsRecovery || session.Dispatch != domain.DispatchPaused || session.Outcome != domain.ExecutionStopped || session.Execution == nil || session.Execution.LastSequence != 2 || session.PendingInputs != 0 {
		t.Fatal("late original acceptance erased Stop/recovery or lost its own fact")
	}
	if lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token); err == nil {
		lease.Release()
		t.Fatal("late original acceptance restored revoked execution authority")
	}
}

func TestGrokRegistrationRejectsUnimplementedProfiles(t *testing.T) {
	for _, test := range []struct {
		name      string
		protocol  domain.APIProtocol
		configure func(*domain.ExecutionJobInput)
	}{
		{name: "responses", protocol: domain.OpenAIResponses},
		{name: "messages", protocol: domain.AnthropicMessages},
		{name: "version", configure: func(i *domain.ExecutionJobInput) { i.Installation.Version = "1.0.42" }},
		{name: "protocol-discovery", configure: func(i *domain.ExecutionJobInput) { i.Installation.ProtocolVerified = false }},
		{name: "effort", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Effort = "high" }},
		{name: "subagent-model", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.SubagentModel = "other-model" }},
		{name: "subagent-effort", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.SubagentEffort = "high" }},
		{name: "concurrency", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.MaxConcurrency = 2 }},
		{name: "review-model", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.ApprovalReviewModel = "other-model" }},
		{name: "service-tier", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.ServiceTier = "fast" }},
		{name: "plan-instructions", configure: func(i *domain.ExecutionJobInput) {
			i.Input.Mode = domain.PlanMode
			i.Configuration.Templates = []domain.AppliedTemplate{{ID: domain.NewID(), Revision: 1, Contents: "Original additive instruction"}}
			i.Configuration.Instructions = "Original additive instruction"
		}},
		{name: "permission", configure: func(i *domain.ExecutionJobInput) { i.Configuration.Options.Permission = domain.PermissionFullAccess }},
	} {
		t.Run(test.name, func(t *testing.T) {
			protocol := test.protocol
			if protocol == "" {
				protocol = domain.OpenAIChat
			}
			f := newProfileAuthorityFixture(t, "http://127.0.0.1:1", domain.GrokBuild, protocol, func(input *domain.ExecutionJobInput) {
				if test.configure != nil {
					test.configure(input)
					// Valid but unsupported selections still have an intact digest.
					if digest, err := input.Configuration.Digest(); err == nil {
						input.ConfigurationDigest = digest
					}
				}
			}, false)
			request := connect.NewRequest(f.register)
			request.Header().Set("Authorization", "Bearer "+f.workerToken)
			if _, err := f.client.RegisterExecution(context.Background(), request); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("unsupported profile registered an execution credential: %v", err)
			}
			if lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token); err == nil {
				lease.Release()
				t.Fatal("rejected registration retained relay authority")
			}
		})
	}
}
