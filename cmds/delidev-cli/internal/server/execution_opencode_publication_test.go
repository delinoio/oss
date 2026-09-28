package server

import (
	"context"
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func newOpenCodePublicationFixture(t *testing.T, mode domain.SessionMode) *publicationFixture {
	t.Helper()
	f := publicationFixtureFromAuthority(t, newProfileAuthorityFixture(t, "http://127.0.0.1:1", domain.OpenCode, domain.OpenAIChat, func(input *domain.ExecutionJobInput) { input.Input.Mode = mode }, false))
	f.thread, f.turn = "ses_01960dcbe1faabcdefghijklmn", "msg_01960dcbe1faABCDEFGHIJKLMN"
	return f
}

func TestOpenCodePublicationRetainsOnlyOriginalBindings(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) {
			f := newOpenCodePublicationFixture(t, mode)
			for index, kind := range []domain.ExecutionEventKind{domain.ExecutionThreadBound, domain.ExecutionInputAccepted} {
				request := f.publish(t, f.event(kind, uint64(index+1)))
				if response, err := f.call(request); err != nil || !response.Msg.Replayed || response.Msg.AcknowledgedSequence != uint64(index+1) {
					t.Fatal("original OpenCode publication receipt was not retained exactly")
				}
			}
			before, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](before)
			if err != nil || session.Execution == nil || session.Execution.NativeThreadID != string(f.thread) || session.Execution.NativeTurnID != string(f.turn) || session.Execution.LastSequence != 2 || session.PendingInputs != 0 || session.PendingInputBytes != 0 || session.Execution.Observed.ValidateForInput(f.input.Configuration, mode) != nil {
				t.Fatal("OpenCode publication lost exact ownership/settings or duplicated queue accounting")
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
					t.Fatalf("binding support granted an unimplemented OpenCode event family: %v", err)
				}
			}
			after, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil || after.Revision != before.Revision {
				t.Fatal("rejected OpenCode events changed retained session state")
			}
		})
	}
}

func TestOpenCodeBindingPublicationRejectsForeignSettingsAndOwners(t *testing.T) {
	for _, test := range []struct {
		name  string
		event func(*domain.ExecutionEvent)
		rpc   func(*pb.PublishExecutionRequest)
	}{
		{name: "mode", event: func(e *domain.ExecutionEvent) { e.Observed.OpenCodeAgent = domain.OpenCodePlanAgent }},
		{name: "model", event: func(e *domain.ExecutionEvent) { e.Observed.Model = "foreign-model" }},
		{name: "sandbox", event: func(e *domain.ExecutionEvent) { e.Observed.Permission = domain.PermissionReadOnly }},
		{name: "thread-namespace", event: func(e *domain.ExecutionEvent) { e.NativeThreadID = string(domain.NewID()) }},
		{name: "execution", event: func(e *domain.ExecutionEvent) { e.ExecutionID = domain.NewID() }},
		{name: "sequence", event: func(e *domain.ExecutionEvent) { e.Sequence = 2 }},
		{name: "acceptance-before-binding", event: func(e *domain.ExecutionEvent) {
			e.Kind, e.Sequence, e.Observed, e.NativeTurnID = domain.ExecutionInputAccepted, 2, nil, "msg_01960dcbe1faABCDEFGHIJKLMN"
		}},
		{name: "revision", rpc: func(r *pb.PublishExecutionRequest) { r.Mutation.ExpectedRevision++ }},
		{name: "worker-instance", rpc: func(r *pb.PublishExecutionRequest) { r.InstanceId = string(domain.NewID()) }},
		{name: "machine", rpc: func(r *pb.PublishExecutionRequest) { r.MachineId = string(domain.NewID()) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
			event := f.event(domain.ExecutionThreadBound, 1)
			if test.event != nil {
				test.event(&event)
			}
			request := f.requestEvent(t, event)
			if test.rpc != nil {
				test.rpc(request)
			}
			if _, err := f.call(request); err == nil {
				t.Fatal("foreign OpenCode binding was accepted")
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

func TestLateOpenCodeAcceptanceCannotClearRecoveryOrAuthorizeRelay(t *testing.T) {
	f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
	f.registerGrant(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.stop-opencode", f.job, func(tx *store.Tx) (any, error) {
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

func TestOpenCodeTranscriptRetainsSeparatePartAndParentOwnership(t *testing.T) {
	f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	parent := "msg_01960dcbe1fbABCDEFGHIJKLMN"
	message := domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: "prt_01960dcbe1fbABCDEFGHIJKLMN", NativeParentID: parent, Role: domain.AssistantMessage, Text: "original"}
	start := f.event(domain.ExecutionMessageStarted, 3)
	start.Message = &message
	f.publish(t, start)
	for _, name := range []string{"parent", "part", "namespace", "phase", "user-owner"} {
		bad := message
		bad.Text = " addition"
		switch name {
		case "parent":
			bad.NativeParentID = "msg_01960dcbe1fcABCDEFGHIJKLMN"
		case "part":
			bad.NativeID = "prt_01960dcbe1fcABCDEFGHIJKLMN"
		case "namespace":
			bad.NativeParentID = string(f.thread)
		case "phase":
			phase := domain.FinalMessage
			bad.Phase = &phase
		case "user-owner":
			bad.NativeParentID = string(f.turn)
		}
		event := f.event(domain.ExecutionTextAppended, 4)
		event.Message = &bad
		if _, err := f.call(f.requestEvent(t, event)); err == nil {
			t.Fatal("changed native parent/part acquired transcript ownership", name)
		}
	}
	delta := message
	delta.Text = " addition"
	event := f.event(domain.ExecutionTextAppended, 4)
	event.Message = &delta
	request := f.publish(t, event)
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("text retry lost its exact original receipt")
	}
	message.Text += delta.Text
	event = f.event(domain.ExecutionMessageCompleted, 5)
	event.Message = &message
	f.publish(t, event)
	r, err := f.service.Store.Get(context.Background(), domain.MessageKind, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil || retained.NativeParentID != parent || retained.NativeID != message.NativeID || retained.Text != "original addition" || retained.State != domain.MessageComplete || retained.FirstSequence != 3 || retained.LastSequence != 5 {
		t.Fatal("transcript lost original part ownership or duplicated a delta")
	}
}

func TestOpenCodeReasoningRequiresOriginalPlainTextArtifactAndMonotonicCompletion(t *testing.T) {
	f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	update := domain.ExecutionArtifactUpdate{ID: domain.NewID(), NativeID: "prt_01960dcbe1fbABCDEFGHIJKLMN", NativeParentID: "msg_01960dcbe1fbABCDEFGHIJKLMN", Snapshot: &domain.ArtifactSnapshot{Kind: domain.ReasoningTextArtifact, Text: "original"}}
	event := f.event(domain.ExecutionArtifactStarted, 3)
	event.Artifact = &update
	f.publish(t, event)
	delta := update
	delta.Snapshot, delta.Delta = nil, &domain.ArtifactDelta{Kind: domain.ReasoningTextDelta, Text: " continuation"}
	event = f.event(domain.ExecutionArtifactDelta, 4)
	event.Artifact = &delta
	request := f.publish(t, event)
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("reasoning delta retry did not preserve its receipt")
	}
	for _, name := range []string{"replacement", "parent", "indexed-summary", "plan"} {
		bad := update
		bad.Snapshot = &domain.ArtifactSnapshot{Kind: domain.ReasoningTextArtifact, Text: "original continuation"}
		switch name {
		case "replacement":
			bad.Snapshot.Text = "replaced reasoning"
		case "parent":
			bad.NativeParentID = "msg_01960dcbe1fcABCDEFGHIJKLMN"
		case "indexed-summary":
			bad.Snapshot = &domain.ArtifactSnapshot{Kind: domain.ReasoningArtifact, Summary: []string{}, Content: []string{"original continuation"}}
		case "plan":
			bad.Snapshot.Kind = domain.PlanArtifact
		}
		event = f.event(domain.ExecutionArtifactCompleted, 5)
		event.Artifact = &bad
		if _, err := f.call(f.requestEvent(t, event)); err == nil {
			t.Fatal("original reasoning acquired foreign content/owner/semantics", name)
		}
	}
	update.Snapshot.Text = "original continuation"
	event = f.event(domain.ExecutionArtifactCompleted, 5)
	event.Artifact = &update
	f.publish(t, event)
	r, err := f.service.Store.Get(context.Background(), domain.MessageKind, update.ID)
	if err != nil {
		t.Fatal(err)
	}
	message, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil || message.NativeParentID != update.NativeParentID || message.Role != domain.ArtifactMessage || message.State != domain.MessageComplete || message.Artifact == nil || message.Artifact.Completed == nil || message.Artifact.Completed.Text != update.Snapshot.Text || len(message.Artifact.Deltas) != 1 {
		t.Fatal("original reasoning record lost exact stream and completion")
	}
}

func TestOpenCodeReadPublicationKeepsOriginalLifecycleAndCallOwnership(t *testing.T) {
	f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	raw, path, output, title, preview := "", "/fixture/original", "original read output", "original", "preview"
	end, truncated := uint64(200), false
	started := domain.ToolSnapshot{Kind: domain.OpenCodeReadTool, Status: domain.ToolPending, Read: &domain.OpenCodeReadObservation{CallID: "call-original", Raw: &raw}}
	running := domain.ToolSnapshot{Kind: domain.OpenCodeReadTool, Status: domain.ToolRunning, Read: &domain.OpenCodeReadObservation{CallID: "call-original", Input: domain.OpenCodeReadInput{FilePath: &path}, Timing: &domain.OpenCodeToolTiming{Start: 100}}}
	completed := domain.ToolSnapshot{Kind: domain.OpenCodeReadTool, Status: domain.ToolCompleted, Read: &domain.OpenCodeReadObservation{CallID: "call-original", Input: running.Read.Input, Timing: &domain.OpenCodeToolTiming{Start: 100, End: &end}, Title: &title, Output: &output, Metadata: &domain.OpenCodeReadMetadata{Preview: &preview, Truncated: &truncated, Loaded: []string{}}}}
	update := domain.ExecutionToolUpdate{ID: domain.NewID(), NativeID: "prt_01960dcbe1fbABCDEFGHIJKLMN", NativeParentID: "msg_01960dcbe1fbABCDEFGHIJKLMN", Snapshot: &started}
	makeEvent := func(kind domain.ExecutionEventKind, sequence uint64, value domain.ExecutionToolUpdate) domain.ExecutionEvent {
		e := f.event(kind, sequence)
		e.Tool = &value
		return e
	}
	request := f.publish(t, makeEvent(domain.ExecutionToolStarted, 3, update))
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("Read proposal replay duplicated publication")
	}
	duplicate := update
	duplicate.ID = domain.NewID()
	duplicate.NativeID = "prt_01960dcbe1fcABCDEFGHIJKLMN"
	if _, err := f.call(f.requestEvent(t, makeEvent(domain.ExecutionToolStarted, 4, duplicate))); err == nil {
		t.Fatal("one native call acquired a second part")
	}
	update.Snapshot = &completed
	if _, err := f.call(f.requestEvent(t, makeEvent(domain.ExecutionToolCompleted, 4, update))); err == nil {
		t.Fatal("pending proposal fabricated successful execution")
	}
	update.Snapshot = &running
	request = f.publish(t, makeEvent(domain.ExecutionToolUpdated, 4, update))
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("Read running replay duplicated publication")
	}
	for _, name := range []string{"parent", "call", "input", "start", "kind"} {
		bad := update
		body, _ := json.Marshal(completed)
		var snapshot domain.ToolSnapshot
		if domain.Decode(body, &snapshot) != nil {
			t.Fatal("invalid fixture")
		}
		bad.Snapshot = &snapshot
		switch name {
		case "parent":
			bad.NativeParentID = "msg_01960dcbe1fcABCDEFGHIJKLMN"
		case "call":
			snapshot.Read.CallID = "changed"
		case "input":
			value := "changed"
			snapshot.Read.Input.FilePath = &value
		case "start":
			snapshot.Read.Timing.Start++
		case "kind":
			snapshot.Kind = domain.CommandTool
		}
		if _, err := f.call(f.requestEvent(t, makeEvent(domain.ExecutionToolCompleted, 5, bad))); err == nil {
			t.Fatal("Read completion substituted original ownership", name)
		}
	}
	update.Snapshot = &completed
	f.publish(t, makeEvent(domain.ExecutionToolCompleted, 5, update))
	update.Snapshot = &running
	if _, err := f.call(f.requestEvent(t, makeEvent(domain.ExecutionToolUpdated, 6, update))); err == nil {
		t.Fatal("completed Read returned to running")
	}
	r, err := f.service.Store.Get(context.Background(), domain.MessageKind, update.ID)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil || value.State != domain.MessageComplete || value.NativeParentID != update.NativeParentID || value.Tool == nil || len(value.Tool.States) != 1 || value.Tool.States[0].Sequence != 4 || value.Tool.Started.Read.Raw == nil || value.Tool.Started.Read.Input.FilePath != nil || value.Tool.Completed == nil || *value.Tool.Completed.Read.Output != output {
		t.Fatal("retained Read lost original proposal, applied input or result")
	}
}

func TestOpenCodeShellPublicationKeepsOriginalLifecycleAndCallOwnership(t *testing.T) {
	f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	raw, path, output, title, preview := "", "/fixture/original", "original shell output", "original", "preview"
	end, truncated := uint64(200), false
	started := domain.ToolSnapshot{Kind: domain.OpenCodeShellTool, Status: domain.ToolPending, Shell: &domain.OpenCodeShellObservation{CallID: "call-original", Raw: &raw}}
	running := domain.ToolSnapshot{Kind: domain.OpenCodeShellTool, Status: domain.ToolRunning, Shell: &domain.OpenCodeShellObservation{CallID: "call-original", Input: domain.OpenCodeShellInput{Command: &path}, Timing: &domain.OpenCodeToolTiming{Start: 100}}}
	completed := domain.ToolSnapshot{Kind: domain.OpenCodeShellTool, Status: domain.ToolCompleted, Shell: &domain.OpenCodeShellObservation{CallID: "call-original", Input: running.Shell.Input, Timing: &domain.OpenCodeToolTiming{Start: 100, End: &end}, Title: &title, Output: &output, Metadata: &domain.OpenCodeShellMetadata{Output: &preview, Truncated: &truncated, ExitObserved: true}}}
	update := domain.ExecutionToolUpdate{ID: domain.NewID(), NativeID: "prt_01960dcbe1fbABCDEFGHIJKLMN", NativeParentID: "msg_01960dcbe1fbABCDEFGHIJKLMN", Snapshot: &started}
	makeEvent := func(kind domain.ExecutionEventKind, sequence uint64, value domain.ExecutionToolUpdate) domain.ExecutionEvent {
		e := f.event(kind, sequence)
		e.Tool = &value
		return e
	}
	request := f.publish(t, makeEvent(domain.ExecutionToolStarted, 3, update))
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("Shell proposal replay duplicated publication")
	}
	duplicate := update
	duplicate.ID = domain.NewID()
	duplicate.NativeID = "prt_01960dcbe1fcABCDEFGHIJKLMN"
	if _, err := f.call(f.requestEvent(t, makeEvent(domain.ExecutionToolStarted, 4, duplicate))); err == nil {
		t.Fatal("one native call acquired a second part")
	}
	duplicate.Snapshot = &domain.ToolSnapshot{Kind: domain.OpenCodeReadTool, Status: domain.ToolPending, Read: &domain.OpenCodeReadObservation{CallID: "call-original", Raw: &raw}}
	if _, err := f.call(f.requestEvent(t, makeEvent(domain.ExecutionToolStarted, 4, duplicate))); err == nil {
		t.Fatal("native call acquired a second part across tool kinds")
	}
	update.Snapshot = &completed
	if _, err := f.call(f.requestEvent(t, makeEvent(domain.ExecutionToolCompleted, 4, update))); err == nil {
		t.Fatal("pending proposal fabricated successful execution")
	}
	update.Snapshot = &running
	request = f.publish(t, makeEvent(domain.ExecutionToolUpdated, 4, update))
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("Shell running replay duplicated publication")
	}
	for _, name := range []string{"parent", "call", "input", "start", "kind"} {
		bad := update
		body, _ := json.Marshal(completed)
		var snapshot domain.ToolSnapshot
		if domain.Decode(body, &snapshot) != nil {
			t.Fatal("invalid fixture")
		}
		bad.Snapshot = &snapshot
		switch name {
		case "parent":
			bad.NativeParentID = "msg_01960dcbe1fcABCDEFGHIJKLMN"
		case "call":
			snapshot.Shell.CallID = "changed"
		case "input":
			value := "changed"
			snapshot.Shell.Input.Command = &value
		case "start":
			snapshot.Shell.Timing.Start++
		case "kind":
			snapshot.Kind = domain.CommandTool
		}
		if _, err := f.call(f.requestEvent(t, makeEvent(domain.ExecutionToolCompleted, 5, bad))); err == nil {
			t.Fatal("Shell completion substituted original ownership", name)
		}
	}
	update.Snapshot = &completed
	f.publish(t, makeEvent(domain.ExecutionToolCompleted, 5, update))
	update.Snapshot = &running
	if _, err := f.call(f.requestEvent(t, makeEvent(domain.ExecutionToolUpdated, 6, update))); err == nil {
		t.Fatal("completed Shell returned to running")
	}
	r, err := f.service.Store.Get(context.Background(), domain.MessageKind, update.ID)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil || value.State != domain.MessageComplete || value.NativeParentID != update.NativeParentID || value.Tool == nil || len(value.Tool.States) != 1 || value.Tool.States[0].Sequence != 4 || value.Tool.Started.Shell.Raw == nil || value.Tool.Started.Shell.Input.Command != nil || value.Tool.Completed == nil || *value.Tool.Completed.Shell.Output != output {
		t.Fatal("retained Shell lost original proposal, applied input or result")
	}
}
