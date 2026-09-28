package worker

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

type openCodeRevisionPart struct {
	parent   string
	kind     opencode.PartKind
	snapshot domain.ArtifactSnapshot
}

func (c *OpenCodeTextPublisher) observeRevision(ctx context.Context, native opencode.NativePart) error {
	b := c.binding
	owner := c.messages[native.MessageID]
	if native.SessionID != b.thread || owner == nil || owner.role != domain.AssistantMessage || c.parts[native.ID] != nil || c.tools[native.ID] != nil || domain.NativeIdentity(native.ID).Validate(domain.OpenCode, domain.NativePartIdentity) != nil {
		return publicationUncertain()
	}
	r := domain.OpenCodeRevision{Source: domain.OpenCodeRevisionSource(native.Kind)}
	switch native.Kind {
	case opencode.SnapshotPartKind:
		if native.Snapshot == nil {
			return publicationUncertain()
		}
		r.Hash = *native.Snapshot
	case opencode.PatchPartKind:
		if native.Patch == nil {
			return publicationUncertain()
		}
		r.Hash = native.Patch.Hash
		r.Files = slices.Clone(native.Patch.Files)
	case opencode.StepStartPartKind, opencode.StepFinishPartKind:
		if native.Step == nil || native.Step.Snapshot == nil {
			return publicationUncertain()
		}
		r.Hash = *native.Step.Snapshot
	default:
		return publicationUncertain()
	}
	snapshot := domain.ArtifactSnapshot{Kind: domain.OpenCodeRevisionArtifact, Revision: &r}
	if snapshot.Validate() != nil {
		return publicationUncertain()
	}
	if prior := c.revisions[native.ID]; prior != nil {
		if prior.parent != native.MessageID || prior.kind != native.Kind || !reflect.DeepEqual(prior.snapshot, snapshot) {
			return publicationUncertain()
		}
		return nil
	}
	if owner.finalized || len(c.parts)+len(c.tools)+len(c.revisions) >= maxOpenCodeTextParts {
		return publicationUncertain()
	}
	update := domain.ExecutionArtifactUpdate{ID: domain.NewID(), NativeID: native.ID, NativeParentID: native.MessageID, Snapshot: &snapshot}
	if err := update.Validate(domain.ExecutionArtifactStarted); err != nil {
		return err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil || len(raw) > maxOpenCodeTextBytes-c.bytes {
		return publicationUncertain()
	}
	// A revision part is already immutable when observed. Preserve the existing
	// artifact transaction boundary; uncertainty between start/completion cannot
	// be repaired by inventing another part or acknowledging the root input.
	for _, kind := range []domain.ExecutionEventKind{domain.ExecutionArtifactStarted, domain.ExecutionArtifactCompleted} {
		if err := b.publisher.Publish(ctx, domain.ExecutionEvent{Kind: kind, NativeThreadID: b.thread, NativeTurnID: b.turn, Artifact: &update}); err != nil {
			return err
		}
	}
	c.revisions[native.ID] = &openCodeRevisionPart{parent: native.MessageID, kind: native.Kind, snapshot: snapshot}
	c.bytes += len(raw)
	return nil
}

func (c *OpenCodeEventPublisher) publishChanges(ctx context.Context, o opencode.Observation) error {
	b := c.text.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	claims, err := b.readClaims()
	if err != nil || !b.validPublicationClaims(claims) || b.stage != openCodeAccepted || c.text.blocked {
		return publicationUncertain()
	}
	changes := domain.OpenCodeChanges{NativeEventID: o.EventID}
	switch o.Kind {
	case opencode.SessionDiffEvent:
		var wire struct {
			SessionID string                    `json:"sessionID"`
			Diff      []domain.OpenCodeFileDiff `json:"diff"`
		}
		if domain.Decode(o.Ancillary, &wire) != nil || wire.SessionID != b.thread {
			return publicationUncertain()
		}
		changes.Source, changes.Diffs = domain.OpenCodeSessionDiff, wire.Diff
	case opencode.MessageUpdatedEvent:
		if o.Message == nil || o.Message.User == nil || o.Message.Assistant != nil || o.Message.ID != b.turn || o.Message.SessionID != b.thread || !c.text.seen[o.EventID] {
			return publicationUncertain()
		}
		var wire struct {
			Title *string                   `json:"title,omitempty"`
			Body  *string                   `json:"body,omitempty"`
			Diffs []domain.OpenCodeFileDiff `json:"diffs"`
		}
		if domain.Decode(o.Message.User.Summary, &wire) != nil {
			return publicationUncertain()
		}
		changes.Source, changes.NativeMessageID = domain.OpenCodeInputSummary, o.Message.ID
		changes.Title, changes.Body, changes.Diffs = wire.Title, wire.Body, wire.Diffs
	default:
		return publicationUncertain()
	}
	update := domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.OpenCodeChangesProgressKind, Changes: &changes}}
	if err := update.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(update)
	if err != nil || len(raw) > maxOpenCodeTextBytes-c.text.bytes {
		return publicationUncertain()
	}
	if err := b.publisher.Publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionProgressObserved, NativeThreadID: b.thread, NativeTurnID: b.turn, Progress: &update}); err != nil {
		return err
	}
	c.text.bytes += len(raw)
	return nil
}
