package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func toolCommand() *domain.ToolSnapshot {
	return &domain.ToolSnapshot{Kind: domain.CommandTool, Status: domain.ToolRunning, Command: &domain.CommandObservation{Command: "printf fixture", Cwd: "/private/workspace", Source: domain.AgentCommand, Actions: []domain.CommandAction{}}}
}

func (f *publicationFixture) toolEvent(kind domain.ExecutionEventKind, sequence uint64, id domain.ID, native string) domain.ExecutionEvent {
	e := f.event(kind, sequence)
	e.Tool = &domain.ExecutionToolUpdate{ID: id, NativeID: native}
	return e
}

func bindNativeMapper(t *testing.T, f *publicationFixture, cfg worker.PublicationConfig) (*worker.ExecutionPublisher, *worker.CodexEventPublisher) {
	t.Helper()
	publisher, err := worker.OpenExecutionPublisher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = publisher.Close() })
	mapper := worker.NewCodexEventPublisher(publisher)
	err = mapper.BindThread(context.Background(), codex.ThreadResult{RequestID: f.input.ThreadRequestID, Thread: &codex.Thread{ID: f.thread}, Effective: &codex.EffectiveSettings{Model: f.input.Configuration.NativeModel, Provider: codex.APIProvider, Sandbox: codex.Sandbox{Type: codex.ReadOnly}, ApprovalPolicy: codex.ApprovalOnRequest, ApprovalsReviewer: "user"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mapper.AcceptInput(context.Background(), codex.TurnResult{RequestID: f.input.TurnRequestID, InputID: f.input.InputID, TurnID: f.turn}); err != nil {
		t.Fatal(err)
	}
	return publisher, mapper
}

func publishNativeEvent(t *testing.T, f *publicationFixture, mapper *worker.CodexEventPublisher, e codex.Event) {
	t.Helper()
	e.ThreadID, e.TurnID, e.Correlated = f.thread, f.turn, true
	if handled, err := mapper.PublishCore(context.Background(), e); !handled || err != nil {
		t.Fatalf("native tool event was not published: %s: %v", e.Kind, err)
	}
}

func TestExecutionToolsPreserveStreamAggregatePatchesAndNativeOutcome(t *testing.T) {
	f := newPublicationFixture(t)
	_, mapper := bindNativeMapper(t, f, publicationWorkerConfig(t, f))
	name, path, process, plugin, script := "fixture", "/private/workspace/file", "native-process", "native-plugin", "/private/workspace/script"
	command := &codex.CommandExecution{Command: "cat file", Cwd: "/private/workspace", Source: codex.AgentCommand, Actions: []codex.CommandAction{{Kind: codex.ReadCommandAction, Command: "cat file", Name: &name, Path: &path}}, PluginID: &plugin, ScriptPath: &script}
	tool := &codex.Tool{ID: "command-item", Kind: codex.CommandTool, Status: codex.ToolRunning, Command: command}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolStartedEvent, ItemID: tool.ID, Tool: tool})
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolOutputEvent, ItemID: tool.ID, TextDelta: "complete streamed output\n"})
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolInputEvent, ItemID: tool.ID, ToolInput: &codex.ToolInput{ProcessID: process, Text: ""}})
	aggregate, exit, duration := "[native truncated output]", int32(7), int64(92)
	command.AggregatedOutput, command.ExitCode, command.DurationMS, command.ProcessID = &aggregate, &exit, &duration, &process
	tool.Status = codex.ToolFailed
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolCompletedEvent, ItemID: tool.ID, Tool: tool})
	patch := &codex.Tool{ID: "patch-item", Kind: codex.PatchTool, Status: codex.ToolRunning, Changes: []codex.FileChange{}}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolStartedEvent, ItemID: patch.ID, Tool: patch})
	move := "/private/workspace/new"
	patch.Status, patch.Changes = "", []codex.FileChange{{Path: path, Diff: "-old\n+new\n", Kind: codex.UpdatedFile, MovePath: &move}}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolPatchEvent, ItemID: patch.ID, Tool: patch})
	patch.Status, patch.Changes[0].Diff = codex.ToolDeclined, "-old\n+final\n"
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolCompletedEvent, ItemID: patch.ID, Tool: patch})
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.TurnCompletedEvent, Turn: &codex.Turn{ID: f.turn, Status: codex.TurnCompleted}})
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("missing typed tool transcript: %v", err)
	}
	for _, r := range rows {
		m, err := store.Decode[domain.ExecutionMessage](r)
		if err != nil || m.ExecutionID != f.input.ExecutionID || m.NativeThreadID != string(f.thread) || m.NativeTurnID != string(f.turn) || m.Role != domain.ToolMessage || m.State != domain.MessageComplete || m.Text != "" || m.Tool == nil || m.Tool.Completed == nil {
			t.Fatal("tool lost provenance or became assistant text")
		}
		switch m.Tool.Started.Kind {
		case domain.CommandTool:
			v := m.Tool
			if m.FirstSequence != 3 || m.LastSequence != 6 || (v.Output == nil || *v.Output != "complete streamed output\n") || v.Started.Command.AggregatedOutput != nil || v.Started.Command.ExitCode != nil || v.Started.Command.DurationMS != nil || v.Started.Command.ProcessID != nil || *v.Completed.Command.AggregatedOutput != aggregate || *v.Completed.Command.ExitCode != 7 || *v.Completed.Command.DurationMS != 92 || *v.Completed.Command.PluginID != plugin || *v.Completed.Command.ScriptPath != script || v.Completed.Status != domain.ToolFailed || len(v.Inputs) != 1 || v.Inputs[0].Sequence != 5 || v.Inputs[0].Input.Text != "" || v.Inputs[0].Input.ProcessID != process || *v.Started.Command.Actions[0].Path != path {
				t.Fatal("command observations were merged, overwritten or fabricated")
			}
		case domain.PatchTool:
			v := m.Tool
			if m.FirstSequence != 7 || m.LastSequence != 9 || v.Started.Changes == nil || len(v.Started.Changes) != 0 || len(v.Patches) != 1 || v.Patches[0].Sequence != 8 || v.Patches[0].Changes[0].Diff != "-old\n+new\n" || v.Completed.Changes[0].Diff != "-old\n+final\n" || *v.Completed.Changes[0].MovePath != move || v.Completed.Status != domain.ToolDeclined {
				t.Fatal("patch history lost its exact revision observations")
			}
		default:
			t.Fatal("unknown tool kind")
		}
	}
	r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Decode[domain.Session](r)
	if err != nil || s.Outcome != domain.ExecutionSucceeded || s.Execution.Outcome != domain.ExecutionSucceeded || s.Execution.LastSequence != 10 || s.Execution.CleanupVerified {
		t.Fatal("tool failure replaced whole-turn outcome or fabricated cleanup")
	}
}

func TestExecutionToolsRejectScopeSubstitutionAndRequireCompletion(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	id := domain.NewID()
	e := f.toolEvent(domain.ExecutionToolStarted, 3, id, "owned-tool")
	e.Tool.Snapshot = toolCommand()
	f.publish(t, e)
	for _, bad := range []string{"duplicate-item", "message-collision", "wrong-turn", "wrong-native", "changed-command", "changed-source", "changed-kind", "missing-terminal", "mixed-payload", "patch-on-command"} {
		t.Run(bad, func(t *testing.T) {
			e := f.toolEvent(domain.ExecutionToolCompleted, 4, id, "owned-tool")
			e.Tool.Snapshot = toolCommand()
			e.Tool.Snapshot.Status = domain.ToolCompleted
			switch bad {
			case "duplicate-item":
				e.Kind, e.Tool.ID, e.Tool.Snapshot.Status = domain.ExecutionToolStarted, domain.NewID(), domain.ToolRunning
			case "message-collision":
				e.Kind, e.Tool = domain.ExecutionMessageStarted, nil
				e.Message = &domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: "owned-tool", Role: domain.AssistantMessage}
			case "wrong-turn":
				e.NativeTurnID = string(domain.NewID())
			case "wrong-native":
				e.Tool.NativeID = "foreign-tool"
			case "changed-command":
				e.Tool.Snapshot.Command.Command = "different"
			case "changed-source":
				e.Tool.Snapshot.Command.Source = domain.UserShellCommand
			case "changed-kind":
				e.Tool.Snapshot = &domain.ToolSnapshot{Kind: domain.PatchTool, Status: domain.ToolCompleted, Changes: []domain.FileChangeObservation{}}
			case "missing-terminal":
				e.Kind, e.Tool, e.Outcome = domain.ExecutionTurnFinished, nil, domain.ExecutionSucceeded
			case "mixed-payload":
				delta := "unowned"
				e.Tool.Delta = &delta
			case "patch-on-command":
				changes := []domain.FileChangeObservation{}
				e.Kind, e.Tool.Snapshot, e.Tool.Changes = domain.ExecutionToolPatch, nil, &changes
			}
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("invalid tool publication accepted")
			}
		})
	}
	e = f.toolEvent(domain.ExecutionToolCompleted, 4, id, "owned-tool")
	e.Tool.Snapshot = toolCommand()
	e.Tool.Snapshot.Status = domain.ToolDeclined
	req := f.publish(t, e)
	if r, err := f.call(req); err != nil || !r.Msg.Replayed {
		t.Fatalf("tool completion replay failed: %v", err)
	}
	e = f.toolEvent(domain.ExecutionToolOutput, 5, id, "owned-tool")
	delta := "late"
	e.Tool.Delta = &delta
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("late tool output changed completed evidence")
	}
	e = f.event(domain.ExecutionTurnFinished, 5)
	e.Outcome = domain.ExecutionSucceeded
	f.publish(t, e)
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal("failed publications leaked transcript records")
	}
}

func TestExecutionToolOutputLostAcknowledgmentReplaysExactDelta(t *testing.T) {
	f := newPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	client := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json"), dropAt: 4}
	cfg.Client = client
	publisher, mapper := bindNativeMapper(t, f, cfg)
	tool := &codex.Tool{ID: "command-item", Kind: codex.CommandTool, Status: codex.ToolRunning, Command: &codex.CommandExecution{Command: "printf fixture", Cwd: "/private/workspace", Source: codex.AgentCommand, Actions: []codex.CommandAction{}}}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolStartedEvent, ItemID: tool.ID, Tool: tool})
	e := codex.Event{Kind: codex.ToolOutputEvent, ThreadID: f.thread, TurnID: f.turn, Correlated: true, ItemID: tool.ID, TextDelta: "once\n"}
	if handled, err := mapper.PublishCore(context.Background(), e); !handled || err == nil {
		t.Fatal("lost tool acknowledgment was not retained")
	}
	if _, err := mapper.PublishCore(context.Background(), e); err == nil {
		t.Fatal("uncertain tool publication allowed substitution")
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := worker.OpenExecutionPublisher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 5 || client.calls[3] != client.calls[4] {
		t.Fatal("recovered output did not reuse its exact request identity")
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal("lost acknowledgment duplicated tool identity")
	}
	m, err := store.Decode[domain.ExecutionMessage](rows[0])
	if err != nil || (m.Tool.Output == nil || *m.Tool.Output != "once\n") || m.LastSequence != 4 {
		t.Fatal("lost acknowledgment duplicated output")
	}
}

func TestExecutionToolBoundsKeepPriorEvidenceAndSequence(t *testing.T) {
	for _, kind := range []domain.ToolKind{domain.CommandTool, domain.PatchTool} {
		t.Run(string(kind), func(t *testing.T) {
			f := newPublicationFixture(t)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			id := domain.NewID()
			e := f.toolEvent(domain.ExecutionToolStarted, 3, id, "bounded-tool")
			e.Tool.Snapshot = toolCommand()
			if kind == domain.PatchTool {
				e.Tool.Snapshot = &domain.ToolSnapshot{Kind: kind, Status: domain.ToolRunning, Changes: []domain.FileChangeObservation{}}
			}
			f.publish(t, e)
			last := uint64(3)
			for sequence := uint64(4); sequence < 12; sequence++ {
				e := f.toolEvent(domain.ExecutionToolOutput, sequence, id, "bounded-tool")
				delta := strings.Repeat("x", domain.MaxMessageText)
				e.Tool.Delta = &delta
				if kind == domain.PatchTool {
					changes := []domain.FileChangeObservation{{Path: "/private/file", Diff: delta, Kind: domain.UpdatedFile}}
					e.Kind, e.Tool.Delta, e.Tool.Changes = domain.ExecutionToolPatch, nil, &changes
				}
				if _, err := f.call(f.requestEvent(t, e)); err != nil {
					break
				}
				last = sequence
			}
			if last < 4 || last > 7 {
				t.Fatal("tool bound was not enforced after retaining valid evidence")
			}
			r, err := f.service.Store.Get(context.Background(), domain.MessageKind, id)
			if err != nil {
				t.Fatal(err)
			}
			m, err := store.Decode[domain.ExecutionMessage](r)
			if err != nil || m.LastSequence != last || (kind == domain.CommandTool && (m.Tool.Output == nil || len(*m.Tool.Output) != domain.MaxMessageText)) || (kind == domain.PatchTool && len(m.Tool.Patches) != int(last-3)) {
				t.Fatal("oversized tool update truncated or replaced prior evidence")
			}
			e = f.event(domain.ExecutionTurnFinished, last+1)
			e.Outcome, e.ProblemCode = domain.ExecutionFailed, domain.ResourceExhausted
			f.publish(t, e)
		})
	}
}
