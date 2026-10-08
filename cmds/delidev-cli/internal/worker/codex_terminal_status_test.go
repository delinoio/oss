// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func codexTerminalStatusFixture(t *testing.T) (*CodexEventPublisher, *openCodeBindingRPC) {
	t.Helper()
	f := newCheckpointFixture(t)
	instance := domain.NewID()
	p := &ExecutionPublisher{config: PublicationConfig{Root: f.root, Instance: instance, Credential: Credential{MachineID: f.input.MachineID}}, execution: f.input.ExecutionID, job: f.jobID, input: f.input, path: filepath.Join(f.root, "publication.json"), state: publicationJournal{Version: 1, JobID: f.jobID, InstanceID: instance}}
	rpc := &openCodeBindingRPC{t: t, publisher: p}
	p.config.Client = rpc
	c := NewCodexEventPublisher(p)
	if err := c.BindThread(context.Background(), f.bound); err != nil {
		t.Fatal(err)
	}
	if err := c.AcceptInput(context.Background(), codex.TurnResult{RequestID: f.input.TurnRequestID, InputID: f.input.InputID, TurnID: domain.NewID()}); err != nil {
		t.Fatal(err)
	}
	return c, rpc
}

func TestCodexEmptyModelVerificationDoesNotPublishOrCompleteInput(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		c, rpc := codexTerminalStatusFixture(t)
		before := len(rpc.events)
		event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.ModelVerificationAbsent, Correlated: true, ThreadID: c.thread, TurnID: c.turn}
		if foreign {
			event.TurnID = domain.NewID()
		}
		handled, err := c.PublishCore(context.Background(), event)
		if foreign {
			if err == nil || handled {
				t.Fatal("foreign metadata crossed original turn boundary")
			}
		} else if err != nil || !handled || c.finished || c.blocked || len(rpc.events) != before {
			t.Fatal("empty verification gained publication/completion authority", err)
		}
	}
}

func TestSubagentCodexTerminalStatusKeepsLiveChildCleanup(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	ctx := context.Background()
	child := domain.SubagentObservation{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(c.thread), Source: domain.CodexCollaborationSource, SourceID: string(domain.NewID()), Status: domain.SubagentRunning}
	observe := func(value domain.SubagentObservation) {
		t.Helper()
		if handled, err := c.PublishCore(ctx, codex.Event{Kind: codex.SubagentEvent, Correlated: true, ThreadID: c.thread, TurnID: c.turn, Subagents: []domain.SubagentObservation{value}}); !handled || err != nil {
			t.Fatal("owned child observation rejected", err)
		}
	}
	observe(child)
	if handled, err := c.PublishCore(ctx, codex.Event{Kind: codex.TurnCompletedEvent, Correlated: true, ThreadID: c.thread, TurnID: c.turn, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}); !handled || err != nil {
		t.Fatal("root completion rejected", err)
	}
	before := len(rpc.events)
	for _, status := range []codex.ThreadState{codex.ThreadIdle, codex.ThreadActive} {
		if handled, err := c.PublishCore(ctx, codex.Event{Kind: codex.ThreadStatusEvent, Correlated: true, ThreadID: c.thread, Status: &codex.ThreadStatus{Type: status}}); !handled || err != nil || len(rpc.events) != before {
			t.Fatal("benign root status interrupted child wait or republished root state", err)
		}
	}
	if _, closed := c.childState(); closed {
		t.Fatal("root idle completed a live child")
	}
	child.Status, child.Source, child.SourceID = domain.SubagentCompleted, domain.CodexHistorySource, string(domain.NewID())
	observe(child)
	if _, closed := c.childState(); !closed || !c.finished || c.blocked {
		t.Fatal("late child completion did not settle independent cleanup")
	}
}

func TestSubagentCodexTerminalStatusStillRejectsForeignAndWaitingEvents(t *testing.T) {
	for _, scenario := range []string{"foreign", "late", "waiting", "duplicate", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			c.finished = true
			event := codex.Event{Kind: codex.ThreadStatusEvent, Correlated: true, ThreadID: c.thread, Status: &codex.ThreadStatus{Type: codex.ThreadActive}}
			switch scenario {
			case "foreign":
				event.ThreadID = domain.NewID()
			case "late":
				event.Late = true
			case "waiting":
				event.Status.ActiveFlags = []codex.ActiveFlag{codex.WaitingInput}
			case "duplicate":
				event.Status.ActiveFlags = []codex.ActiveFlag{codex.WaitingInput, codex.WaitingInput}
			case "unknown":
				event.Status.ActiveFlags = []codex.ActiveFlag{"unknown"}
			}
			before := len(rpc.events)
			if _, err := c.PublishCore(context.Background(), event); err == nil || !c.blocked || len(rpc.events) != before {
				t.Fatal("invalid terminal status acquired publication authority")
			}
		})
	}
}
