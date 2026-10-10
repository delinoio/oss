// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"strings"
	"testing"
)

func goalFixture(thread domain.ID) map[string]any {
	return map[string]any{"threadId": thread, "objective": "Complete the original task", "status": "active", "tokenBudget": nil, "tokensUsed": int64(7), "timeUsedSeconds": int64(3), "createdAt": int64(1), "updatedAt": int64(2)}
}

func TestNativeGoalSnapshotRequiresOriginalThreadAndCompleteNativeFields(t *testing.T) {
	thread := domain.NewID()
	for _, field := range []string{"threadId", "objective", "status", "tokenBudget", "tokensUsed", "timeUsedSeconds", "createdAt", "updatedAt"} {
		t.Run("missing-"+field, func(t *testing.T) {
			value := goalFixture(thread)
			delete(value, field)
			raw, _ := json.Marshal(value)
			if _, err := DecodeGoal(raw, thread); err == nil {
				t.Fatal("accepted incomplete native goal")
			}
		})
	}
	for _, changed := range []struct {
		field string
		value any
	}{
		{"threadId", domain.NewID()}, {"status", "stopped"}, {"tokensUsed", -1}, {"tokensUsed", 1.5}, {"timeUsedSeconds", -1}, {"createdAt", -1}, {"updatedAt", 0}, {"tokenBudget", 0}, {"tokenBudget", -1}, {"tokenBudget", "100"}, {"objective", ""}, {"unowned", true},
	} {
		value := goalFixture(thread)
		value[changed.field] = changed.value
		raw, _ := json.Marshal(value)
		if _, err := DecodeGoal(raw, thread); err == nil {
			t.Fatalf("accepted changed %s", changed.field)
		}
	}
	for _, status := range []GoalStatus{GoalActive, GoalPaused, GoalBlocked, GoalUsageLimited, GoalBudgetLimited, GoalComplete} {
		value := goalFixture(thread)
		value["status"] = status
		raw, _ := json.Marshal(value)
		observed, err := DecodeGoal(raw, thread)
		if err != nil || observed.Status != status || observed.TokenBudget != nil || observed.TokensUsed != 7 {
			t.Fatalf("lost native state %s", status)
		}
	}
}

func TestNativeGoalObjectiveCountsUnicodeScalarsAndRetainsBudgetPresence(t *testing.T) {
	objective := strings.Repeat("한", maxGoalObjectiveCharacters)
	if !validGoalObjective(objective) || validGoalObjective(objective+"한") {
		t.Fatal("goal limit does not match native Unicode scalar limit")
	}
	for _, budget := range []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage("123")} {
		set := GoalSet{Objective: &objective, TokenBudget: budget}
		if set.validate() != nil {
			t.Fatal("rejected native budget presence")
		}
		raw, err := json.Marshal(set)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]json.RawMessage
		_ = json.Unmarshal(raw, &decoded)
		if (len(budget) > 0) != (len(decoded["tokenBudget"]) > 0) || len(budget) > 0 && string(budget) != string(decoded["tokenBudget"]) {
			t.Fatal("changed omitted/null/value budget")
		}
	}
	if (GoalSet{}).validate() == nil {
		t.Fatal("accepted an empty goal mutation")
	}
	for _, budget := range []string{"0", "-1", "1.5", "true", "{}", "9223372036854775808"} {
		if (GoalSet{Objective: &objective, TokenBudget: json.RawMessage(budget)}).validate() == nil {
			t.Fatalf("accepted invalid budget %s", budget)
		}
	}
}

func TestNativeGoalObservationsNeverSupplyClearAcknowledgment(t *testing.T) {
	thread, turn := domain.NewID(), domain.NewID()
	client := &Client{nativeGoals: true, thread: thread, execution: &executionState{turns: map[domain.ID]trackedTurn{turn: {Turn: Turn{ID: turn, Status: TurnRunning}}}}}
	update := func(root domain.ID, nativeTurn any) nativewire.Event {
		raw, _ := json.Marshal(map[string]any{"threadId": root, "turnId": nativeTurn, "goal": goalFixture(root)})
		return nativewire.Event{Kind: nativewire.Notification, Method: "thread/goal/updated", Params: raw}
	}
	for _, nativeTurn := range []any{nil, turn} {
		event, err := client.observeGoalLocked(update(thread, nativeTurn))
		if err != nil || event.Kind != GoalObservedEvent || event.Goal == nil || event.Goal.Goal == nil || !event.Correlated || event.Goal.Cleared {
			t.Fatal("lost original ordered native goal snapshot")
		}
	}
	for _, native := range []nativewire.Event{update(domain.NewID(), nil), update(thread, domain.NewID())} {
		if _, err := client.observeGoalLocked(native); err == nil {
			t.Fatal("accepted foreign goal observation")
		}
	}
	raw, _ := json.Marshal(map[string]any{"threadId": thread})
	event, err := client.observeGoalLocked(nativewire.Event{Kind: nativewire.Notification, Method: "thread/goal/cleared", Params: raw})
	if err != nil || event.Goal == nil || !event.Goal.Cleared || event.Goal.Goal != nil || event.RequestID != "" || event.Action != "" {
		t.Fatal("cleared snapshot became an action receipt")
	}
}
