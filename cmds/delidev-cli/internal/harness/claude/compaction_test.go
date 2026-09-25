package claude

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func compactionFixture(t *testing.T, b *ExecutionBinding, metadata any) StreamEvent {
	t.Helper()
	return lifecycleMessage(t, b, "system", map[string]any{"subtype": "compact_boundary", "compact_metadata": metadata})
}

func TestCompactionObservationNeverAcceptsOrCompletesOriginalInput(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		b, _ := lifecycleFixture(t)
		lifecycleObserve(t, b, lifecycleCommand(t, b, CommandQueued))
		lifecycleObserve(t, b, lifecycleCommand(t, b, CommandStarted))
		lifecycleObserve(t, b, lifecycleInit(t, b))
		if accepted {
			lifecycleObserve(t, b, lifecycleReplay(t, b))
		}
		for _, trigger := range []CompactionTrigger{ManualCompaction, AutomaticCompaction} {
			meta := map[string]any{"trigger": trigger, "pre_tokens": 0, "post_tokens": 0, "duration_ms": 0}
			observed := lifecycleObserve(t, b, compactionFixture(t, b, meta))
			if observed.Kind != CompactionObserved || observed.InputID != "" || observed.Accepted || b.accepted != accepted || b.finished || observed.Compaction.Before != 0 || observed.Compaction.After == nil || *observed.Compaction.After != 0 || observed.Compaction.DurationMS == nil || observed.Compaction.Trigger != trigger {
				t.Fatal("native boundary changed original acceptance or null/zero evidence")
			}
		}
	}
}

func TestCompactionPreservesBothRelinkingObservationsWithoutApplyingThem(t *testing.T) {
	anchor, head, tail := string(domain.NewID()), string(domain.NewID()), string(domain.NewID())
	meta := map[string]any{"trigger": AutomaticCompaction, "pre_tokens": 120000, "post_tokens": 42, "preserved_segment": map[string]any{"head_uuid": head, "anchor_uuid": anchor, "tail_uuid": tail}, "preserved_messages": map[string]any{"anchor_uuid": anchor, "uuids": []string{head, tail}}}
	raw, _ := json.Marshal(meta)
	value, err := decodeCompaction(raw)
	if err != nil || !bytes.Equal(value.Native, raw) || value.Segment.Head != head || value.Messages.IDs[1] != tail || value.DurationMS != nil {
		t.Fatal("original compaction metadata lost", err)
	}
	raw[0] = '['
	if value.Native[0] != '{' {
		t.Fatal("caller changed original compaction metadata")
	}
	public, _ := json.Marshal(value)
	if bytes.Contains(public, []byte("Native")) {
		t.Fatal("raw native metadata entered public JSON")
	}
}

func compactionSummaryFixture(t *testing.T, b *ExecutionBinding) (StreamEvent, StreamEvent) {
	t.Helper()
	anchor, previous := string(domain.NewID()), string(domain.NewID())
	boundary := compactionFixture(t, b, map[string]any{"trigger": AutomaticCompaction, "pre_tokens": 123, "post_tokens": 12, "cumulative_dropped_tokens": 111, "preserved_messages": map[string]any{"anchor_uuid": anchor, "uuids": []string{previous}, "all_uuids": []string{previous}}})
	boundary = lifecycleChange(t, boundary, "logical_parent_uuid", previous)
	summary := lifecycleMessage(t, b, "user", map[string]any{"uuid": anchor, "parent_tool_use_id": nil, "isSynthetic": true, "timestamp": "2026-09-26T00:00:00Z", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "Private native summary."}}}})
	return boundary, summary
}

func TestCompactionSummaryRetainsExactAnchorWithoutOriginalInputAcceptance(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		b, _ := lifecycleFixture(t)
		lifecycleReady(t, b)
		if accepted {
			lifecycleObserve(t, b, lifecycleReplay(t, b))
		}
		boundary, summary := compactionSummaryFixture(t, b)
		observed := lifecycleObserve(t, b, boundary)
		if observed.Compaction.CumulativeDropped == nil || *observed.Compaction.CumulativeDropped != 111 || observed.Compaction.LogicalParent == nil || len(observed.Compaction.Messages.AllIDs) != 1 {
			t.Fatal("original native extension fields lost")
		}
		// Mutating a consumer's metadata must not change the owned pending anchor.
		observed.Compaction.Messages.Anchor = string(domain.NewID())
		value := lifecycleObserve(t, b, summary)
		if value.Kind != CompactionSummaryObserved || value.Summary.BoundaryID != observed.NativeID || len(value.Summary.Blocks) != 1 || *value.Summary.Blocks[0].Text != "Private native summary." || value.Accepted || value.InputID != "" || b.accepted != accepted || b.finished || b.pendingCompaction != nil {
			t.Fatal("summary changed input authority or original content")
		}
		raw, _ := json.Marshal(value.Summary)
		if bytes.Contains(raw, []byte("Private native")) {
			t.Fatal("summary entered ordinary JSON")
		}
		if !accepted {
			lifecycleObserve(t, b, lifecycleReplay(t, b))
		}
		lifecycleObserve(t, b, lifecycleResult(t, b, Completed, false))
	}
}

func TestCompactionPendingRootSummaryDoesNotAdoptOwnedChildContent(t *testing.T) {
	b := taskFixture(t)
	boundary, summary := compactionSummaryFixture(t, b)
	lifecycleObserve(t, b, boundary)
	child := lifecycleMessage(t, b, "user", map[string]any{"parent_tool_use_id": "parent", "timestamp": "2026-09-26T00:00:00Z", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "Original child context."}}}})
	value := lifecycleObserve(t, b, child)
	if value.Kind != ContentObserved || value.Content[0].Kind != ChildInputObserved || b.pendingCompaction == nil {
		t.Fatal("child content became the pending root summary")
	}
	contentStart(t, b, "parent", "msg_child_during_root_compaction")
	value = lifecycleObserve(t, b, summary)
	if value.Kind != CompactionSummaryObserved || b.content.active["parent"] == nil {
		t.Fatal("root summary changed independent child content")
	}
}

func TestCompactionSummaryRejectsMissingForeignOrUncorrelatedContext(t *testing.T) {
	for _, name := range []string{"missing-before-result", "missing-before-provider", "missing-before-state", "another-boundary", "wrong-id", "wrong-parent", "missing-parent", "not-synthetic", "invalid-time", "tool-block", "empty-blocks", "string", "unknown-field", "duplicate-summary", "no-boundary", "input-anchor", "seen-anchor", "conflicting-anchor", "null-all", "invalid-all", "null-dropped"} {
		t.Run(name, func(t *testing.T) {
			b := contentFixture(t)
			boundary, summary := compactionSummaryFixture(t, b)
			event := summary
			var fields map[string]any
			_ = json.Unmarshal(boundary.Body, &fields)
			metadata := fields["compact_metadata"].(map[string]any)
			messages := metadata["preserved_messages"].(map[string]any)
			switch name {
			case "input-anchor":
				messages["anchor_uuid"] = b.input
			case "seen-anchor":
				messages["anchor_uuid"] = b.turnID
			case "conflicting-anchor":
				metadata["preserved_segment"] = map[string]any{"head_uuid": string(domain.NewID()), "tail_uuid": string(domain.NewID()), "anchor_uuid": string(domain.NewID())}
			case "null-all":
				messages["all_uuids"] = nil
			case "invalid-all":
				messages["all_uuids"] = []string{"invalid"}
			case "null-dropped":
				metadata["cumulative_dropped_tokens"] = nil
			}
			if name == "input-anchor" || name == "seen-anchor" || name == "conflicting-anchor" || name == "null-all" || name == "invalid-all" || name == "null-dropped" {
				boundary.Body, _ = json.Marshal(fields)
				event = boundary
			} else if name != "no-boundary" {
				lifecycleObserve(t, b, boundary)
			}
			switch name {
			case "missing-before-result":
				event = lifecycleResult(t, b, Completed, false)
			case "missing-before-provider":
				event = lifecycleMessage(t, b, "assistant", map[string]any{})
			case "missing-before-state":
				event = runStateEvent(t, b, RunIdle)
			case "another-boundary":
				event = compactionFixture(t, b, map[string]any{"trigger": AutomaticCompaction, "pre_tokens": 1})
			case "wrong-id":
				event = lifecycleChange(t, event, "uuid", string(domain.NewID()))
			case "wrong-parent":
				event = lifecycleChange(t, event, "parent_tool_use_id", "other-tool")
			case "missing-parent":
				event = lifecycleChange(t, event, "parent_tool_use_id", nil)
			case "not-synthetic":
				event = lifecycleChange(t, event, "isSynthetic", false)
			case "invalid-time":
				event = lifecycleChange(t, event, "timestamp", "invalid")
			case "tool-block":
				event = lifecycleChange(t, event, "message", map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_use", "id": "tool", "name": "Bash", "input": map[string]any{}}}})
			case "empty-blocks":
				event = lifecycleChange(t, event, "message", map[string]any{"role": "user", "content": []any{}})
			case "string":
				event = lifecycleChange(t, event, "message", map[string]any{"role": "user", "content": "Private native summary."})
			case "unknown-field":
				event = lifecycleChange(t, event, "extra", true)
			case "duplicate-summary":
				lifecycleObserve(t, b, summary)
			}
			if _, err := b.Observe(event); err == nil || b.problem == nil {
				t.Fatal("unverified summary cleared pending original context")
			}
		})
	}
}

func TestCompactionMalformedMetadataAndLifecycleRetainRecovery(t *testing.T) {
	for _, name := range []string{"before-init", "after-result", "active-message", "foreign-session", "duplicate-id", "missing-before", "null-before", "negative-before", "fractional-before", "unknown-trigger", "unknown-field", "null-after", "null-duration", "null-segment", "missing-anchor", "self-anchor", "null-messages", "empty-messages", "duplicate-messages", "anchor-in-messages", "invalid-uuid", "oversized-messages", "alias"} {
		t.Run(name, func(t *testing.T) {
			b := contentFixture(t)
			meta := map[string]any{"trigger": AutomaticCompaction, "pre_tokens": 5}
			head, anchor := string(domain.NewID()), string(domain.NewID())
			switch name {
			case "before-init":
				b, _ = lifecycleFixture(t)
			case "after-result":
				lifecycleObserve(t, b, lifecycleResult(t, b, Completed, false))
			case "active-message":
				contentStart(t, b, "", "active")
			case "missing-before":
				delete(meta, "pre_tokens")
			case "null-before":
				meta["pre_tokens"] = nil
			case "negative-before":
				meta["pre_tokens"] = -1
			case "fractional-before":
				meta["pre_tokens"] = 0.5
			case "unknown-trigger":
				meta["trigger"] = "scheduled"
			case "unknown-field":
				meta["extension"] = true
			case "null-after":
				meta["post_tokens"] = nil
			case "null-duration":
				meta["duration_ms"] = nil
			case "null-segment":
				meta["preserved_segment"] = nil
			case "missing-anchor":
				meta["preserved_segment"] = map[string]any{"head_uuid": head, "tail_uuid": head}
			case "self-anchor":
				meta["preserved_segment"] = map[string]any{"head_uuid": head, "anchor_uuid": head, "tail_uuid": head}
			case "null-messages":
				meta["preserved_messages"] = nil
			case "empty-messages":
				meta["preserved_messages"] = map[string]any{"anchor_uuid": anchor, "uuids": []string{}}
			case "duplicate-messages":
				meta["preserved_messages"] = map[string]any{"anchor_uuid": anchor, "uuids": []string{head, head}}
			case "anchor-in-messages":
				meta["preserved_messages"] = map[string]any{"anchor_uuid": anchor, "uuids": []string{anchor}}
			case "invalid-uuid":
				meta["preserved_messages"] = map[string]any{"anchor_uuid": anchor, "uuids": []string{"foreign"}}
			case "oversized-messages":
				ids := make([]string, maxStreamIdentities+1)
				for index := range ids {
					ids[index] = string(domain.NewID())
				}
				meta["preserved_messages"] = map[string]any{"anchor_uuid": anchor, "uuids": ids}
			case "alias":
				meta["Pre_tokens"] = 7
			}
			event := compactionFixture(t, b, meta)
			if name == "foreign-session" {
				event = lifecycleChange(t, event, "session_id", domain.NewID())
			}
			if name == "duplicate-id" {
				lifecycleObserve(t, b, event)
			}
			accepted, finished := b.accepted, b.finished
			if _, err := b.Observe(event); err == nil || b.problem == nil || b.accepted != accepted || b.finished != finished {
				t.Fatal("invalid compaction changed original lifecycle")
			}
		})
	}
}
