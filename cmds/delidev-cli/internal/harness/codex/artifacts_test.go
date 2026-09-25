package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestNativeArtifactsPreserveAuthoritativePlanAndReasoningParts(t *testing.T) {
	c, turn := observationClient()
	params := map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 1, "item": map[string]any{"type": "plan", "id": "plan-item", "text": ""}}
	e, err := observeFixture(c, "item/started", params)
	if err != nil || e.Kind != ArtifactStartedEvent || !e.Correlated || e.Artifact.Kind != PlanArtifact || e.Artifact.Text != "" || e.Artifact.Summary != nil || e.Artifact.Content != nil {
		t.Fatal("plan start was not a distinct native artifact")
	}
	e, err = observeFixture(c, "item/plan/delta", map[string]any{"threadId": c.thread, "turnId": turn, "itemId": "plan-item", "delta": "Draft plan"})
	if err != nil || e.Kind != ArtifactDeltaEvent || e.ArtifactDelta.Kind != PlanTextDelta || e.ArtifactDelta.Index != nil || e.ArtifactDelta.Text != "Draft plan" {
		t.Fatal("plan streaming observation was changed")
	}
	delete(params, "startedAtMs")
	params["completedAtMs"], params["item"] = 2, map[string]any{"type": "plan", "id": "plan-item", "text": "Final replacement plan"}
	e, err = observeFixture(c, "item/completed", params)
	if err != nil || e.Kind != ArtifactCompletedEvent || e.Artifact.Text != "Final replacement plan" || c.execution.turns[turn].Turn.Status != TurnRunning {
		t.Fatal("authoritative replacement plan was rejected or became turn completion")
	}
	params["item"] = map[string]any{"type": "reasoning", "id": "reasoning-item"}
	e, err = observeFixture(c, "item/completed", params)
	if err != nil || e.Artifact.Kind != ReasoningArtifact || e.Artifact.Summary == nil || e.Artifact.Content == nil || len(e.Artifact.Summary) != 0 || len(e.Artifact.Content) != 0 {
		t.Fatal("pinned empty-array defaults were lost or native reasoning was invented")
	}
	params["item"] = map[string]any{"type": "reasoning", "id": "reasoning-item", "summary": []string{"first", "", "third"}, "content": []string{"native content"}}
	e, err = observeFixture(c, "item/completed", params)
	if err != nil || len(e.Artifact.Summary) != 3 || e.Artifact.Summary[1] != "" || e.Artifact.Content[0] != "native content" || e.Artifact.Text != "" {
		t.Fatal("native reasoning parts were flattened or conflated")
	}
}

func TestNativeArtifactsKeepIndexedDeltasAndTurnObservationsDistinct(t *testing.T) {
	c, turn := observationClient()
	for _, item := range []struct {
		method string
		index  string
		kind   ArtifactDeltaKind
	}{{"item/reasoning/summaryTextDelta", "summaryIndex", ReasoningSummaryDelta}, {"item/reasoning/summaryPartAdded", "summaryIndex", ReasoningSummaryAdded}, {"item/reasoning/textDelta", "contentIndex", ReasoningContentDelta}} {
		params := map[string]any{"threadId": c.thread, "turnId": turn, "itemId": "reasoning-item", item.index: 2}
		if item.kind != ReasoningSummaryAdded {
			params["delta"] = "exact 한글"
		}
		e, err := observeFixture(c, item.method, params)
		if err != nil || e.Kind != ArtifactDeltaEvent || e.ArtifactDelta.Kind != item.kind || e.ArtifactDelta.Index == nil || *e.ArtifactDelta.Index != 2 || !e.Correlated || e.Late || (item.kind != ReasoningSummaryAdded && e.ArtifactDelta.Text != "exact 한글") {
			t.Fatalf("indexed native delta lost its identity: %s: %v", item.method, err)
		}
	}
	params := map[string]any{"threadId": c.thread, "turnId": turn, "explanation": nil, "plan": []any{map[string]any{"step": "one", "status": "completed"}, map[string]any{"step": "two", "status": "inProgress"}, map[string]any{"step": "three", "status": "pending"}}}
	e, err := observeFixture(c, "turn/plan/updated", params)
	if err != nil || e.Kind != TurnPlanEvent || e.ItemID != "" || e.Plan == nil || e.Plan.Explanation != nil || len(e.Plan.Steps) != 3 || e.Plan.Steps[0].Status != PlanCompleted || e.Plan.Steps[1].Status != PlanRunning || e.Plan.Steps[2].Status != PlanPending || c.execution.turns[turn].Turn.Status != TurnRunning {
		t.Fatal("native plan progress invented item identity or terminal authority")
	}
	for _, diff := range []string{"", "diff --git a/file b/file\n-old\n+new\n"} {
		e, err := observeFixture(c, "turn/diff/updated", map[string]any{"threadId": c.thread, "turnId": turn, "diff": diff})
		if err != nil || e.Kind != TurnDiffEvent || e.Diff == nil || *e.Diff != diff || e.ItemID != "" || e.Tool != nil {
			t.Fatal("turn diff was rewritten or promoted to file-tool ownership")
		}
	}
	c.execution.turns[turn] = trackedTurn{Turn: Turn{ID: turn, Status: TurnCompleted}}
	e, err = observeFixture(c, "turn/plan/updated", params)
	if err != nil || !e.Late || !e.Correlated {
		t.Fatal("post-terminal plan lost its late-observation classification")
	}
	params["threadId"], params["explanation"] = domain.NewID(), "foreign-observation"
	e, err = observeFixture(c, "turn/plan/updated", params)
	raw, _ := json.Marshal(e)
	if err != nil || e.Kind != NativeExtensionEvent || e.Plan != nil || strings.Contains(string(raw), "foreign-observation") {
		t.Fatal("foreign plan escaped private native storage")
	}
}

func TestNativeArtifactsRejectMalformedUnionsAndIndexes(t *testing.T) {
	for _, item := range []map[string]any{
		{"type": "plan", "id": "plan"},
		{"type": "plan", "id": "plan", "text": nil},
		{"type": "plan", "id": "plan", "text": "", "summary": nil},
		{"type": "reasoning", "id": "reasoning", "summary": nil},
		{"type": "reasoning", "id": "reasoning", "summary": []any{nil}},
		{"type": "reasoning", "id": "reasoning", "content": []any{42}},
		{"type": "reasoning", "id": "reasoning", "content": make([]string, MaxArtifactParts+1)},
		{"type": "reasoning", "id": "reasoning", "text": nil},
		{"type": "reasoning", "id": "reasoning", "encryptedContent": "opaque"},
	} {
		c, turn := observationClient()
		if _, err := observeFixture(c, "item/started", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 1, "item": item}); err == nil {
			t.Fatal("malformed native artifact was accepted")
		}
	}
	for _, bad := range []string{"missing-index", "null-index", "negative-index", "excessive-index", "wrong-index", "wrong-null-field", "missing-delta", "unknown-turn"} {
		t.Run(bad, func(t *testing.T) {
			c, turn := observationClient()
			params := map[string]any{"threadId": c.thread, "turnId": turn, "itemId": "reasoning", "summaryIndex": 0, "delta": "text"}
			switch bad {
			case "missing-index":
				delete(params, "summaryIndex")
			case "null-index":
				params["summaryIndex"] = nil
			case "negative-index":
				params["summaryIndex"] = -1
			case "excessive-index":
				params["summaryIndex"] = MaxArtifactParts
			case "wrong-index":
				params["contentIndex"] = 0
			case "wrong-null-field":
				params["diff"] = nil
			case "missing-delta":
				delete(params, "delta")
			case "unknown-turn":
				params["turnId"] = domain.NewID()
			}
			if _, err := observeFixture(c, "item/reasoning/summaryTextDelta", params); err == nil {
				t.Fatal("invalid indexed reasoning delta accepted")
			}
		})
	}
	for _, plan := range []any{nil, []any{map[string]any{"step": "step", "status": "failed"}}, []any{map[string]any{"status": "pending"}}} {
		c, turn := observationClient()
		if _, err := observeFixture(c, "turn/plan/updated", map[string]any{"threadId": c.thread, "turnId": turn, "plan": plan}); err == nil {
			t.Fatal("malformed native progress plan accepted")
		}
	}
}
