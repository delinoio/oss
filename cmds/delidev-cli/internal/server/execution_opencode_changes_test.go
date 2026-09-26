package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestOpenCodeRevisionsRejectChangedCompletionAndForeignProfiles(t *testing.T) {
	f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	u := &domain.ExecutionArtifactUpdate{ID: domain.NewID(), NativeID: "prt_01960dcbe1fbABCDEFGHIJKLMN", NativeParentID: "msg_01960dcbe1fbABCDEFGHIJKLMN", Snapshot: &domain.ArtifactSnapshot{Kind: domain.OpenCodeRevisionArtifact, Revision: &domain.OpenCodeRevision{Source: domain.OpenCodePatchRevision, Hash: "original", Files: []string{"one.txt", "two.txt"}}}}
	e := f.event(domain.ExecutionArtifactStarted, 3)
	e.Artifact = u
	f.publish(t, e)
	e.Kind = domain.ExecutionArtifactCompleted
	e.Sequence = 4
	u.Snapshot.Revision.Files = []string{"two.txt", "one.txt"}
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("changed native file order completed an immutable revision")
	}
	u.Snapshot.Revision.Files = []string{"one.txt", "two.txt"}
	request := f.publish(t, e)
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("immutable revision completion receipt changed")
	}
	record, _ := f.service.Store.Get(context.Background(), domain.MessageKind, u.ID)
	message, err := store.Decode[domain.ExecutionMessage](record)
	if err != nil || message.State != domain.MessageComplete || message.Artifact.Completed.Revision.Hash != "original" {
		t.Fatal("native revision did not retain original closure")
	}
	input := f.input
	input.Configuration.Harness = domain.Codex
	e.Artifact.NativeParentID = ""
	if validateNativeMessageOrigin(input, e) == nil {
		t.Fatal("foreign profile accepted an OpenCode revision")
	}
}

func TestOpenCodeChangesRequireOriginalInputAndEventWithoutNativeItem(t *testing.T) {
	f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	e := f.event(domain.ExecutionProgressObserved, 3)
	changes := &domain.OpenCodeChanges{Source: domain.OpenCodeInputSummary, NativeEventID: "evt_01960dcbe1fbABCDEFGHIJKLMN", NativeMessageID: "msg_01960dcbe1fcABCDEFGHIJKLMN", Diffs: []domain.OpenCodeFileDiff{}}
	e.Progress = &domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.OpenCodeChangesProgressKind, Changes: changes}}
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("foreign user message acquired input-summary ownership")
	}
	changes.NativeMessageID = e.NativeTurnID
	request := f.publish(t, e)
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("original change progress receipt lost")
	}
	id := e.Progress.ID
	e.Progress.ID = domain.NewID()
	e.Sequence++
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("same native event acquired duplicate progress")
	}
	e.Progress.Progress = domain.NativeProgress{Kind: domain.OpenCodeTodoProgressKind, Todo: &domain.OpenCodeTodoProgress{NativeEventID: changes.NativeEventID, Todos: []domain.OpenCodeTodo{}}}
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("same native event acquired a different progress kind")
	}
	record, _ := f.service.Store.Get(context.Background(), domain.MessageKind, id)
	message, _ := store.Decode[domain.ExecutionMessage](record)
	if message.NativeID != "" || message.NativeParentID != "" || message.Progress.Changes.Diffs == nil {
		t.Fatal("native session progress fabricated item ownership")
	}
	record, _ = f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	session, _ := store.Decode[domain.Session](record)
	if session.Execution.LatestDiffID != id || session.Execution.LastSequence != 3 {
		t.Fatal("rejected changes altered the latest source reference")
	}
}
