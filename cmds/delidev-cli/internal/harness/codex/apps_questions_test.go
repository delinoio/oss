// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"crypto/sha256"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"testing"
)

func appQuestionFixture(t *testing.T) (*Client, nativewire.Event) {
	t.Helper()
	thread, turn := domain.NewID(), domain.NewID()
	selection := domain.CodexAppConfiguration{Version: 1, SessionID: domain.NewID(), AccountID: domain.NewID(), Generation: domain.NewID(), AppIDs: []string{"original-app"}}
	c := &Client{appsProfile: true, thread: thread, managedHome: t.TempDir(), apps: &appsController{original: selection, current: selection}, execution: &executionState{turns: map[domain.ID]trackedTurn{turn: {Turn: Turn{ID: turn, Status: TurnRunning}}}}}
	started, _ := json.Marshal(originalAppCallFixture(false))
	if _, err := c.observeCodexAppCallLocked(nativewire.Event{Method: "item/started"}, turn, started); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{"threadId": thread, "turnId": turn, "itemId": "original-app-call", "isBlocking": true, "autoResolutionMs": nil, "questions": []any{map[string]any{"id": domain.CodexAppApprovalQuestionPrefix + "different-native-request", "header": "Approve app tool call?", "question": "Run the original app?", "isOther": false, "isSecret": false, "options": []any{map[string]any{"label": "Allow", "description": "Run once"}, map[string]any{"label": "Cancel", "description": "Do not run"}}}}})
	return c, nativewire.Event{Kind: nativewire.ServerRequest, Method: "item/tool/requestUserInput", ID: json.RawMessage(`11`), Token: domain.NewID(), Params: params}
}

func TestAppQuestionBindsOriginalCallAndRejectsUnownedApproval(t *testing.T) {
	c, native := appQuestionFixture(t)
	event, err := c.observeInteractionLocked(native)
	if err != nil || event.Interaction.Questions.CodexApp == nil || event.Interaction.Questions.CodexApp.NativeCallID != "original-app-call" {
		t.Fatalf("original approval: %v", err)
	}
	// Public question provenance cannot substitute the retained account or args.
	event.Interaction.Questions.CodexApp.Identity.Arguments[0] = '['
	owned := c.execution.interactions.arrivals[event.Interaction.ID]
	if !json.Valid(owned.questions.CodexApp.Identity.Arguments) {
		t.Fatal("approval authority shared a mutable public argument buffer")
	}
	for _, change := range []func(*Client, nativewire.Event) nativewire.Event{
		func(c *Client, n nativewire.Event) nativewire.Event { c.appsProfile = false; return n },
		func(c *Client, n nativewire.Event) nativewire.Event {
			delete(c.apps.calls, "original-app-call")
			return n
		},
		func(c *Client, n nativewire.Event) nativewire.Event {
			v := c.apps.calls["original-app-call"]
			v.completed = true
			c.apps.calls["original-app-call"] = v
			return n
		},
	} {
		c, n := appQuestionFixture(t)
		if _, err := c.observeInteractionLocked(change(c, n)); err == nil {
			t.Fatal("unowned app approval accepted")
		}
	}
}

func TestAppAcceptanceRequiresExactOneCallAllowAndSuccessfulOriginalResult(t *testing.T) {
	for _, label := range []string{"Allow", "Cancel"} {
		c, n := appQuestionFixture(t)
		e, err := c.observeInteractionLocked(n)
		if err != nil {
			t.Fatal(err)
		}
		owned := c.execution.interactions.arrivals[e.Interaction.ID]
		owned.status.ResponseID = domain.NewID()
		owned.status.Delivery = QuestionTransmitted
		raw, _ := json.Marshal(nativeQuestionResponse{Answers: map[string]nativeQuestionAnswer{owned.questions.Questions[0].ID: {Answers: []string{label}}}})
		owned.answerDigest = sha256.Sum256(raw)
		completed, _ := json.Marshal(originalAppCallFixture(true))
		tool, err := decodeCodexAppCall(completed, c.apps.original, true)
		if err != nil {
			t.Fatal(err)
		}
		status, err := c.confirmAppQuestionLocked(owned.status.TurnID, tool)
		if label == "Allow" {
			if err != nil || status == nil || !status.Accepted || status.QuestionEvidence != domain.NativeCodexAppResult {
				t.Fatalf("Allow result proof: %v", err)
			}
		} else if err == nil || owned.status.Accepted {
			t.Fatal("Cancel inferred accepted from successful external call")
		}
	}
	c, n := appQuestionFixture(t)
	e, err := c.observeInteractionLocked(n)
	if err != nil {
		t.Fatal(err)
	}
	owned := c.execution.interactions.arrivals[e.Interaction.ID]
	owned.status.ResponseID = domain.NewID()
	owned.status.Delivery = QuestionTransmitted
	call := c.apps.calls["original-app-call"]
	failed := &Tool{ID: "original-app-call", Status: ToolFailed, CodexApp: &domain.CodexAppCallObservation{Identity: call.identity}}
	if status, err := c.confirmAppQuestionLocked(owned.status.TurnID, failed); err != nil || status != nil || owned.status.Accepted {
		t.Fatal("failure inferred answer acceptance")
	}
}
