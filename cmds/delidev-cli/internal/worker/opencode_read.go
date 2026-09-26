package worker

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

type openCodeReadPart struct {
	update domain.ExecutionToolUpdate
	latest domain.ToolSnapshot
	states int
}

// Only the pinned local Read shape crosses this presentation boundary. Native
// image/PDF attachments, provider-executed calls and unknown metadata need their
// own adapters; never omit those fields to make a tool appear supported.
func openCodeReadSnapshot(native *opencode.NativeToolPart) (domain.ToolSnapshot, error) {
	bad := func() (domain.ToolSnapshot, error) {
		return domain.ToolSnapshot{}, domain.Fail(domain.Unsupported, "This OpenCode tool needs an additional presentation profile.", "Retain the original tool and its complete content without omission or reinterpretation.")
	}
	if native == nil || native.Name != "read" || len(native.Attachments) != 0 || native.Timing != nil && native.Timing.Compacted != nil {
		return bad()
	}
	r := &domain.OpenCodeReadObservation{CallID: native.CallID, Raw: native.Raw, Title: native.Title, Output: native.Output, Error: native.Error}
	if len(native.Input) == 0 || domain.Decode(native.Input, &r.Input) != nil {
		return bad()
	}
	if native.Metadata != nil {
		r.Metadata = &domain.OpenCodeReadMetadata{}
		if domain.Decode(native.Metadata, r.Metadata) != nil {
			return bad()
		}
	}
	if native.PartMetadata != nil {
		var metadata struct {
			ProviderExecuted *bool `json:"providerExecuted,omitempty"`
		}
		if domain.Decode(native.PartMetadata, &metadata) != nil {
			return bad()
		}
		r.ProviderExecuted = metadata.ProviderExecuted
	}
	if native.Timing != nil {
		r.Timing = &domain.OpenCodeToolTiming{Start: native.Timing.Start, End: native.Timing.End}
	}
	s := domain.ToolSnapshot{Kind: domain.OpenCodeReadTool, Read: r}
	switch native.State {
	case opencode.ToolPending:
		s.Status = domain.ToolPending
	case opencode.ToolRunning:
		s.Status = domain.ToolRunning
	case opencode.ToolCompleted:
		s.Status = domain.ToolCompleted
	case opencode.ToolError:
		s.Status = domain.ToolFailed
	default:
		return bad()
	}
	if s.Validate() != nil {
		return bad()
	}
	return s, nil
}

func (c *OpenCodeTextPublisher) observeRead(ctx context.Context, native opencode.NativePart) error {
	b := c.binding
	owner := c.messages[native.MessageID]
	if native.SessionID != b.thread || owner == nil || owner.role != domain.AssistantMessage || c.parts[native.ID] != nil || domain.NativeIdentity(native.ID).Validate(domain.OpenCode, domain.NativePartIdentity) != nil {
		return publicationUncertain()
	}
	snapshot, err := openCodeReadSnapshot(native.Tool)
	if err != nil {
		return err
	}
	prior := c.reads[native.ID]
	if prior != nil && prior.update.NativeParentID == native.MessageID && reflect.DeepEqual(prior.latest, snapshot) {
		return nil // Original repeated native snapshots never append twice.
	}
	if owner.finalized {
		return publicationUncertain()
	}
	kind := domain.ExecutionToolUpdated
	if prior == nil {
		if snapshot.Status != domain.ToolPending || len(c.parts)+len(c.reads) >= maxOpenCodeTextParts || c.calls[snapshot.Read.CallID] != "" {
			return publicationUncertain()
		}
		prior = &openCodeReadPart{update: domain.ExecutionToolUpdate{ID: domain.NewID(), NativeID: native.ID, NativeParentID: native.MessageID}}
		kind = domain.ExecutionToolStarted
	} else {
		if prior.update.NativeParentID != native.MessageID || prior.states >= 1024 || domain.ValidateOpenCodeReadTransition(prior.latest, snapshot) != nil {
			return publicationUncertain()
		}
		if snapshot.Status == domain.ToolCompleted || snapshot.Status == domain.ToolFailed {
			kind = domain.ExecutionToolCompleted
		}
	}
	update := prior.update
	update.Snapshot = &snapshot
	if err := update.Validate(kind); err != nil {
		return err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil || len(raw) > maxOpenCodeTextBytes-c.bytes {
		return publicationUncertain()
	}
	if err := b.publisher.Publish(ctx, domain.ExecutionEvent{Kind: kind, NativeThreadID: b.thread, NativeTurnID: b.turn, Tool: &update}); err != nil {
		return err
	}
	// Retain an owned immutable copy, not pointers belonging to the caller's
	// native observation. Changed content cannot rewrite an earlier comparison.
	var retained domain.ToolSnapshot
	if domain.Decode(raw, &retained) != nil || retained.Validate() != nil {
		return publicationUncertain()
	}
	prior.latest = retained
	prior.states++
	c.reads[native.ID] = prior
	c.calls[snapshot.Read.CallID] = native.ID
	c.bytes += len(raw)
	return nil
}
