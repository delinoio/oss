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

func lifecycleObservationClient() (*Client, domain.ID) {
	c, turn := observationClient()
	c.execution.paused = false
	return c, turn
}

type lifecycleMetadataFixture struct {
	method string
	params map[string]any
}

func lifecycleMetadataFixtures(thread, turn domain.ID) []lifecycleMetadataFixture {
	return []lifecycleMetadataFixture{
		{"thread/unarchived", map[string]any{"threadId": thread}},
		{"thread/name/updated", map[string]any{"threadId": thread}},
		{"thread/name/updated", map[string]any{"threadId": thread, "threadName": "private-descriptor-sentinel"}},
		{"thread/attachment/updated", map[string]any{"threadId": thread, "attachmentType": "private-descriptor-sentinel", "identityKey": "private-descriptor-sentinel", "attachmentId": "native-attachment", "operation": "created"}},
		{"thread/attachment/updated", map[string]any{"threadId": thread, "attachmentType": "private-descriptor-sentinel", "identityKey": "private-descriptor-sentinel", "attachmentId": "native-attachment", "operation": "deleted"}},
		{"thread/queue/changed", map[string]any{"threadId": thread}},
		{"project/changed", map[string]any{"projectId": "private-descriptor-sentinel", "changeType": "created"}},
		{"project/changed", map[string]any{"projectId": "private-descriptor-sentinel", "changeType": "updated"}},
		{"project/changed", map[string]any{"projectId": "private-descriptor-sentinel", "changeType": "deleted"}},
		{"thread/project/updated", map[string]any{"threadId": thread, "projectId": nil}},
		{"thread/project/updated", map[string]any{"threadId": thread, "projectId": "private-descriptor-sentinel"}},
		{"thread/environment/connected", map[string]any{"threadId": thread, "environmentId": "private-descriptor-sentinel"}},
		{"thread/environment/disconnected", map[string]any{"threadId": thread, "environmentId": "private-descriptor-sentinel"}},
		{"thread/prediction/updated", map[string]any{"threadId": thread, "sourceTurnId": turn, "result": map[string]any{"type": "completed", "text": nil}}},
		{"thread/prediction/updated", map[string]any{"threadId": thread, "sourceTurnId": turn, "result": map[string]any{"type": "completed", "text": "private-descriptor-sentinel"}}},
		{"thread/prediction/updated", map[string]any{"threadId": thread, "sourceTurnId": turn, "result": map[string]any{"type": "failed"}}},
		{"thread/readState/changed", map[string]any{"threadId": thread, "readState": map[string]any{"firstUnread": nil, "revision": "private-descriptor-sentinel"}}},
		{"thread/readState/changed", map[string]any{"threadId": thread, "readState": map[string]any{"firstUnread": map[string]any{"type": "threadStart"}, "revision": "private-descriptor-sentinel"}}},
		{"thread/readState/changed", map[string]any{"threadId": thread, "readState": map[string]any{"firstUnread": map[string]any{"type": "turn", "turnId": turn}, "revision": "private-descriptor-sentinel"}}},
	}
}

func TestNativeLifecycleMetadataIsPrivateAndInert(t *testing.T) {
	c, turn := lifecycleObservationClient()
	settings, thread := c.execution.settings, c.execution.thread
	for _, fixture := range lifecycleMetadataFixtures(c.thread, turn) {
		event, err := observeFixture(c, fixture.method, fixture.params)
		if err != nil || event.Kind != MetadataEvent || !event.Correlated || event.ThreadID != c.thread || event.TurnID != "" || event.Native != nil || event.Problem != nil {
			t.Fatal("passive metadata gained authority", fixture.method, event, err)
		}
		raw, _ := json.Marshal(event)
		if strings.Contains(string(raw), "private-descriptor-sentinel") {
			t.Fatal("private metadata escaped")
		}
		if c.problem != nil || c.execution.paused || c.execution.active != turn || !reflect.DeepEqual(c.execution.settings, settings) || !reflect.DeepEqual(c.execution.thread, thread) || len(c.execution.inputs) != 0 || len(c.execution.pending) != 0 || len(c.execution.interactions.arrivals) != 0 {
			t.Fatal("metadata changed original state")
		}
	}
}

func TestNativeLifecycleMetadataPreservesForeignAndMalformedFences(t *testing.T) {
	c, turn := lifecycleObservationClient()
	for _, fixture := range lifecycleMetadataFixtures(c.thread, turn) {
		params := map[string]any{}
		for key, value := range fixture.params {
			params[key] = value
		}
		params["unexpected"] = true
		if _, err := observeFixture(c, fixture.method, params); err == nil {
			t.Fatal("extra metadata field accepted", fixture.method)
		}
		delete(params, "unexpected")
		if _, scoped := params["threadId"]; scoped {
			params["threadId"] = domain.NewID()
			event, err := observeFixture(c, fixture.method, params)
			if err != nil || event.Kind != NativeExtensionEvent || c.problem != nil || c.execution.paused {
				t.Fatal("foreign metadata adopted", fixture.method, err)
			}
		}
	}
	bad := []lifecycleMetadataFixture{
		{"thread/name/updated", map[string]any{"threadId": c.thread, "threadName": nil}},
		{"thread/attachment/updated", map[string]any{"threadId": c.thread, "attachmentType": "native", "identityKey": "key", "attachmentId": "id", "operation": "updated"}},
		{"project/changed", map[string]any{"projectId": "project", "changeType": "renamed"}},
		{"thread/project/updated", map[string]any{"threadId": c.thread}},
		{"thread/environment/connected", map[string]any{"threadId": c.thread}},
		{"thread/prediction/updated", map[string]any{"threadId": c.thread, "sourceTurnId": turn, "result": map[string]any{"type": "completed"}}},
		{"thread/prediction/updated", map[string]any{"threadId": c.thread, "sourceTurnId": turn, "result": map[string]any{"type": "failed", "text": nil}}},
		{"thread/prediction/updated", map[string]any{"threadId": c.thread, "sourceTurnId": domain.NewID(), "result": map[string]any{"type": "completed", "text": nil}}},
		{"thread/readState/changed", map[string]any{"threadId": c.thread, "readState": map[string]any{"firstUnread": nil}}},
		{"thread/readState/changed", map[string]any{"threadId": c.thread, "readState": map[string]any{"firstUnread": map[string]any{"type": "threadStart", "turnId": turn}, "revision": "r"}}},
		{"thread/readState/changed", map[string]any{"threadId": c.thread, "readState": map[string]any{"firstUnread": map[string]any{"type": "turn"}, "revision": "r"}}},
		{"thread/readState/changed", map[string]any{"threadId": c.thread, "readState": map[string]any{"firstUnread": map[string]any{"type": "other"}, "revision": "r"}}},
	}
	for _, fixture := range bad {
		if _, err := observeFixture(c, fixture.method, fixture.params); err == nil {
			t.Fatal("malformed private metadata accepted", fixture.method)
		}
	}
	for _, raw := range []string{"null", "[]", `{"threadId":"` + string(c.thread) + `","threadId":"` + string(c.thread) + `"}`} {
		if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "thread/queue/changed", Params: json.RawMessage(raw)}); err == nil {
			t.Fatal("invalid metadata document accepted")
		}
	}
	event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: "thread/queue/changed", ID: json.RawMessage(`7`), Token: domain.NewID(), Params: json.RawMessage(`{"threadId":"` + string(c.thread) + `"}`)})
	if err == nil && event.Kind != NativeExtensionEvent {
		t.Fatal("notification shape granted request authority")
	}
}

func TestNativeThreadLossFencesOriginalExecutionWithoutTerminalProof(t *testing.T) {
	for _, method := range []string{"thread/archived", "thread/deleted", "thread/closed"} {
		t.Run(method, func(t *testing.T) {
			c, turn := lifecycleObservationClient()
			c.mode = ThreadProtocol
			event, err := observeFixture(c, method, map[string]any{"threadId": c.thread})
			if err != nil || event.Metadata != NativeThreadLifecycleLost || event.Problem == nil || event.Problem.Code != domain.RecoveryRequired || !c.execution.paused || c.problem != event.Problem || c.execution.active != turn || c.execution.turns[turn].Turn.Status != TurnRunning {
				t.Fatal("thread loss fabricated terminal/cleanup facts", event, err)
			}
			for _, fixture := range lifecycleMetadataFixtures(c.thread, turn) {
				if _, err := observeFixture(c, fixture.method, fixture.params); err != nil {
					t.Fatal(err)
				}
			}
			if c.eligibleTurnLocked(false) != c.problem || !c.execution.paused {
				t.Fatal("passive metadata reopened original lost thread")
			}
		})
	}
	for _, method := range []string{"thread/archived", "thread/deleted", "thread/closed"} {
		c, _ := lifecycleObservationClient()
		event, err := observeFixture(c, method, map[string]any{"threadId": domain.NewID()})
		if err != nil || event.Kind != NativeExtensionEvent || c.problem != nil || c.execution.paused {
			t.Fatal("foreign thread loss fenced original execution")
		}
	}
}

func TestDeprecatedLifecycleSupplementsRequireOriginalCanonicalOwner(t *testing.T) {
	for _, method := range []string{"thread/compacted", "thread/reverted"} {
		c, turn := lifecycleObservationClient()
		params := map[string]any{"threadId": c.thread}
		if method == "thread/compacted" {
			params["turnId"] = turn
		}
		event, err := observeFixture(c, method, params)
		if err != nil || event.Metadata != NativeThreadLifecycleLost || c.problem == nil {
			t.Fatal("unclaimed deprecated supplement created authority")
		}
	}
	c, turn := lifecycleObservationClient()
	if _, err := observeFixture(c, "item/started", contextItem(c, turn, "original-context", true, 1)); err != nil {
		t.Fatal(err)
	}
	for _, completed := range []bool{false, true} {
		if completed {
			if _, err := observeFixture(c, "item/completed", contextItem(c, turn, "original-context", false, 2)); err != nil {
				t.Fatal(err)
			}
		}
		before := len(c.execution.contextOrder)
		for n := 0; n < 2; n++ {
			event, err := observeFixture(c, "thread/compacted", map[string]any{"threadId": c.thread, "turnId": turn})
			if err != nil || event.Metadata != NativeLifecycleSupplementDiscarded || event.Compaction != nil || event.Turn != nil || len(c.execution.contextOrder) != before || c.execution.turns[turn].Turn.Status != TurnRunning {
				t.Fatal("supplement duplicated canonical context/result", err)
			}
		}
	}
	c, turn = lifecycleObservationClient()
	action := domain.NewID()
	c.execution.compaction = &manualCompaction{actionID: action, source: ContinuationCheckpoint{ThreadID: c.thread, TurnID: turn}}
	event, err := observeFixture(c, "thread/compacted", map[string]any{"threadId": c.thread, "turnId": turn})
	if err != nil || event.Metadata != NativeLifecycleSupplementDiscarded || c.execution.compaction.acknowledged || c.execution.compaction.terminal || c.execution.compaction.turnID != "" {
		t.Fatal("supplement acknowledged/bound manual mutation")
	}
	c, turn = lifecycleObservationClient()
	c.execution.contextBase = &ContinuationContextCheckpoint{Version: 1, TurnsCount: 1, HistoryDigest: strings.Repeat("0", 64), RolloutDigest: strings.Repeat("0", 64), Records: []ContextRecord{{Trigger: AutomaticCompaction, TurnID: turn, LiveItemID: "original-live", HistoryItemID: "original-history"}}}
	event, err = observeFixture(c, "thread/compacted", map[string]any{"threadId": c.thread, "turnId": turn})
	if err != nil || event.Metadata != NativeLifecycleSupplementDiscarded || len(c.execution.contextBase.Records) != 1 {
		t.Fatal("verified retained context was replaced by supplement")
	}
	c, turn = lifecycleObservationClient()
	c.execution.revertClaim = action
	for n := 0; n < 2; n++ {
		event, err := observeFixture(c, "thread/reverted", map[string]any{"threadId": c.thread})
		if err != nil || event.Metadata != NativeLifecycleSupplementDiscarded || c.execution.revertClaim != action || c.execution.turns[turn].Turn.Status != TurnRunning || c.problem != nil {
			t.Fatal("supplement completed claimed Revert")
		}
	}
}

func TestNativeMetadataSurvivesOriginalAcceptedTurnAndLossBlocksSends(t *testing.T) {
	for _, loss := range []bool{false, true} {
		t.Run(map[bool]string{false: "passive", true: "loss"}[loss], func(t *testing.T) {
			c, capture, _, _ := boundTurnFixture(t, "normal")
			accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			if err != nil {
				t.Fatal(err)
			}
			nextKind(t, c, MessageCompletedEvent)
			for _, fixture := range lifecycleMetadataFixtures(c.thread, accepted.TurnID) {
				fixtureSignal(t, c, "notify", map[string]any{"method": fixture.method, "params": fixture.params})
				event := nextKind(t, c, MetadataEvent)
				if event.Problem != nil || c.problem != nil {
					t.Fatal("passive notification stopped original input")
				}
			}
			if loss {
				fixtureSignal(t, c, "notify", map[string]any{"method": "thread/closed", "params": map[string]any{"threadId": c.thread}})
				event := nextKind(t, c, MetadataEvent)
				if event.Problem == nil || event.Problem.Code != domain.RecoveryRequired {
					t.Fatal("loss did not retain recovery")
				}
				_, err = c.Steer(context.Background(), domain.NewID(), domain.NewID(), accepted.TurnID, input(domain.ExecuteMode))
				if domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("loss allowed native Steer", err)
				}
			}
			fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
			nextKind(t, c, TurnCompletedEvent)
			nextKind(t, c, ThreadStatusEvent)
			if loss {
				_, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
				if domain.SafeError(err).Code != domain.RecoveryRequired || c.problem == nil {
					t.Fatal("late terminal cleared loss fence", err)
				}
			} else if c.problem != nil {
				t.Fatal("passive metadata changed original completion")
			}
			if len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "thread/revert")) != 0 {
				t.Fatal("metadata replayed input or mutation")
			}
		})
	}
}

func TestNativeLifecycleMetadataRetainsChildScopeAndBounds(t *testing.T) {
	c, turn := lifecycleObservationClient()
	child := domain.NewID()
	c.subagents = map[string]domain.SubagentObservation{string(child): {}}
	for _, fixture := range lifecycleMetadataFixtures(child, turn) {
		if fixture.method == "project/changed" {
			continue
		}
		event, err := observeFixture(c, fixture.method, fixture.params)
		if err != nil || event.Metadata != RawSupplementDiscarded || c.execution.active != turn || c.problem != nil {
			t.Fatal("existing child metadata became root authority", fixture.method, err)
		}
	}
	for _, fixture := range []lifecycleMetadataFixture{
		{"thread/name/updated", map[string]any{"threadId": c.thread, "threadName": strings.Repeat("x", 4097)}},
		{"thread/attachment/updated", map[string]any{"threadId": c.thread, "attachmentType": strings.Repeat("x", 257), "identityKey": "key", "attachmentId": "id", "operation": "created"}},
		{"thread/prediction/updated", map[string]any{"threadId": c.thread, "sourceTurnId": turn, "result": map[string]any{"type": "completed", "text": strings.Repeat("x", nativewire.MaxFrame+1)}}},
		{"thread/readState/changed", map[string]any{"threadId": c.thread, "readState": map[string]any{"firstUnread": nil, "revision": strings.Repeat("x", 4097)}}},
	} {
		if _, err := observeFixture(c, fixture.method, fixture.params); err == nil {
			t.Fatal("metadata owning bounds widened", fixture.method)
		}
	}
}

func TestNativeRevertSupplementRetainsActualDurableClaim(t *testing.T) {
	for _, mode := range []string{"ready", "lost", "claim-rejected"} {
		t.Run(mode, func(t *testing.T) {
			fixtureMode := mode
			if mode == "claim-rejected" {
				fixtureMode = "ready"
			}
			c, capture, source, ids, inputs := revertFixture(t, fixtureMode)
			action := domain.NewID()
			claims := 0
			_, err := c.RevertThread(context.Background(), action, source, inputs[1], ids[1], func(RevertIntent) error {
				claims++
				if c.execution.revertClaim != "" {
					t.Fatal("claim identity retained before durable callback")
				}
				if mode == "claim-rejected" {
					return domain.Fail(domain.Conflict, "Fixture claim rejected.", "No native mutation.")
				}
				return nil
			})
			if claims != 1 || (err == nil) != (mode == "ready") {
				t.Fatal("original fixture outcome changed", err)
			}
			before := len(requestsOf(t, capture, "thread/revert"))
			originalProblem := c.problem
			event, observeErr := observeFixture(c, "thread/reverted", map[string]any{"threadId": c.thread})
			if observeErr != nil || len(requestsOf(t, capture, "thread/revert")) != before {
				t.Fatal("supplement resent mutation")
			}
			if mode == "claim-rejected" {
				if c.execution.revertClaim != "" || before != 0 || event.Metadata != NativeThreadLifecycleLost || c.problem == nil {
					t.Fatal("rejected durable claim gained supplement ownership")
				}
			} else if c.execution.revertClaim != action || before != 1 || event.Metadata != NativeLifecycleSupplementDiscarded || c.problem != originalProblem || !c.execution.paused || event.Compaction != nil || event.Turn != nil {
				t.Fatal("supplement replaced original receipt/uncertainty or cleanup")
			}
		})
	}
}
