package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

func revisionFixture(f *openCodeTextFixture, kind opencode.PartKind) opencode.Observation {
	hash := "original opaque snapshot"
	p := &opencode.NativePart{ID: textPartOneID, MessageID: textAssistantID, SessionID: f.input.SessionID, Kind: kind}
	switch kind {
	case opencode.SnapshotPartKind:
		p.Snapshot = &hash
	case opencode.PatchPartKind:
		p.Patch = &opencode.NativePatchPart{Hash: hash, Files: []string{"first.txt", "second.txt"}}
	case opencode.StepStartPartKind, opencode.StepFinishPartKind:
		p.Step = &opencode.NativeStepPart{Snapshot: &hash}
	}
	return opencode.Observation{Kind: opencode.MessagePartUpdatedEvent, Part: p}
}

func TestOpenCodeRevisionsRetainOriginalSourceAndOwnedFiles(t *testing.T) {
	for _, kind := range []opencode.PartKind{opencode.SnapshotPartKind, opencode.PatchPartKind, opencode.StepStartPartKind, opencode.StepFinishPartKind} {
		t.Run(string(kind), func(t *testing.T) {
			f := newOpenCodeTextFixture(t)
			f.user(t)
			f.publish(t, f.assistant(false))
			o := revisionFixture(f, kind)
			f.publish(t, o)
			f.publish(t, o)
			if len(f.rpc.events) != 6 {
				t.Fatal("immutable revision was dropped or duplicated")
			}
			var event domain.ExecutionEvent
			if domain.Decode(f.rpc.events[5], &event) != nil || event.Kind != domain.ExecutionArtifactCompleted || event.Artifact.NativeID != textPartOneID || event.Artifact.NativeParentID != textAssistantID || event.Artifact.Snapshot.Revision.Source != domain.OpenCodeRevisionSource(kind) {
				t.Fatal("revision source or native ownership changed")
			}
			if kind == opencode.PatchPartKind {
				o.Part.Patch.Files[0] = "changed caller value"
				if f.c.revisions[textPartOneID].snapshot.Revision.Files[0] != "first.txt" {
					t.Fatal("caller mutated retained original evidence")
				}
			}
		})
	}
}

func TestOpenCodeRevisionUncertaintyCannotAdoptChangedEvidence(t *testing.T) {
	for _, name := range []string{"hash", "files", "kind", "parent", "text-collision", "lost-ack"} {
		t.Run(name, func(t *testing.T) {
			f := newOpenCodeTextFixture(t)
			f.user(t)
			f.publish(t, f.assistant(false))
			o := revisionFixture(f, opencode.PatchPartKind)
			if name != "lost-ack" {
				f.publish(t, o)
			}
			switch name {
			case "hash":
				o.Part.Patch.Hash = "changed"
			case "files":
				o.Part.Patch.Files[0] = "changed"
			case "kind":
				o = revisionFixture(f, opencode.SnapshotPartKind)
			case "parent":
				o.Part.MessageID = f.input.MessageID
			case "text-collision":
				o = f.part(textPartOneID, "changed", true)
			case "lost-ack":
				f.rpc.lose = true
			}
			if _, err := f.c.PublishObservation(context.Background(), f.observation(o)); err == nil || !f.c.blocked {
				t.Fatal("changed/uncertain revision remained publishable")
			}
		})
	}
}

func TestOpenCodeChangeSummariesPreserveSourceEmptyAndOriginalPatch(t *testing.T) {
	f, c := newOpenCodeEventsFixture(t)
	for _, summary := range []bool{false, true} {
		o := opencode.Observation{Kind: opencode.SessionDiffEvent}
		if summary {
			o.Kind = opencode.MessageUpdatedEvent
			o.Message = &opencode.NativeMessage{ID: f.input.MessageID, SessionID: f.input.SessionID, Role: opencode.UserMessageRole, User: &opencode.NativeUserMessage{}}
			// Reuse the original fixture's exact selected user settings.
			settings := f.c.binding.requested.Session
			o.Message.User.Agent = string(settings.Agent)
			o.Message.User.Provider = settings.Provider
			o.Message.User.Model = settings.Model
			o.Message.User.Summary = json.RawMessage(`{"title":"Original title","body":"Original body","diffs":[{"file":"first.txt","patch":" original patch\\n","additions":2,"deletions":1,"status":"modified"}]}`)
		} else {
			o.Ancillary, _ = json.Marshal(map[string]any{"sessionID": f.input.SessionID, "diff": []any{}})
		}
		publishOpenCodeFixtureEvent(t, f, c, o)
		var event domain.ExecutionEvent
		if domain.Decode(f.rpc.events[len(f.rpc.events)-1], &event) != nil || event.Progress == nil || event.Progress.Progress.Changes == nil {
			t.Fatal("native change progress disappeared")
		}
		changes := event.Progress.Progress.Changes
		if summary && (changes.NativeMessageID != f.input.MessageID || len(changes.Diffs) != 1 || changes.Title == nil || changes.Diffs[0].Patch == nil) || !summary && (changes.NativeMessageID != "" || changes.Diffs == nil || len(changes.Diffs) != 0) {
			t.Fatal("native source or empty observation reinterpreted")
		}
	}
}
