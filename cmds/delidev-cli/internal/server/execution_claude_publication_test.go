package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func newClaudePublicationFixture(t *testing.T, mode domain.SessionMode) *publicationFixture {
	t.Helper()
	f := publicationFixtureFromAuthority(t, newProfileAuthorityFixture(t, "http://127.0.0.1:1", domain.ClaudeCode, domain.AnthropicMessages, func(input *domain.ExecutionJobInput) {
		input.Input.Mode = mode
		input.Installation.ResolvedPath = "/fixture/claude"
	}, false))
	f.thread, f.turn = f.input.SessionID, "123e4567-e89b-42d3-a456-426614174000"
	return f
}

func TestClaudePublicationRetainsOnlyOriginalBindings(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) {
			f := newClaudePublicationFixture(t, mode)
			for index, kind := range []domain.ExecutionEventKind{domain.ExecutionThreadBound, domain.ExecutionInputAccepted} {
				request := f.publish(t, f.event(kind, uint64(index+1)))
				if response, err := f.call(request); err != nil || !response.Msg.Replayed || response.Msg.AcknowledgedSequence != uint64(index+1) {
					t.Fatal("original Claude publication receipt was not retained exactly")
				}
			}
			before, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](before)
			if err != nil || session.Execution == nil || session.Execution.NativeThreadID != string(f.thread) || session.Execution.NativeTurnID != string(f.turn) || session.Execution.LastSequence != 2 || session.PendingInputs != 0 || session.PendingInputBytes != 0 || session.Execution.Observed.ValidateForInput(f.input.Configuration, mode) != nil {
				t.Fatal("Claude publication lost exact ownership/settings or duplicated queue accounting")
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
					// Binding alone lacks the independently required original
					// result/command/idle proof for the now supported terminal.
					expected = connect.CodeAborted
				}
				if _, err := f.call(f.requestEvent(t, event)); connect.CodeOf(err) != expected {
					t.Fatalf("binding support granted an unsupported or unproved Claude event: %v", err)
				}
			}
			after, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil || after.Revision != before.Revision {
				t.Fatal("rejected Claude events changed retained session state")
			}
		})
	}
}

func TestClaudeBindingPublicationRejectsForeignSettingsAndOwners(t *testing.T) {
	for _, test := range []struct {
		name  string
		event func(*domain.ExecutionEvent)
		rpc   func(*pb.PublishExecutionRequest)
	}{
		{name: "mode", event: func(e *domain.ExecutionEvent) { e.Observed.ClaudePermission = domain.ClaudePermissionPlan }},
		{name: "effort", event: func(e *domain.ExecutionEvent) { value := "invalid\x00effort"; e.Observed.Effort = &value }},
		{name: "model", event: func(e *domain.ExecutionEvent) { e.Observed.Model = "foreign-model" }},
		{name: "sandbox", event: func(e *domain.ExecutionEvent) { e.Observed.Permission = domain.PermissionReadOnly }},
		{name: "thread-namespace", event: func(e *domain.ExecutionEvent) { e.NativeThreadID = string(domain.NewID()) }},
		{name: "execution", event: func(e *domain.ExecutionEvent) { e.ExecutionID = domain.NewID() }},
		{name: "sequence", event: func(e *domain.ExecutionEvent) { e.Sequence = 2 }},
		{name: "acceptance-before-binding", event: func(e *domain.ExecutionEvent) {
			e.Kind, e.Sequence, e.Observed, e.NativeTurnID = domain.ExecutionInputAccepted, 2, nil, "123e4567-e89b-42d3-a456-426614174000"
		}},
		{name: "revision", rpc: func(r *pb.PublishExecutionRequest) { r.Mutation.ExpectedRevision++ }},
		{name: "worker-instance", rpc: func(r *pb.PublishExecutionRequest) { r.InstanceId = string(domain.NewID()) }},
		{name: "machine", rpc: func(r *pb.PublishExecutionRequest) { r.MachineId = string(domain.NewID()) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newClaudePublicationFixture(t, domain.ExecuteMode)
			event := f.event(domain.ExecutionThreadBound, 1)
			if test.event != nil {
				test.event(&event)
			}
			request := f.requestEvent(t, event)
			if test.rpc != nil {
				test.rpc(request)
			}
			_, err := f.call(request)
			if test.name == "worker-instance" || test.name == "machine" {
				if err != nil {
					t.Fatal("ownership metadata blocked publication", err)
				}
				return
			}
			if err == nil {
				t.Fatal("foreign Claude binding was accepted")
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

func TestLateClaudeAcceptanceCannotClearRecoveryOrAuthorizeRelay(t *testing.T) {
	f := newClaudePublicationFixture(t, domain.ExecuteMode)
	f.registerGrant(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.stop-claude", f.job, func(tx *store.Tx) (any, error) {
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
