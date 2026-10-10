// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestThreadLifecycleLossPreservesOriginalExecutionAndCleanup(t *testing.T) {
	for _, method := range []string{"thread/archived", "thread/deleted", "thread/closed"} {
		t.Run(method, func(t *testing.T) {
			c, turn := observationClient()
			c.mode = ThreadProtocol
			direct := true
			c.execution.thread.DirectInput = &direct
			if c.eligibleTurnLocked(false) != nil {
				t.Fatal("fixture lacks original send authority")
			}
			before := c.execution.thread
			_, err := observeFixture(c, method, map[string]any{"threadId": c.thread})
			if domain.SafeError(err).Code != domain.RecoveryRequired || c.problem == nil || !c.execution.paused || c.execution.active != turn || !reflect.DeepEqual(before, c.execution.thread) {
				t.Fatal("lifecycle loss changed product/native success", err)
			}
			if domain.SafeError(c.eligibleTurnLocked(false)).Code != domain.RecoveryRequired {
				t.Fatal("lifecycle loss allowed another send")
			}
			e, err := observeFixture(c, "thread/unarchived", map[string]any{"threadId": c.thread})
			if err != nil || e.Kind != MetadataEvent || c.problem == nil || !c.execution.paused || c.execution.active != turn {
				t.Fatal("unarchive cleared original recovery", err)
			}
		})
	}
}

func TestThreadLifecycleForeignMalformedAndChildKeepOriginalFences(t *testing.T) {
	for _, method := range []string{"thread/archived", "thread/deleted", "thread/closed", "thread/unarchived", "thread/queue/changed", "thread/compacted", "thread/reverted"} {
		c, turn := observationClient()
		params := map[string]any{"threadId": domain.NewID()}
		if method == "thread/compacted" {
			params["turnId"] = turn
		}
		e, err := observeFixture(c, method, params)
		if err != nil || e.Kind != NativeExtensionEvent || c.problem != nil || c.execution.active != turn {
			t.Fatal("foreign lifecycle gained root owner", method, err)
		}
		for _, params := range []any{nil, map[string]any{}, map[string]any{"threadId": nil}, map[string]any{"threadId": c.thread, "extra": true}} {
			if _, err := observeFixture(c, method, params); err == nil {
				t.Fatal("malformed lifecycle accepted", method)
			}
		}
	}
}

func TestThreadContextSupplementsNeverCreateCanonicalProof(t *testing.T) {
	for _, method := range []string{"thread/compacted", "thread/reverted"} {
		for _, owned := range []bool{false, true} {
			c, turn := observationClient()
			if owned {
				if method == "thread/compacted" {
					c.execution.compaction = &manualCompaction{actionID: domain.NewID(), turnID: turn}
				} else {
					c.execution.revertAction = domain.NewID()
					c.execution.active = ""
				}
			}
			beforeItems, beforeOrder := len(c.execution.compactionItems), len(c.execution.contextOrder)
			params := map[string]any{"threadId": c.thread}
			if method == "thread/compacted" {
				params["turnId"] = turn
			}
			event, err := observeFixture(c, method, params)
			if !owned {
				if domain.SafeError(err).Code != domain.RecoveryRequired || !c.execution.paused || c.problem == nil {
					t.Fatal("unowned supplement synthesized owner", method, err)
				}
			} else {
				if err != nil || event.Kind != MetadataEvent || event.Metadata != ThreadContextSupplementDiscarded || event.Compaction != nil || event.Turn != nil || event.ItemID != "" || c.problem != nil {
					t.Fatal("supplement became canonical content", method, err)
				}
				raw, _ := json.Marshal(event)
				if strings.Contains(string(raw), "action_id") {
					t.Fatal("claim became public proof")
				}
			}
			expectedActive := turn
			if owned && method == "thread/reverted" {
				expectedActive = ""
			}
			if len(c.execution.compactionItems) != beforeItems || len(c.execution.contextOrder) != beforeOrder || c.execution.active != expectedActive {
				t.Fatal("supplement duplicated canonical history")
			}
		}
	}
}

func TestThreadPassiveMetadataClosedUnionsAndPrivacy(t *testing.T) {
	c, turn := observationClient()
	fixtures := []struct {
		method string
		params map[string]any
	}{
		{"thread/name/updated", map[string]any{"threadId": c.thread}},
		{"thread/name/updated", map[string]any{"threadId": c.thread, "threadName": nil}},
		{"thread/name/updated", map[string]any{"threadId": c.thread, "threadName": "private-native-sentinel"}},
		{"thread/queue/changed", map[string]any{"threadId": c.thread}},
		{"thread/unarchived", map[string]any{"threadId": c.thread}},
		{"thread/project/updated", map[string]any{"threadId": c.thread, "projectId": nil}},
		{"thread/project/updated", map[string]any{"threadId": c.thread, "projectId": "private-native-sentinel"}},
	}
	for _, operation := range []string{"created", "deleted"} {
		fixtures = append(fixtures, struct {
			method string
			params map[string]any
		}{"thread/attachment/updated", map[string]any{"threadId": c.thread, "attachmentId": "private-native-sentinel", "attachmentType": "fixture-type", "identityKey": "private-native-sentinel", "operation": operation}})
	}
	for _, change := range []string{"created", "updated", "deleted"} {
		fixtures = append(fixtures, struct {
			method string
			params map[string]any
		}{"project/changed", map[string]any{"projectId": "private-native-sentinel", "changeType": change}})
	}
	for _, result := range []any{map[string]any{"type": "completed"}, map[string]any{"type": "completed", "text": nil}, map[string]any{"type": "completed", "text": "private-native-sentinel"}, map[string]any{"type": "failed"}} {
		fixtures = append(fixtures, struct {
			method string
			params map[string]any
		}{"thread/prediction/updated", map[string]any{"threadId": c.thread, "sourceTurnId": turn, "result": result}})
	}
	for _, method := range []string{"thread/environment/connected", "thread/environment/disconnected"} {
		fixtures = append(fixtures, struct {
			method string
			params map[string]any
		}{method, map[string]any{"threadId": c.thread, "environmentId": "private-native-sentinel"}})
	}
	for _, position := range []any{nil, map[string]any{"type": "threadStart"}, map[string]any{"type": "turn", "turnId": turn}} {
		fixtures = append(fixtures, struct {
			method string
			params map[string]any
		}{"thread/readState/changed", map[string]any{"threadId": c.thread, "readState": map[string]any{"firstUnread": position, "revision": "private-native-sentinel"}}})
	}
	fixtures = append(fixtures, struct {
		method string
		params map[string]any
	}{"thread/readState/changed", map[string]any{"threadId": c.thread, "readState": map[string]any{"revision": "private-native-sentinel"}}})
	beforeThread, beforeSettings := c.execution.thread, c.execution.settings
	beforeInputs, beforePending, beforeTurns := len(c.execution.inputs), len(c.execution.pending), len(c.execution.turns)
	for _, f := range fixtures {
		event, err := observeFixture(c, f.method, f.params)
		if err != nil || event.Kind != MetadataEvent || event.Metadata != ThreadMetadataDiscarded || event.Native != nil || !reflect.DeepEqual(beforeThread, c.execution.thread) || !reflect.DeepEqual(beforeSettings, c.execution.settings) || len(c.execution.inputs) != beforeInputs || len(c.execution.pending) != beforePending || len(c.execution.turns) != beforeTurns || c.execution.active != turn || c.problem != nil {
			t.Fatal("passive metadata changed execution", f.method, err)
		}
		raw, _ := json.Marshal(event)
		if strings.Contains(string(raw), "private-native-sentinel") {
			t.Fatal("private descriptors escaped", f.method)
		}
		extra := map[string]any{}
		for k, v := range f.params {
			extra[k] = v
		}
		extra["extra"] = true
		if _, err := observeFixture(c, f.method, extra); err == nil {
			t.Fatal("extra schema field accepted", f.method)
		}
	}
}

func TestThreadPassiveMetadataRejectsMalformedUnionsAndForeignTurns(t *testing.T) {
	c, turn := observationClient()
	fixtures := []struct {
		method string
		params any
	}{
		{"thread/environment/connected", map[string]any{"threadId": c.thread}},
		{"thread/readState/changed", map[string]any{"threadId": c.thread, "readState": nil}},
		{"thread/readState/changed", map[string]any{"threadId": c.thread, "readState": map[string]any{"firstUnread": nil}}},
		{"thread/readState/changed", map[string]any{"threadId": c.thread, "readState": map[string]any{"revision": "fixture", "firstUnread": map[string]any{"type": "threadStart", "turnId": nil}}}},
		{"thread/readState/changed", map[string]any{"threadId": c.thread, "readState": map[string]any{"revision": "fixture", "firstUnread": map[string]any{"type": "turn", "turnId": nil}}}},
		{"thread/readState/changed", map[string]any{"threadId": c.thread, "readState": map[string]any{"revision": "fixture", "firstUnread": map[string]any{"type": "unknown"}}}},
		{"thread/attachment/updated", map[string]any{"threadId": c.thread, "attachmentId": "fixture", "attachmentType": "fixture", "identityKey": "fixture", "operation": "updated"}},
		{"thread/project/updated", map[string]any{"threadId": c.thread}},
		{"project/changed", map[string]any{"projectId": "fixture", "changeType": "unknown"}},
		{"thread/prediction/updated", map[string]any{"threadId": c.thread, "sourceTurnId": turn, "result": map[string]any{"type": "failed", "text": nil}}},
		{"thread/prediction/updated", map[string]any{"threadId": c.thread, "sourceTurnId": turn, "result": map[string]any{"type": "completed", "text": false}}},
		{"thread/prediction/updated", map[string]any{"threadId": c.thread, "sourceTurnId": turn, "result": nil}},
	}
	for _, f := range fixtures {
		if _, err := observeFixture(c, f.method, f.params); err == nil {
			t.Fatal("invalid union accepted", f.method)
		}
	}
	event, err := observeFixture(c, "thread/name/updated", map[string]any{"threadId": domain.NewID(), "threadName": "private-native-sentinel"})
	if err != nil || event.Kind != NativeExtensionEvent || c.problem != nil {
		t.Fatal("foreign descriptor gained root scope", err)
	}
	if _, err := observeFixture(c, "thread/prediction/updated", map[string]any{"threadId": c.thread, "sourceTurnId": domain.NewID(), "result": map[string]any{"type": "completed", "text": nil}}); domain.SafeError(err).Code != domain.RecoveryRequired || c.problem == nil || !c.execution.paused {
		t.Fatal("unowned prediction granted input authority", err)
	}
}

func TestThreadCompactedSupplementJoinsExactCanonicalOwnerWithoutDuplicateHistory(t *testing.T) {
	c, turn := observationClient()
	if _, err := observeFixture(c, "item/started", contextItem(c, turn, "original-context", true, 10)); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		event, err := observeFixture(c, "thread/compacted", map[string]any{"threadId": c.thread, "turnId": turn})
		if err != nil || event.Kind != MetadataEvent || event.Metadata != ThreadContextSupplementDiscarded || len(c.execution.contextOrder) != 1 || len(c.execution.compactionItems) != 1 || c.execution.compactionItems[string(turn)+"/original-context"].completedAt != nil {
			t.Fatal("supplement counted as canonical completion", err)
		}
	}
	if event, err := observeFixture(c, "item/completed", contextItem(c, turn, "original-context", false, 12)); err != nil || event.Kind != CompactionEvent || event.Compaction.Stage != CompactionCompleted {
		t.Fatal("canonical completion lost", err)
	}
	if event, err := observeFixture(c, "thread/compacted", map[string]any{"threadId": c.thread, "turnId": turn}); err != nil || event.Kind != MetadataEvent || len(c.execution.contextOrder) != 1 {
		t.Fatal("late supplement duplicated history", err)
	}
	if _, err := observeFixture(c, "thread/compacted", map[string]any{"threadId": c.thread, "turnId": domain.NewID()}); domain.SafeError(err).Code != domain.RecoveryRequired || !c.execution.paused {
		t.Fatal("old context owner accepted foreign turn", err)
	}
}

func TestThreadMetadataPreservesChildOwnership(t *testing.T) {
	c, turn := observationClient()
	child := domain.NewID()
	c.subagents = map[string]domain.SubagentObservation{string(child): {ID: domain.NewID(), NativeID: string(child), ParentID: string(c.thread), Status: domain.SubagentRunning}}
	before := c.subagents[string(child)]
	for _, method := range []string{"thread/closed", "thread/deleted", "thread/archived", "thread/name/updated", "thread/queue/changed", "thread/reverted"} {
		event, err := observeFixture(c, method, map[string]any{"threadId": child})
		if err != nil || event.Kind != MetadataEvent || event.Metadata != RawSupplementDiscarded || c.problem != nil || c.execution.active != turn || !reflect.DeepEqual(before, c.subagents[string(child)]) {
			t.Fatal("metadata changed child/root cleanup owner", method, err)
		}
	}
}

func TestThreadRevertedSupplementCannotReuseOwnerDuringNewExecution(t *testing.T) {
	c, turn := observationClient()
	c.execution.revertAction = domain.NewID()
	if _, err := observeFixture(c, "thread/reverted", map[string]any{"threadId": c.thread}); domain.SafeError(err).Code != domain.RecoveryRequired || c.execution.active != turn || !c.execution.paused {
		t.Fatal("old Revert owner authorized a new execution observation", err)
	}
}

func TestThreadRevertedSupplementAfterOriginalClaimRetainsProofAndRecovery(t *testing.T) {
	for _, mode := range []string{"ready", "lost"} {
		t.Run(mode, func(t *testing.T) {
			c, capture, source, ids, inputs := revertFixture(t, mode)
			action := domain.NewID()
			claims := 0
			proof, err := c.RevertThread(context.Background(), action, source, inputs[1], ids[1], func(RevertIntent) error { claims++; return nil })
			if (mode == "ready") != (err == nil) || claims != 1 || c.execution.revertAction != action {
				t.Fatal("original Revert owner missing", err)
			}
			beforeProblem, beforePaused := c.problem, c.execution.paused
			for n := 0; n < 2; n++ {
				event, err := observeFixture(c, "thread/reverted", map[string]any{"threadId": c.thread})
				if err != nil || event.Kind != MetadataEvent || event.Metadata != ThreadContextSupplementDiscarded || c.problem != beforeProblem || c.execution.paused != beforePaused || len(requestsOf(t, capture, "thread/revert")) != 1 {
					t.Fatal("supplement acknowledged/replayed original Revert", err)
				}
			}
			if mode == "ready" && (proof.Revert == nil || proof.Revert.ActionID != action || proof.TurnsCount != 1) {
				t.Fatal("canonical Revert proof changed")
			}
		})
	}
}

func TestThreadMetadataRejectsBoundsDuplicateAndNestedFields(t *testing.T) {
	c, turn := observationClient()
	fixtures := []struct {
		method string
		params any
	}{
		{"thread/name/updated", map[string]any{"threadId": c.thread, "threadName": strings.Repeat("n", 4097)}},
		{"thread/environment/connected", map[string]any{"threadId": c.thread, "environmentId": strings.Repeat("e", 1025)}},
		{"thread/readState/changed", map[string]any{"threadId": c.thread, "readState": map[string]any{"revision": strings.Repeat("r", 1025)}}},
		{"thread/readState/changed", map[string]any{"threadId": c.thread, "readState": map[string]any{"revision": "revision", "firstUnread": map[string]any{"type": "threadStart", "extra": true}}}},
		{"thread/prediction/updated", map[string]any{"threadId": c.thread, "sourceTurnId": turn, "result": map[string]any{"type": "completed", "extra": true}}},
		{"thread/prediction/updated", map[string]any{"threadId": c.thread, "sourceTurnId": turn, "result": map[string]any{"type": "completed", "text": strings.Repeat("p", 1<<20)}}},
	}
	for _, f := range fixtures {
		if _, err := observeFixture(c, f.method, f.params); err == nil {
			t.Fatal("malformed private metadata accepted", f.method)
		}
	}
	raw := json.RawMessage(`{"threadId":"` + string(c.thread) + `","threadName":"first","threadName":"second"}`)
	if _, err := c.observeMetadataLocked(nativewire.Event{Kind: nativewire.Notification, Method: "thread/name/updated", Params: raw}); err == nil {
		t.Fatal("duplicate private field accepted")
	}
}
