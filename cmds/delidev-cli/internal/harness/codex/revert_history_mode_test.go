// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestRevertSelectedPaginatedHistoryAtMutationAndReplacement(t *testing.T) {
	for _, mode := range []string{"paginated-ready", "omitted"} {
		t.Run(mode, func(t *testing.T) {
			c, capture, source, ids, inputs := revertFixture(t, mode)
			claims := 0
			var intent RevertIntent
			proof, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[1], ids[1], func(value RevertIntent) error {
				claims++
				intent = value
				return nil
			})
			if err != nil || proof.Revert == nil || proof.TurnsCount != 1 || claims != 1 || len(requestsOf(t, capture, "thread/revert")) != 1 {
				t.Fatal("selected original history blocked or mutation repeated", err)
			}
			if _, err := c.RevertThread(context.Background(), intent.ActionID, source, inputs[1], ids[1], func(RevertIntent) error { claims++; return nil }); err == nil || claims != 1 {
				t.Fatal("settled original mutation was claimed again")
			}
			replacement, replacementCapture := openThreadFixture(t, "thread-revert-"+mode)
			settings := threadSettings(t)
			settings.Cwd = source.Effective.Cwd
			if _, err := replacement.ResumeThread(context.Background(), domain.NewID(), source.ThreadID, settings); err != nil {
				t.Fatal("replacement original binding rejected", err)
			}
			fixtureSignal(t, replacement, "history", map[string]any{"page": map[string]any{"data": intent.ExpectedHistory, "nextCursor": nil, "backwardsCursor": nil}})
			if _, err := replacement.VerifyCompactedContinuation(context.Background(), domain.NewID(), proof); err != nil || replacement.execution.continuationPending || replacement.problem != nil {
				t.Fatal("exact replacement history rejected", err)
			}
			if len(requestsOf(t, replacementCapture, "thread/revert")) != 0 {
				t.Fatal("replacement observation sent another mutation")
			}
		})
	}
}

func TestRevertSelectedPaginatedHistoryExplicitRecovery(t *testing.T) {
	c, capture, source, ids, inputs := revertFixture(t, "paginated-lost")
	var intent RevertIntent
	claims := 0
	if _, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[1], ids[1], func(value RevertIntent) error {
		intent = value
		claims++
		return nil
	}); err == nil || claims != 1 {
		t.Fatal("lost acknowledgment released input or changed claim")
	}
	// Clear only fixture process failure, as in the existing recovery fixture.
	// The original intent, thread, paginated mode and history remain unchanged.
	c.problem, c.execution.paused = nil, false
	proof, err := c.ReconcileRevert(context.Background(), intent)
	if err != nil || proof.TurnsCount != 1 || proof.Revert == nil || len(requestsOf(t, capture, "thread/revert")) != 1 {
		t.Fatal("explicit original observation rejected or resent", err)
	}
}

func TestRevertHistoryModeDriftAfterSendRetainsQuarantine(t *testing.T) {
	for _, mode := range []string{"paginated-result-mode-change", "paginated-current-mode-change"} {
		t.Run(mode, func(t *testing.T) {
			c, capture, source, ids, inputs := revertFixture(t, mode)
			claims := 0
			action := domain.NewID()
			if _, err := c.RevertThread(context.Background(), action, source, inputs[1], ids[1], func(RevertIntent) error { claims++; return nil }); err == nil || c.problem == nil || !c.execution.paused {
				t.Fatal("changed result/current history released a checkpoint")
			}
			if _, err := c.RevertThread(context.Background(), action, source, inputs[1], ids[1], func(RevertIntent) error { claims++; return nil }); err == nil || claims != 1 || len(requestsOf(t, capture, "thread/revert")) != 1 {
				t.Fatal("quarantined mutation was repeated")
			}
		})
	}
}

func TestNativeStateHistoryPreservesProfileAndBoundMode(t *testing.T) {
	for _, scenario := range []struct {
		name, fixture string
		mode          HistoryMode
		revert, valid bool
	}{
		{"selected-paginated", "paginated-ready", PaginatedHistory, true, true},
		{"selected-legacy", "ready", LegacyHistory, true, true},
		{"omitted-legacy", "ready", "", false, true},
		{"legacy-paginated", "ready", PaginatedHistory, false, false},
		{"selected-mode-drift", "paginated-ready", LegacyHistory, true, false},
		{"legacy-mode-drift", "ready", PaginatedHistory, true, false},
		{"selected-unknown", "paginated-ready", "unknown", true, false},
		{"legacy-unknown", "ready", "unknown", false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			c, capture, _, _, _ := revertFixture(t, scenario.fixture)
			c.revertHistory = scenario.revert
			fixtureSignal(t, c, "metadata", map[string]any{"historyMode": scenario.mode})
			err := c.checkNativeStateLocked(context.Background(), true)
			if (err == nil) != scenario.valid {
				t.Fatal("history profile or binding admission changed", err)
			}
			if err := c.inspectRetainedThreadLocked(context.Background()); (err == nil) != scenario.valid {
				t.Fatal("retained input inspection changed the history profile", err)
			}
			if len(requestsOf(t, capture, "thread/revert")) != 0 {
				t.Fatal("state observation gained mutation authority")
			}
		})
	}
}

func TestRevertSelectedPaginatedMetadataChangesRefusedBeforeClaim(t *testing.T) {
	for _, scenario := range []struct {
		field string
		value any
	}{
		{"historyMode", "unknown"},
		{"historyMode", "legacy"},
		{"cwd", "/foreign"},
		{"modelProvider", "foreign"},
		{"sessionId", domain.NewID()},
		{"parentThreadId", domain.NewID()},
		{"canAcceptDirectInput", false},
		{"status", map[string]any{"type": "active", "activeFlags": []any{}}},
	} {
		t.Run(scenario.field, func(t *testing.T) {
			c, capture, source, ids, inputs := revertFixture(t, "paginated-ready")
			fixtureSignal(t, c, "metadata", map[string]any{scenario.field: scenario.value})
			claims := 0
			if _, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[0], ids[0], func(RevertIntent) error { claims++; return nil }); err == nil || claims != 0 || len(requestsOf(t, capture, "thread/revert")) != 0 {
				t.Fatal("foreign original metadata reached claim or mutation")
			}
		})
	}
}

func TestNativeStateHistoryBindingRejectsUnselectedPagination(t *testing.T) {
	settings := threadSettings(t)
	threadID := domain.NewID()
	// Build the same closed response shape used by the wire fixture without a
	// live process; all unrelated native metadata and effective settings agree.
	thread := map[string]any{
		"id": threadID, "sessionId": threadID, "cliVersion": "0.162.0", "cwd": settings.Cwd,
		"modelProvider": settings.Provider, "createdAt": 1, "updatedAt": 1, "ephemeral": false,
		"preview": "", "projectId": nil, "source": "appServer", "status": map[string]any{"type": "idle"},
		"turns": []any{}, "historyMode": "paginated", "canAcceptDirectInput": true,
	}
	response, _ := json.Marshal(map[string]any{
		"thread": thread, "model": settings.Model, "modelProvider": settings.Provider,
		"cwd": settings.Cwd, "approvalPolicy": "on-request", "approvalsReviewer": "user",
		"sandbox": map[string]any{"type": "workspaceWrite"}, "reasoningEffort": settings.Effort,
		"serviceTier": settings.Options.ServiceTier, "instructionSources": []string{}, "runtimeWorkspaceRoots": []string{},
	})
	if _, _, err := decodeBoundThreadProfile(response, settings, threadID, resumeThread, "0.162.0", false); err == nil {
		t.Fatal("version alone admitted paginated history")
	}
	if _, _, err := decodeBoundThreadProfile(response, settings, threadID, resumeThread, "0.162.0", true); err != nil {
		t.Fatal("selected closed profile rejected paginated binding", err)
	}
}
