package codex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func observationClient() (*Client, domain.ID) {
	thread, turn := domain.NewID(), domain.NewID()
	state := newExecutionState(Thread{ID: thread, SessionID: thread, History: LegacyHistory}, EffectiveSettings{Model: "fixture-model", Provider: "fixture-provider", Cwd: "/fixture", ApprovalPolicy: ApprovalOnRequest, ApprovalsReviewer: "user", Sandbox: Sandbox{Type: ReadOnly}})
	state.active = turn
	state.turns[turn] = trackedTurn{Turn: Turn{ID: turn, Status: TurnRunning}, Mode: domain.PlanMode}
	return &Client{thread: thread, execution: state}, turn
}

func observeFixture(c *Client, method string, params any) (Event, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return Event{}, err
	}
	return c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: method, Params: raw})
}

func usageCounts(total int64) map[string]any {
	return map[string]any{"inputTokens": int64(11), "cachedInputTokens": int64(4), "outputTokens": int64(7), "reasoningOutputTokens": int64(3), "totalTokens": total}
}

func TestNativeResumeGoalAbsenceHasNoExecutionAuthority(t *testing.T) {
	c, turn := observationClient()
	c.execution.paused = true
	event, err := observeFixture(c, "thread/goal/cleared", map[string]any{"threadId": c.thread})
	if err != nil || event.Metadata != NativeGoalAbsent || !event.Correlated || event.Native != nil || !c.execution.paused || c.execution.active != turn {
		t.Fatal("goal absence changed execution authority", err)
	}
	for _, value := range []any{nil, map[string]any{}, map[string]any{"threadId": "invalid"}, map[string]any{"threadId": c.thread, "goal": "unexpected"}} {
		if _, err := observeFixture(c, "thread/goal/cleared", value); err == nil {
			t.Fatal("malformed goal absence accepted")
		}
	}
	for _, observation := range []struct {
		method string
		params any
	}{
		{"thread/goal/cleared", map[string]any{"threadId": domain.NewID()}},
		{"thread/goal/updated", map[string]any{"threadId": c.thread, "goal": "private goal"}},
	} {
		event, err := observeFixture(c, observation.method, observation.params)
		if err != nil || event.Kind != NativeExtensionEvent || event.Correlated {
			t.Fatal("foreign/populated goal bypassed its adapter", err)
		}
	}
}

func TestNativeUsagePreservesReportedMeaningAndUnavailableFields(t *testing.T) {
	c, turn := observationClient()
	params := map[string]any{"threadId": c.thread, "turnId": turn, "tokenUsage": map[string]any{"total": usageCounts(30), "last": usageCounts(20), "modelContextWindow": nil}}
	e, err := observeFixture(c, "thread/tokenUsage/updated", params)
	if err != nil || e.Kind != UsageEvent || !e.Correlated || e.Late || e.Native != nil || e.Usage == nil || *e.Usage.Total.Total != 30 || *e.Usage.Last.Total != 20 || e.Usage.Total.CacheWrite != nil || e.Usage.ContextWindow != nil {
		t.Fatalf("native counters or unavailable observations were invented: %v", err)
	}
	// Neither cumulative nor last-request total must be reconstructed by adding
	// overlapping token breakdowns. Codex can also reset its reported counters.
	params["tokenUsage"].(map[string]any)["total"] = usageCounts(2)
	e, err = observeFixture(c, "thread/tokenUsage/updated", params)
	if err != nil || *e.Usage.Total.Total != 2 {
		t.Fatal("counter reset was silently changed or discarded")
	}
	params["threadId"] = domain.NewID()
	e, err = observeFixture(c, "thread/tokenUsage/updated", params)
	if err != nil || e.Kind != NativeExtensionEvent || e.Usage != nil {
		t.Fatal("subagent usage was attributed to the root")
	}
}

func TestNativeUsageRejectsMalformedAndUnownedReports(t *testing.T) {
	for _, bad := range []string{"missing", "null", "negative", "overflow", "fraction", "unknown", "context-zero", "unknown-turn"} {
		t.Run(bad, func(t *testing.T) {
			c, turn := observationClient()
			counts := usageCounts(20)
			usage := map[string]any{"total": counts, "last": usageCounts(20), "modelContextWindow": nil}
			params := map[string]any{"threadId": c.thread, "turnId": turn, "tokenUsage": usage}
			switch bad {
			case "missing":
				delete(counts, "inputTokens")
			case "null":
				counts["outputTokens"] = nil
			case "negative":
				counts["cachedInputTokens"] = -1
			case "overflow":
				counts["totalTokens"] = json.Number("9223372036854775808")
			case "fraction":
				counts["totalTokens"] = 1.5
			case "unknown":
				usage["cost"] = "protected-diagnostic"
			case "context-zero":
				usage["modelContextWindow"] = 0
			case "unknown-turn":
				params["turnId"] = domain.NewID()
			}
			if _, err := observeFixture(c, "thread/tokenUsage/updated", params); err == nil {
				t.Fatal("accepted malformed or unowned native usage")
			}
		})
	}
}

func nativeSettingsFixture(c *Client) map[string]any {
	s := c.execution.settings
	return map[string]any{"model": s.Model, "modelProvider": s.Provider, "effort": s.Effort, "serviceTier": s.ServiceTier, "cwd": s.Cwd, "approvalPolicy": s.ApprovalPolicy, "approvalsReviewer": s.ApprovalsReviewer, "sandboxPolicy": s.Sandbox, "collaborationMode": map[string]any{"mode": "plan", "settings": map[string]any{"model": s.Model, "reasoning_effort": s.Effort, "developer_instructions": "native-protected-internal-instructions"}}, "multiAgentMode": "explicitRequestOnly", "personality": nil, "summary": nil, "activePermissionProfile": nil}
}

func TestNativeSettingsNotificationsCannotReplaceAcceptedAuthority(t *testing.T) {
	for _, changed := range []string{"", "model", "modelProvider", "effort", "serviceTier", "cwd", "approvalPolicy", "approvalsReviewer", "sandboxPolicy", "multiAgentMode", "collaborationMode"} {
		t.Run(changed, func(t *testing.T) {
			c, _ := observationClient()
			settings := nativeSettingsFixture(c)
			if changed != "" {
				settings[changed] = "changed"
				if changed == "sandboxPolicy" {
					settings[changed] = Sandbox{Type: ReadOnly, NetworkAccess: true}
				}
				if changed == "collaborationMode" {
					settings[changed] = collaborationMode{Mode: nativeExecute, Settings: collaborationSettings{Model: c.execution.settings.Model}}
				}
			}
			e, err := observeFixture(c, "thread/settings/updated", map[string]any{"threadId": c.thread, "threadSettings": settings})
			if changed != "" {
				if err == nil {
					t.Fatal("accepted changed native authority")
				}
				return
			}
			raw, _ := json.Marshal(e)
			if err != nil || e.Metadata != ThreadSettingsChecked || strings.Contains(string(raw), "protected") || strings.Contains(string(raw), "/fixture") {
				t.Fatalf("metadata exposed internal settings or lost validation: %v", err)
			}
		})
	}
}

func TestNativeMetadataRetainsNoticesWithoutSensitivePayloads(t *testing.T) {
	c, _ := observationClient()
	for _, method := range []string{"warning", "configWarning"} {
		params := map[string]any{"message": "fixture-protected-warning", "threadId": c.thread}
		if method == "configWarning" {
			params = map[string]any{"summary": "fixture-protected-warning", "details": "fixture-protected-details", "path": "/fixture-protected-path", "range": nil}
		}
		e, err := observeFixture(c, method, params)
		raw, _ := json.Marshal(e)
		if err != nil || e.Kind != NoticeEvent || !e.Correlated || strings.Contains(string(raw), "protected") {
			t.Fatalf("native diagnostic escaped normalization: %v", err)
		}
	}
	e, err := observeFixture(c, "remoteControl/status/changed", map[string]any{"status": "disabled", "installationId": "fixture-protected-id", "serverName": "fixture-protected-name"})
	raw, _ := json.Marshal(e)
	if err != nil || e.Metadata != RemoteControlDisabled || strings.Contains(string(raw), "protected") {
		t.Fatal("remote identity escaped")
	}
	for _, status := range []string{"connecting", "connected", "errored", "unknown"} {
		if _, err := observeFixture(c, "remoteControl/status/changed", map[string]any{"status": status, "installationId": "id", "serverName": "name"}); err == nil {
			t.Fatal("remote control acquired private runtime authority")
		}
	}
	e, err = observeFixture(c, "account/rateLimits/updated", map[string]any{"rateLimits": map[string]any{"limitId": "codex", "primary": nil, "spendControlReached": nil}})
	if err != nil || e.Metadata != QuotaUnavailable {
		t.Fatal("empty quota was interpreted as a measured allowance")
	}
	e, err = observeFixture(c, "account/rateLimits/updated", map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 40}}})
	if err != nil || e.Kind != NativeExtensionEvent {
		t.Fatal("populated quota bypassed its required adapter")
	}
}

func TestNativeSettingsDriftBlocksFurtherInputThroughEventReader(t *testing.T) {
	c, _, _, _ := boundTurnFixture(t, "ready")
	if _, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode)); err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	settings := nativeSettingsFixture(c)
	settings["model"] = "foreign"
	fixtureSignal(t, c, "notify", map[string]any{"method": "thread/settings/updated", "params": map[string]any{"threadId": c.thread, "threadSettings": settings}})
	_, err := c.NextEvent(context.Background())
	assertCode(t, err, domain.RecoveryRequired)
	_, err = c.Steer(context.Background(), domain.NewID(), domain.NewID(), c.execution.active, input(domain.PlanMode))
	assertCode(t, err, domain.RecoveryRequired)
}
