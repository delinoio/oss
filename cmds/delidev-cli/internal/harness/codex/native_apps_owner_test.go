// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"testing"
)

func sessionNativeAppsOwnerFixture(t *testing.T) (*Client, domain.ID, *decodedNativeAppsTool) {
	t.Helper()
	c, turn := observationClient()
	scope := nativeAppsScopeFixture()
	c.nativeApps = &domain.SessionNativeAppSelection{Scope: scope, InventoryID: domain.NewID(), Revision: 1, AppIDs: []string{"original"}}
	c.nativeAppsCaller = func(_ context.Context, _ domain.ID, method string, _ any) (nativewire.Response, error) {
		raw := `{"data":[{"id":"original","name":"Original App","isAccessible":true}],"nextCursor":null}`
		if method == "app/read" {
			raw = `{"apps":[{"id":"original","name":"Original App"}],"missingAppIds":[]}`
		}
		if method == "app/installed" {
			raw = `{"apps":[{"id":"original","enabled":true,"callable":true}]}`
		}
		return nativewire.Response{Result: json.RawMessage(raw)}, nil
	}
	return c, turn, &decodedNativeAppsTool{ID: "original-call", Server: "codex_apps", ToolName: "native_tool", AppID: "original", Arguments: json.RawMessage(`{"private":"original"}`), AppContext: json.RawMessage(`{"connectorId":"original"}`), Status: ToolRunning}
}
func sessionNativeAppsQuestionFixture(id string) *QuestionRequest {
	return &QuestionRequest{Blocking: true, Questions: []Question{{ID: "mcp_tool_call_approval_" + id, Header: "Approve app tool call?", Text: "Approve the original call?", Options: []QuestionOption{{Label: "Allow", Description: "Run the tool and continue."}, {Label: "Cancel", Description: "Cancel this tool call."}}}}}
}
func TestSessionNativeAppsOriginalLiveOwnerRejectsForeignAndDuplicateCalls(t *testing.T) {
	c, turn, source := sessionNativeAppsOwnerFixture(t)
	event := Event{Kind: ToolStartedEvent, TurnID: turn, ItemID: source.ID, Correlated: true, Tool: &Tool{Kind: NativeAppsTool, NativeApps: source}}
	if err := c.enrichNativeAppsEventLocked(context.Background(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Tool.Apps.Name != "Original App" || event.Tool.Apps.AppID != "original" {
		t.Fatal("canonical original identity lost")
	}
	if c.enrichNativeAppsEventLocked(context.Background(), &event) == nil {
		t.Fatal("duplicate start gained a new owner")
	}
	question := Event{Kind: InteractionRequestedEvent, TurnID: turn, ItemID: source.ID, Correlated: true, Interaction: &Interaction{Kind: UserInputInteraction, Questions: sessionNativeAppsQuestionFixture(source.ID)}}
	if c.enrichNativeAppsEventLocked(context.Background(), &question) != nil || question.Interaction.NativeApps == nil {
		t.Fatal("original native question lost proof")
	}
	foreign := question
	foreign.ItemID = "foreign-call"
	foreign.Interaction = &Interaction{Kind: UserInputInteraction, Questions: sessionNativeAppsQuestionFixture("foreign-call")}
	if c.enrichNativeAppsEventLocked(context.Background(), &foreign) == nil {
		t.Fatal("foreign question borrowed App ownership")
	}
	changed := *source
	changed.Arguments = json.RawMessage(`{"private":"foreign"}`)
	if sameNativeAppsIdentity(source, &changed) {
		t.Fatal("foreign arguments borrowed original owner")
	}
	changed = *source
	changed.AppContext = json.RawMessage(`{"connectorId":"foreign"}`)
	if sameNativeAppsIdentity(source, &changed) {
		t.Fatal("foreign connector borrowed original owner")
	}
}
func TestSessionNativeAppsOriginalClosedAnswerChoices(t *testing.T) {
	for _, answer := range []string{"Allow", "Cancel", "allow", "Allow for this session", "foreign"} {
		t.Run(answer, func(t *testing.T) {
			c, turn, source := sessionNativeAppsOwnerFixture(t)
			e := Event{Kind: ToolStartedEvent, TurnID: turn, Correlated: true, Tool: &Tool{Kind: NativeAppsTool, NativeApps: source}}
			if err := c.enrichNativeAppsEventLocked(context.Background(), &e); err != nil {
				t.Fatal(err)
			}
			q := sessionNativeAppsQuestionFixture(source.ID)
			owned := &trackedInteraction{status: InteractionStatus{TurnID: turn, ItemID: source.ID}, questions: q}
			err := c.nativeAppsAnswerChoice(owned, QuestionAnswers{Answers: map[string][]string{q.Questions[0].ID: {answer}}})
			if (err == nil) != (answer == "Allow" || answer == "Cancel") {
				t.Fatal("unknown choice acquired effect or cleanup", err)
			}
			if err == nil && c.nativeAppsAnswerChoice(owned, QuestionAnswers{Answers: map[string][]string{q.Questions[0].ID: {answer}}}) == nil {
				t.Fatal("original choice replayed")
			}
		})
	}
}

func TestSessionNativeAppsSharedHistoryCannotAcquireCallAuthority(t *testing.T) {
	item := nativeAppsCompletedFixture()
	raw := nativeAppsDecodeJSON(t, item)
	if !settledForkTool(raw, "mcpToolCall") || !managedForkItem(raw, "mcpToolCall") {
		t.Fatal("settled original App history rejected")
	}
	turn := fixtureTurn(domain.NewID(), TurnCompleted)
	turn["itemsView"] = "full"
	user := map[string]any{"type": "userMessage", "id": "original-user", "clientId": domain.NewID(), "content": []any{map[string]any{"type": "text", "text": "Original input", "text_elements": []any{}}}}
	for _, scenario := range []string{"original", "running", "foreign", "duplicate", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := nativeAppsCompletedFixture()
			switch scenario {
			case "running":
				candidate = nativeAppsDecodeFixture()
			case "foreign":
				candidate["server"] = "foreign"
			case "unknown":
				candidate["authority"] = true
			}
			items := []any{user, candidate}
			if scenario == "duplicate" {
				items = append(items, candidate)
			}
			turn["items"] = items
			page := nativeAppsDecodeJSON(t, map[string]any{"data": []any{turn}, "nextCursor": nil, "backwardsCursor": nil})
			_, inputs, err := decodeLatestTurnInputs(page)
			if (err == nil) != (scenario == "original") {
				t.Fatal("history eligibility diverged from closed decoder", err)
			}
			if scenario == "original" && len(inputs) != 1 {
				t.Fatal("original accepted input rewritten")
			}
		})
	}
	c, _, _ := sessionNativeAppsOwnerFixture(t)
	if len(c.nativeAppCalls) != 0 {
		t.Fatal("history decoding created a live call owner")
	}
}

func TestSessionNativeAppsOriginalPreapprovalFailureGrantsNoEffect(t *testing.T) {
	c, turn, source := sessionNativeAppsOwnerFixture(t)
	event := Event{Kind: ToolStartedEvent, TurnID: turn, Correlated: true, Tool: &Tool{Kind: NativeAppsTool, NativeApps: source}}
	if err := c.enrichNativeAppsEventLocked(context.Background(), &event); err != nil {
		t.Fatal(err)
	}
	terminal := *source
	terminal.Status = ToolFailed
	terminal.errorPresent = true
	event = Event{Kind: ToolCompletedEvent, TurnID: turn, Correlated: true, Tool: &Tool{Kind: NativeAppsTool, NativeApps: &terminal}}
	if err := c.enrichNativeAppsEventLocked(context.Background(), &event); err != nil {
		t.Fatal(err)
	}
	if c.nativeAppCalls[source.ID].choice != "" || event.Tool.Apps.Result != nil || !event.Tool.Apps.ErrorPresent {
		t.Fatal("failure manufactured approval or effects")
	}
	if c.enrichNativeAppsEventLocked(context.Background(), &event) == nil {
		t.Fatal("duplicate terminal observation accepted")
	}
}

func TestSessionNativeAppsUnselectedOrUnsupportedCallCannotReadOriginalInventory(t *testing.T) {
	for _, scenario := range []string{"unselected", "unknown-version", "unsupported-version"} {
		t.Run(scenario, func(t *testing.T) {
			c, turn, source := sessionNativeAppsOwnerFixture(t)
			calls := 0
			original := c.nativeAppsCaller
			c.nativeAppsCaller = func(ctx context.Context, id domain.ID, method string, params any) (nativewire.Response, error) {
				calls++
				return original(ctx, id, method, params)
			}
			switch scenario {
			case "unselected":
				source.AppID = "foreign"
			case "unknown-version":
				c.version = ""
			case "unsupported-version":
				c.version = "0.161.0"
			}
			event := Event{Kind: ToolStartedEvent, TurnID: turn, Correlated: true, Tool: &Tool{Kind: NativeAppsTool, NativeApps: source}}
			if c.enrichNativeAppsEventLocked(context.Background(), &event) == nil || calls != 0 || len(c.nativeAppCalls) != 0 {
				t.Fatal("foreign or unsupported original call borrowed native read authority")
			}
		})
	}
}
