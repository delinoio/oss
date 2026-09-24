package codex

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func (f *threadFixture) notify(method string, params any) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"method": method, "params": params})
}
func fixtureTurn(id domain.ID, status TurnStatus) map[string]any {
	result := map[string]any{"id": id, "items": []any{}, "itemsView": "notLoaded", "status": status, "startedAt": nil, "completedAt": nil, "durationMs": nil, "error": nil}
	if status == TurnFailed {
		result["error"] = map[string]any{"message": "fixture-protected-diagnostic", "additionalDetails": "fixture-protected-details"}
	}
	return result
}
func (f *threadFixture) handleTurn(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if !strings.HasPrefix(method, "turn/") && !strings.HasPrefix(method, "fixture/") {
		return false
	}
	if f.thread == nil {
		os.Exit(30)
	}
	var params map[string]any
	if json.Unmarshal(raw, &params) != nil {
		os.Exit(31)
	}
	if file := os.Getenv("DELIDEV_CODEX_CAPTURE"); file != "" {
		out, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(32)
		}
		_ = json.NewEncoder(out).Encode(map[string]any{"method": method, "params": params})
		_ = out.Close()
	}
	reject := func() {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": id, "error": map[string]any{"code": -32602, "message": "fixture-protected-rejection"}})
	}
	late := func() { time.Sleep(200 * time.Millisecond) }
	switch method {
	case "fixture/question":
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": params["requestId"], "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": f.thread["id"], "turnId": f.turn, "itemId": "question-tool", "isBlocking": true, "questions": params["questions"]}})
		write(id, map[string]any{})
		if f.mode == "thread-turn-questions-blocked" {
			// The owned fixture intentionally stops reading its pipe so a large
			// answer is canceled after native delivery becomes uncertain.
			time.Sleep(30 * time.Second)
		}
	case "fixture/notify":
		f.notify(params["method"].(string), params["params"])
		write(id, map[string]any{})
	case "turn/start":
		if f.mode == "thread-turn-start-reject" {
			reject()
			return true
		}
		f.turn = domain.NewID()
		f.turnInput = domain.ID(params["clientUserMessageId"].(string))
		f.thread["status"] = map[string]any{"type": "active", "activeFlags": []string{}}
		if f.mode == "thread-turn-late-start" {
			f.notify("turn/started", map[string]any{"threadId": f.thread["id"], "turn": fixtureTurn(f.turn, TurnRunning)})
			late()
		}
		response := map[string]any{"turn": fixtureTurn(f.turn, TurnRunning)}
		if f.mode == "thread-turn-malformed" {
			response["unknownMeaning"] = true
		}
		write(id, response)
		f.notify("turn/started", map[string]any{"threadId": f.thread["id"], "turn": fixtureTurn(f.turn, TurnRunning)})
		content := []any{map[string]any{"type": "text", "text": params["input"].([]any)[0].(map[string]any)["text"], "text_elements": []any{}}}
		item := map[string]any{"type": "userMessage", "id": "native-user-item", "clientId": f.turnInput, "content": content}
		f.notify("item/completed", map[string]any{"threadId": f.thread["id"], "turnId": f.turn, "completedAtMs": 1, "item": item})

	case "turn/steer":
		f.steerCount++
		if params["expectedTurnId"] != string(f.turn) || (f.mode == "thread-turn-steer-reject" && f.steerCount == 1) {
			reject()
			return true
		}
		if f.mode == "thread-turn-late-steer" {
			late()
		}
		accepted := f.turn
		if f.mode == "thread-turn-wrong-steer" {
			accepted = domain.NewID()
		}
		write(id, map[string]any{"turnId": accepted})
	case "turn/interrupt":
		if params["turnId"] != string(f.turn) {
			reject()
			return true
		}
		if f.mode == "thread-turn-late-interrupt" {
			late()
		}
		write(id, map[string]any{})
	case "fixture/finish":
		status := TurnStatus(params["status"].(string))
		turn := f.turn
		if selected, ok := params["turnId"].(string); ok {
			turn = domain.ID(selected)
		}
		f.notify("turn/completed", map[string]any{"threadId": f.thread["id"], "turn": fixtureTurn(turn, status)})
		f.thread["status"] = map[string]any{"type": "idle"}
		f.notify("thread/status/changed", map[string]any{"threadId": f.thread["id"], "status": f.thread["status"]})
		write(id, map[string]any{})
	case "fixture/started":
		turn := f.turn
		if selected, ok := params["turnId"].(string); ok {
			turn = domain.ID(selected)
		}
		f.notify("turn/started", map[string]any{"threadId": f.thread["id"], "turn": fixtureTurn(turn, TurnRunning)})
		write(id, map[string]any{})
	case "fixture/waiting":
		f.thread["status"] = map[string]any{"type": "active", "activeFlags": []string{"waitingOnApproval"}}
		write(id, map[string]any{})
	case "fixture/delta":
		thread := f.thread["id"]
		if params["foreign"] == true {
			thread = domain.NewID()
		}
		f.notify("item/agentMessage/delta", map[string]any{"threadId": thread, "turnId": f.turn, "itemId": "native-agent-item", "delta": "한글 🐦"})
		f.notify("item/completed", map[string]any{"threadId": thread, "turnId": f.turn, "completedAtMs": 1, "item": map[string]any{"type": "agentMessage", "id": "native-agent-item", "text": "한글 🐦", "phase": "final_answer", "delivery": nil, "memoryCitation": nil}})
		write(id, map[string]any{})
	default:
		os.Exit(33)
	}
	return true
}
func boundTurnFixture(t *testing.T, mode string) (*Client, string, ThreadResult, ThreadSettings) {
	t.Helper()
	client, capture := openThreadFixture(t, "thread-turn-"+mode)
	settings := threadSettings(t)
	result, err := client.StartThread(context.Background(), domain.NewID(), settings)
	if err != nil {
		t.Fatal(err)
	}
	return client, capture, result, settings
}
func input(mode domain.SessionMode) domain.SessionInput {
	return domain.SessionInput{Mode: mode, Prompt: "Fixture prompt 한글 🐦"}
}
func fixtureSignal(t *testing.T, c *Client, method string, params any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.wire.Call(ctx, domain.NewID(), "fixture/"+method, params); err != nil {
		t.Fatal(err)
	}
}
func nextKind(t *testing.T, c *Client, kind EventKind) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		event, err := c.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == kind {
			return event
		}
	}
}
func assertCode(t *testing.T, err error, code domain.Code) {
	t.Helper()
	if err == nil || domain.SafeError(err).Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}
func requestsOf(t *testing.T, capture, method string) []map[string]any {
	t.Helper()
	var result []map[string]any
	for _, row := range capturedThreads(t, capture) {
		if row["method"] == method {
			result = append(result, row["params"].(map[string]any))
		}
	}
	return result
}
func TestTurnStartsOnlyWhenIdleAndPinsEffectiveSettings(t *testing.T) {
	client, capture, bound, settings := boundTurnFixture(t, "ready")
	bound.Effective.Model = "changed-by-caller"
	*bound.Effective.Effort = "low"
	bound.Effective.Sandbox.Type = FullAccess
	inputID := domain.NewID()
	result, err := client.StartTurn(context.Background(), domain.NewID(), inputID, input(domain.PlanMode))
	if err != nil {
		t.Fatal(err)
	}
	if result.TurnID.Validate() != nil || result.InputID != inputID {
		t.Fatal("native input mapping missing")
	}
	_, err = client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	assertCode(t, err, domain.Conflict)
	params := requestsOf(t, capture, "turn/start")
	if len(params) != 1 {
		t.Fatal("a queued input was silently steered")
	}
	p := params[0]
	mode := p["collaborationMode"].(map[string]any)
	nativeSettings := mode["settings"].(map[string]any)
	if p["clientUserMessageId"] != string(inputID) || p["model"] != settings.Model || p["effort"] != settings.Effort || p["cwd"] != settings.Cwd || p["approvalPolicy"] != "on-request" || p["approvalsReviewer"] != "user" || p["serviceTier"] != "fast" || p["sandboxPolicy"].(map[string]any)["type"] != "workspaceWrite" || mode["mode"] != "plan" || nativeSettings["model"] != settings.Model || nativeSettings["reasoning_effort"] != settings.Effort || nativeSettings["developer_instructions"] != nil {
		t.Fatal("native turn settings were not preserved")
	}
	started := nextKind(t, client, TurnStartedEvent)
	if !started.Correlated || started.TurnID != result.TurnID {
		t.Fatal("turn start lost correlation")
	}
	user := nextKind(t, client, MessageCompletedEvent)
	if user.Message == nil || user.Message.Role != UserRole || user.Message.ClientInputID != inputID || user.Message.Text != input(domain.PlanMode).Prompt {
		t.Fatal("user input identity or content lost")
	}
	fixtureSignal(t, client, "delta", map[string]any{})
	delta := nextKind(t, client, TextDeltaEvent)
	if delta.TextDelta != "한글 🐦" || delta.Native != nil {
		t.Fatal("typed Unicode delta changed")
	}
	answer := nextKind(t, client, MessageCompletedEvent)
	if answer.Message == nil || answer.Message.Phase == nil || *answer.Message.Phase != FinalAnswerPhase || answer.Message.Text != delta.TextDelta {
		t.Fatal("native final message changed")
	}
	fixtureSignal(t, client, "finish", map[string]any{"status": TurnCompleted})
	completed := nextKind(t, client, TurnCompletedEvent)
	if completed.Turn.Status != TurnCompleted || !completed.Correlated {
		t.Fatal("terminal state lost")
	}
	_, err = client.StartTurn(context.Background(), domain.NewID(), inputID, input(domain.ExecuteMode))
	assertCode(t, err, domain.Conflict)
	_, err = client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	if len(requestsOf(t, capture, "turn/start")) != 2 {
		t.Fatal("idle follow-up did not start exactly once")
	}
}
func TestSteerKeepsExpectedTurnModeAndRetainsDefiniteRejection(t *testing.T) {
	client, capture, _, _ := boundTurnFixture(t, "steer-reject")
	first, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Steer(context.Background(), domain.NewID(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	assertCode(t, err, domain.Conflict)
	_, err = client.Steer(context.Background(), domain.NewID(), domain.NewID(), first.TurnID, input(domain.PlanMode))
	assertCode(t, err, domain.Conflict)
	inputID := domain.NewID()
	_, err = client.Steer(context.Background(), domain.NewID(), inputID, first.TurnID, input(domain.ExecuteMode))
	assertCode(t, err, domain.Conflict)
	result, err := client.Steer(context.Background(), domain.NewID(), inputID, first.TurnID, input(domain.ExecuteMode))
	if err != nil || result.TurnID != first.TurnID {
		t.Fatalf("rejected input lost eligibility: %v", err)
	}
	params := requestsOf(t, capture, "turn/steer")
	if len(params) != 2 {
		t.Fatal("stale/mode rejection reached native steer")
	}
	for _, p := range params {
		if len(p) != 4 || p["expectedTurnId"] != string(first.TurnID) || p["clientUserMessageId"] != string(inputID) {
			t.Fatal("steer changed native configuration")
		}
	}
	fixtureSignal(t, client, "waiting", map[string]any{})
	_, err = client.Steer(context.Background(), domain.NewID(), domain.NewID(), first.TurnID, input(domain.ExecuteMode))
	assertCode(t, err, domain.Conflict)
	if len(requestsOf(t, capture, "turn/steer")) != 2 {
		t.Fatal("ordinary input bypassed a pending interaction")
	}
}
func TestInterruptRequiresTerminalEvidenceAndLeavesInputPaused(t *testing.T) {
	client, capture, _, _ := boundTurnFixture(t, "ready")
	first, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Interrupt(context.Background(), domain.NewID(), domain.NewID())
	assertCode(t, err, domain.Conflict)
	_, err = client.Interrupt(context.Background(), domain.NewID(), first.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	if client.execution.active != first.TurnID {
		t.Fatal("interrupt acknowledgment fabricated terminal proof")
	}
	_, err = client.Interrupt(context.Background(), domain.NewID(), first.TurnID)
	assertCode(t, err, domain.Conflict)
	_, err = client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	assertCode(t, err, domain.Conflict)
	fixtureSignal(t, client, "finish", map[string]any{"status": TurnInterrupted})
	event := nextKind(t, client, TurnCompletedEvent)
	if event.Turn.Status != TurnInterrupted {
		t.Fatal("interruption was promoted to success")
	}
	_, err = client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	assertCode(t, err, domain.Conflict)
	if len(requestsOf(t, capture, "turn/interrupt")) != 1 || len(requestsOf(t, capture, "turn/start")) != 1 {
		t.Fatal("stop caused a duplicate native operation")
	}
}
func TestLateTurnAcknowledgmentsDoNotAuthorizeReplay(t *testing.T) {
	for _, action := range []string{"start", "steer", "interrupt"} {
		t.Run(action, func(t *testing.T) {
			client, capture, _, _ := boundTurnFixture(t, "late-"+action)
			var turnID domain.ID
			if action != "start" {
				result, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
				if err != nil {
					t.Fatal(err)
				}
				turnID = result.TurnID
				nextKind(t, client, TurnStartedEvent)
			}
			requestID, inputID := domain.NewID(), domain.NewID()
			ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
			defer cancel()
			var err error
			switch action {
			case "start":
				_, err = client.StartTurn(ctx, requestID, inputID, input(domain.ExecuteMode))
			case "steer":
				_, err = client.Steer(ctx, requestID, inputID, turnID, input(domain.ExecuteMode))
			case "interrupt":
				_, err = client.Interrupt(ctx, requestID, turnID)
			}
			assertCode(t, err, domain.RecoveryRequired)
			event := nextKind(t, client, LateTurnResponseEvent)
			if event.RequestID != requestID || event.TurnID.Validate() != nil || event.Problem != nil || !event.Correlated {
				t.Fatal("late acceptance lost original identity")
			}
			_, err = client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			assertCode(t, err, domain.RecoveryRequired)
			if len(requestsOf(t, capture, "turn/"+action)) != 1 {
				t.Fatal("uncertain request was repeated")
			}
		})
	}
}
func TestNativeTurnFailureCannotBeReversedByLateIdleOrStartedEvents(t *testing.T) {
	client, _, _, _ := boundTurnFixture(t, "ready")
	first, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, client, TurnStartedEvent)
	fixtureSignal(t, client, "finish", map[string]any{"status": TurnFailed})
	event := nextKind(t, client, TurnCompletedEvent)
	raw, _ := json.Marshal(event)
	if strings.Contains(string(raw), "fixture-protected") || event.Turn.Problem == nil || event.Native != nil {
		t.Fatal("native error body escaped normalized event")
	}
	nextKind(t, client, ThreadStatusEvent)
	fixtureSignal(t, client, "started", map[string]any{"turnId": first.TurnID})
	late := nextKind(t, client, TurnStartedEvent)
	if !late.Late {
		t.Fatal("old running event was not marked late")
	}
	_, err = client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	assertCode(t, err, domain.Conflict)
	fixtureSignal(t, client, "finish", map[string]any{"status": TurnCompleted, "turnId": first.TurnID})
	_, err = client.NextEvent(context.Background())
	assertCode(t, err, domain.RecoveryRequired)
	if client.execution.turns[first.TurnID].Turn.Status != TurnFailed {
		t.Fatal("late completion changed failed outcome")
	}
}
func TestCanceledEventReaderRetainsConsumedTerminalObservation(t *testing.T) {
	client, _, _, _ := boundTurnFixture(t, "ready")
	result, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, client, MessageCompletedEvent)
	fixtureSignal(t, client, "finish", map[string]any{"status": TurnCompleted})
	client.control <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = client.NextEvent(ctx)
	assertCode(t, err, domain.Unavailable)
	<-client.control
	terminal := nextKind(t, client, TurnCompletedEvent)
	if terminal.TurnID != result.TurnID {
		t.Fatal("cancellation lost or replaced terminal event")
	}
}
func TestMalformedAcceptedTurnOrWrongSteerBlocksAllFurtherInput(t *testing.T) {
	for _, mode := range []string{"malformed", "wrong-steer"} {
		t.Run(mode, func(t *testing.T) {
			client, capture, _, _ := boundTurnFixture(t, mode)
			turn, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			if mode == "wrong-steer" {
				if err != nil {
					t.Fatal(err)
				}
				_, err = client.Steer(context.Background(), domain.NewID(), domain.NewID(), turn.TurnID, input(domain.ExecuteMode))
			}
			assertCode(t, err, domain.RecoveryRequired)
			_, err = client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			assertCode(t, err, domain.RecoveryRequired)
			if len(requestsOf(t, capture, "turn/start")) != 1 {
				t.Fatal("invalid acknowledgment authorized replacement")
			}
		})
	}
}

func TestForeignNativeEventsStayPrivateAndCannotChangeRootState(t *testing.T) {
	client, _, _, _ := boundTurnFixture(t, "ready")
	first, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, client, MessageCompletedEvent)
	fixtureSignal(t, client, "delta", map[string]any{"foreign": true})
	event, err := client.NextEvent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != NativeExtensionEvent || event.Native == nil || event.Correlated {
		t.Fatal("foreign native event was attributed to root execution")
	}
	raw, err := json.Marshal(event)
	if err != nil || strings.Contains(string(raw), "한글") || strings.Contains(string(raw), "native-agent-item") || strings.Contains(string(raw), "Params") {
		t.Fatal("private native extension was serialized")
	}
	if client.execution.active != first.TurnID {
		t.Fatal("foreign event changed root turn")
	}
}

func TestAcceptedInputIdentityCannotBeReboundToDifferentContentOrTurn(t *testing.T) {
	client, _, _, _ := boundTurnFixture(t, "ready")
	inputID := domain.NewID()
	first, err := client.StartTurn(context.Background(), domain.NewID(), inputID, input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, client, MessageCompletedEvent)
	fixtureSignal(t, client, "finish", map[string]any{"status": TurnCompleted})
	nextKind(t, client, TurnCompletedEvent)
	second, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		turnID domain.ID
		text   string
	}{
		{"content", first.TurnID, "different native content"},
		{"turn", second.TurnID, input(domain.ExecuteMode).Prompt},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"threadId": client.thread, "turnId": test.turnID, "completedAtMs": 1, "item": map[string]any{"type": "userMessage", "id": "native-user-item", "clientId": inputID, "content": []any{map[string]any{"type": "text", "text": test.text}}}})
			if err := client.acquireControl(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, err := client.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "item/completed", Params: raw})
			<-client.control
			if err == nil {
				t.Fatal("a native client input identity replaced its accepted content or turn")
			}
		})
	}
}

func TestRetainedTerminalEventCannotOutliveNativeConnectionFailure(t *testing.T) {
	client, _, _, _ := boundTurnFixture(t, "ready")
	result, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, client, MessageCompletedEvent)
	fixtureSignal(t, client, "finish", map[string]any{"status": TurnCompleted})
	client.control <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = client.NextEvent(ctx)
	assertCode(t, err, domain.Unavailable)
	<-client.control
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = client.NextEvent(context.Background())
	assertCode(t, err, domain.Canceled)
	if client.execution.turns[result.TurnID].Turn.Status != TurnRunning {
		t.Fatal("a dead connection's buffered event changed accepted execution state")
	}
}
