package codex

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
)

func TestSafetyMetadataPrivateAndOriginalBound(t *testing.T) {
	for _, method := range []string{"model/verification", "model/safetyBuffering/updated", "turn/moderationMetadata", "model/rerouted"} {
		t.Run(method, func(t *testing.T) {
			c, turn := observationClient()
			paused := c.execution.paused
			p := map[string]any{"threadId": c.thread, "turnId": turn}
			switch method {
			case "model/verification":
				p["verifications"] = []string{"trustedAccessForCyber"}
			case "model/safetyBuffering/updated":
				p["model"] = "fixture-model"
				p["useCases"] = []string{"private-sentinel"}
				p["reasons"] = []string{"private-sentinel"}
				p["showBufferingUi"] = true
				p["fasterModel"] = "private-faster-model"
			case "turn/moderationMetadata":
				p["metadata"] = map[string]any{"private-sentinel": true}
			case "model/rerouted":
				p["fromModel"] = "fixture-model"
				p["toModel"] = "private-safety-model"
				p["reason"] = "highRiskCyberActivity"
			}
			e, err := observeFixture(c, method, p)
			if err != nil || e.Kind != MetadataEvent || e.Safety == nil || e.TurnID != turn || !e.Correlated {
				t.Fatal("valid metadata unsupported", err)
			}
			for _, v := range []any{e, e.Safety, e.Safety.Buffering, e.Safety.Reroute} {
				raw, _ := json.Marshal(v)
				if strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "trustedAccessForCyber") {
					t.Fatal("private metadata serialized")
				}
			}
			if c.execution.settings.Model != "fixture-model" || c.execution.active != turn || len(c.execution.inputs) != 0 {
				t.Fatal("metadata changed original authority")
			}
			if (c.problem != nil) != (method == "model/rerouted") || method == "model/rerouted" && !c.execution.paused || method != "model/rerouted" && c.execution.paused != paused {
				t.Fatal("reroute lost fresh-send fence")
			}
			e.Safety.Moderation = append(e.Safety.Moderation, 'x')
			if _, err := observeFixture(c, method, p); err != nil {
				t.Fatal("original replay lost", err)
			}
			tracked := c.execution.turns[turn]
			tracked.Turn.Status = TurnCompleted
			c.execution.turns[turn] = tracked
			replay, err := observeFixture(c, method, p)
			if err != nil || !replay.Late {
				t.Fatal("terminal exact replay lost", err)
			}
			p["unknown"] = true
			if _, err := observeFixture(c, method, p); err == nil {
				t.Fatal("unknown fields accepted")
			}
			delete(p, "unknown")
			p["turnId"] = domain.NewID()
			if _, err := observeFixture(c, method, p); err == nil {
				t.Fatal("foreign turn accepted")
			}
			p["threadId"] = domain.NewID()
			if e, err := observeFixture(c, method, p); err != nil || e.Kind != NativeExtensionEvent || e.Safety != nil {
				t.Fatal("foreign metadata promoted")
			}
		})
	}
}
func TestSafetyMetadataRejectsMalformedVariants(t *testing.T) {
	for _, tc := range []struct {
		method string
		extra  map[string]any
	}{
		{"model/verification", map[string]any{"verifications": []string{"unknown"}}},
		{"model/verification", map[string]any{"verifications": nil}},
		{"model/rerouted", map[string]any{"fromModel": "fixture-model", "toModel": "other", "reason": "unknown"}},
		{"model/rerouted", map[string]any{"fromModel": "foreign-model", "toModel": "other", "reason": "highRiskCyberActivity"}},
		{"model/safetyBuffering/updated", map[string]any{"model": "fixture-model", "useCases": []string{}, "reasons": []string{}}},
		{"model/safetyBuffering/updated", map[string]any{"model": "foreign-model", "useCases": []string{}, "reasons": []string{}, "showBufferingUi": true}},
		{"turn/moderationMetadata", map[string]any{}},
		{"turn/moderationMetadata", map[string]any{"metadata": strings.Repeat("x", maxSafetyMetadata)}},
	} {
		c, turn := observationClient()
		p := map[string]any{"threadId": c.thread, "turnId": turn}
		for k, v := range tc.extra {
			p[k] = v
		}
		if _, err := observeFixture(c, tc.method, p); err == nil {
			t.Fatal("malformed safety metadata accepted", tc.method)
		}
	}
}

func TestSafetyBufferingTransitionsPreserveUsageAndOriginalCompletion(t *testing.T) {
	c, turn := observationClient()
	for _, show := range []bool{true, false} {
		e, err := observeFixture(c, "model/safetyBuffering/updated", map[string]any{"threadId": c.thread, "turnId": turn, "model": "fixture-model", "useCases": []string{}, "reasons": []string{}, "showBufferingUi": show, "fasterModel": nil})
		if err != nil || e.Safety.Buffering.ShowBufferingUI != show || c.problem != nil {
			t.Fatal("buffering transition failed", err)
		}
	}
	usage, err := observeFixture(c, "thread/tokenUsage/updated", map[string]any{"threadId": c.thread, "turnId": turn, "tokenUsage": map[string]any{"total": usageCounts(30), "last": usageCounts(20), "modelContextWindow": nil}})
	if err != nil || usage.Usage == nil || *usage.Usage.Total.Total != 30 || *usage.Usage.Last.Total != 20 {
		t.Fatal("metadata changed original usage", err)
	}
	done, err := observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": map[string]any{"id": turn, "status": "completed", "items": []any{}, "error": nil}})
	if err != nil || done.Kind != TurnCompletedEvent || !done.Correlated || c.execution.active != "" {
		t.Fatal("metadata changed original completion", err)
	}
	if _, err := observeFixture(c, "model/safetyBuffering/updated", map[string]any{"threadId": c.thread, "turnId": turn, "model": "fixture-model", "useCases": []string{}, "reasons": []string{}, "showBufferingUi": true, "fasterModel": nil}); err == nil {
		t.Fatal("changed terminal metadata replay accepted")
	}
}
func TestSafetyRerouteBlocksFreshInputWithoutReconciliation(t *testing.T) {
	c, turn := observationClient()
	c.mode = ThreadProtocol
	direct := true
	c.execution.thread.DirectInput = &direct
	if err := c.eligibleTurnLocked(false); err != nil {
		t.Fatal(err)
	}
	e, err := observeFixture(c, "model/rerouted", map[string]any{"threadId": c.thread, "turnId": turn, "fromModel": "fixture-model", "toModel": "safety-model", "reason": "highRiskCyberActivity"})
	if err != nil || e.Safety.Reroute.ToModel != "safety-model" || c.eligibleTurnLocked(false) == nil || c.execution.settings.Model != "fixture-model" {
		t.Fatal("reroute granted a fresh send or changed attribution", err)
	}
}
