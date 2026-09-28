package worker

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

type openCodeToolPart struct {
	update domain.ExecutionToolUpdate
	latest domain.ToolSnapshot
	states int
}

func (c *OpenCodeTextPublisher) observeTool(ctx context.Context, native opencode.NativePart) error {
	b := c.binding
	owner := c.messages[native.MessageID]
	if native.SessionID != b.thread || owner == nil || owner.role != domain.AssistantMessage || c.revisions[native.ID] != nil || c.parts[native.ID] != nil || domain.NativeIdentity(native.ID).Validate(domain.OpenCode, domain.NativePartIdentity) != nil {
		return publicationUncertain()
	}
	var snapshot domain.ToolSnapshot
	var err error
	if native.Tool != nil && native.Tool.Name == "bash" {
		snapshot, err = openCodeShellSnapshot(native.Tool)
	} else if native.Tool != nil && native.Tool.Name == "todowrite" {
		snapshot, err = openCodeTodoSnapshot(native.Tool)
	} else if native.Tool != nil && domain.OpenCodeBuiltinName(native.Tool.Name).Valid() {
		snapshot, err = openCodeBuiltinSnapshot(native.Tool)
	} else {
		snapshot, err = openCodeReadSnapshot(native.Tool)
	}
	if err != nil {
		return err
	}
	prior := c.tools[native.ID]
	if prior != nil && prior.update.NativeParentID == native.MessageID && reflect.DeepEqual(prior.latest, snapshot) {
		return nil // Original repeated native snapshots never append twice.
	}
	if owner.finalized {
		return publicationUncertain()
	}
	kind := domain.ExecutionToolUpdated
	if prior == nil {
		if snapshot.Status != domain.ToolPending || len(c.parts)+len(c.tools)+len(c.revisions) >= maxOpenCodeTextParts || c.calls[snapshot.OpenCodeCallID()] != "" {
			return publicationUncertain()
		}
		prior = &openCodeToolPart{update: domain.ExecutionToolUpdate{ID: domain.NewID(), NativeID: native.ID, NativeParentID: native.MessageID}}
		kind = domain.ExecutionToolStarted
	} else {
		if prior.update.NativeParentID != native.MessageID || prior.states >= 1024 || domain.ValidateOpenCodeToolTransition(prior.latest, snapshot) != nil {
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
	c.tools[native.ID] = prior
	c.calls[snapshot.OpenCodeCallID()] = native.ID
	c.bytes += len(raw)
	return nil
}
