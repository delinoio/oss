// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func (f *threadFixture) currentTimeReply(id, raw json.RawMessage) bool {
	var result currentTimeResponse
	if domain.Decode(raw, &result) != nil {
		return false
	}
	file, err := os.OpenFile(os.Getenv("DELIDEV_CODEX_CAPTURE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	err = json.NewEncoder(file).Encode(map[string]any{"method": "current-time-response", "id": id, "result": raw})
	return file.Close() == nil && err == nil
}

func currentTimeRequest(t *testing.T, c *Client) nativewire.Event {
	return currentTimeRequestFor(t, c, c.thread)
}

func currentTimeRequestFor(t *testing.T, c *Client, threadID domain.ID) nativewire.Event {
	t.Helper()
	fixtureSignal(t, c, "current-time", map[string]any{"requestId": 17, "params": map[string]any{"threadId": threadID}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		event, err := c.wire.Next(ctx)
		if err != nil {
			t.Fatal("original time request missing", err)
		}
		if event.Method == "currentTime/read" && event.Kind == nativewire.ServerRequest {
			return event
		}
		if event.Kind != nativewire.Notification {
			t.Fatalf("unexpected native event before original time request: kind %s", event.Kind)
		}
	}
}

func TestCurrentTimeOwnedDescendantUsesOriginalArrival(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "current-time")
	parent, child := domain.NewID(), domain.NewID()
	c.subagents = map[string]domain.SubagentObservation{
		string(parent): {ID: domain.NewID(), NativeID: string(parent), ParentID: string(c.thread), Status: domain.SubagentRunning},
		string(child):  {ID: domain.NewID(), NativeID: string(child), ParentID: string(parent), Status: domain.SubagentRunning},
	}
	samples := 0
	c.currentTimeClock = func() time.Time { samples++; return time.Unix(1700000000, 999999999) }
	request := currentTimeRequestFor(t, c, child)
	event, err := c.replyCurrentTimeLocked(context.Background(), request)
	if err != nil || event.Metadata != CurrentTimeReplySent || event.ThreadID != c.thread || event.Native != nil || !event.Correlated || samples != 1 {
		t.Fatal("owned descendant clock request was not answered as a private technical service", err)
	}
	fixtureSignal(t, c, "metadata", map[string]any{})
	responses := []map[string]any{}
	for _, row := range capturedThreads(t, capture) {
		if row["method"] == "current-time-response" {
			responses = append(responses, row)
		}
	}
	if len(responses) != 1 || responses[0]["id"] != float64(17) || !reflect.DeepEqual(responses[0]["result"], map[string]any{"currentTimeAt": float64(1700000000)}) {
		t.Fatal("owned descendant did not receive the exact original whole-second reply")
	}
}

func TestCurrentTimeWholeWorkerSecondsDoNotAcknowledgeOrCompleteTurn(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "current-time")
	accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	samples := 0
	c.currentTimeClock = func() time.Time { samples++; return time.Unix(1700000000, 999999999) }
	var logs bytes.Buffer
	c.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	settings, execution, inputs := c.execution.settings, c.execution, len(c.execution.inputs)
	fixtureSignal(t, c, "current-time", map[string]any{"requestId": 17, "params": map[string]any{"threadId": c.thread}})
	event := nextKind(t, c, MetadataEvent)
	if event.Metadata != CurrentTimeReplySent || event.Native != nil || event.Interaction != nil || event.InputID != "" || event.TurnID != "" || event.ThreadID != c.thread || !event.Correlated || samples != 1 {
		t.Fatal("technical response became public interaction or input evidence")
	}
	if c.execution != execution || c.execution.active != accepted.TurnID || c.execution.paused || c.problem != nil || len(c.execution.inputs) != inputs || !reflect.DeepEqual(settings, c.execution.settings) {
		t.Fatal("clock response changed original execution authority")
	}
	// This original read barrier observes the fixture's consumed pipe response;
	// it does not invent a native semantic-acceptance acknowledgment.
	fixtureSignal(t, c, "metadata", map[string]any{})
	responses := []map[string]any{}
	for _, row := range capturedThreads(t, capture) {
		if row["method"] == "current-time-response" {
			responses = append(responses, row)
		}
	}
	if len(responses) != 1 || responses[0]["id"] != float64(17) || !reflect.DeepEqual(responses[0]["result"], map[string]any{"currentTimeAt": float64(1700000000)}) {
		t.Fatal("same-ID whole-second response was not exact")
	}
	encoded, _ := json.Marshal(event)
	if bytes.Contains(encoded, []byte("1700000000")) || strings.Contains(logs.String(), "1700000000") || !strings.Contains(logs.String(), `"service":"current-time"`) || !strings.Contains(logs.String(), `"delivery":"sent"`) {
		t.Fatal("technical diagnostics lost their closed projection")
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	terminal := nextKind(t, c, TurnCompletedEvent)
	nextKind(t, c, ThreadStatusEvent)
	if terminal.TurnID != accepted.TurnID || c.problem != nil || c.execution.active != "" || len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 || samples != 1 {
		t.Fatal("clock response supplied original input/completion authority")
	}
}

func TestCurrentTimeStaleReplacedAndStoppedRequestsNeverResample(t *testing.T) {
	c, _, _, _ := boundTurnFixture(t, "current-time")
	samples := 0
	c.currentTimeClock = func() time.Time { samples++; return time.Unix(1700000000, 123) }
	request := currentTimeRequest(t, c)
	if event, err := c.replyCurrentTimeLocked(context.Background(), request); err != nil || event.Metadata != CurrentTimeReplySent || samples != 1 {
		t.Fatal("original request was not answered", err)
	}
	if _, err := c.replyCurrentTimeLocked(context.Background(), request); domain.SafeError(err).Code != domain.Conflict || samples != 1 {
		t.Fatal("answered request resampled the clock")
	}
	replacement := currentTimeRequest(t, c)
	if request.Token == replacement.Token {
		t.Fatal("fixture failed to replace request arrival")
	}
	forged := replacement
	forged.Token = request.Token
	if _, err := c.replyCurrentTimeLocked(context.Background(), forged); domain.SafeError(err).Code != domain.Conflict || samples != 1 {
		t.Fatal("replaced request obtained another sample")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.replyCurrentTimeLocked(context.Background(), replacement); err == nil || samples != 1 {
		t.Fatal("stopped connection sampled the clock")
	}
}

func TestCurrentTimeRejectsMalformedForeignAndWrongEnvelope(t *testing.T) {
	c, _ := observationClient()
	c.execution.paused = false
	samples := 0
	c.currentTimeClock = func() time.Time { samples++; return time.Now() }
	for _, raw := range []string{`null`, `{}`, `{"threadId":null}`, `{"threadId":1}`, `{"threadId":"foreign-private-text"}`, `{"threadId":"` + string(c.thread) + `","unknown":true}`, `{"threadId":"` + string(c.thread) + `","threadId":"` + string(c.thread) + `"}`, `{"ThreadId":"` + string(c.thread) + `"}`} {
		_, err := c.replyCurrentTimeLocked(context.Background(), nativewire.Event{Kind: nativewire.ServerRequest, Method: "currentTime/read", ID: json.RawMessage(`17`), Token: domain.NewID(), Params: json.RawMessage(raw)})
		if err == nil || samples != 0 {
			t.Fatal("malformed service request was admitted")
		}
	}
	params, _ := json.Marshal(map[string]any{"threadId": domain.NewID()})
	event, err := c.replyCurrentTimeLocked(context.Background(), nativewire.Event{Kind: nativewire.ServerRequest, Method: "currentTime/read", ID: json.RawMessage(`17`), Token: domain.NewID(), Params: params})
	if err != nil || event.Kind != NativeExtensionEvent || samples != 0 {
		t.Fatal("foreign thread received a clock response")
	}
	unownedChild := domain.NewID()
	missingParent := domain.NewID()
	c.subagents = map[string]domain.SubagentObservation{
		string(unownedChild): {ID: domain.NewID(), NativeID: string(unownedChild), ParentID: string(missingParent), Status: domain.SubagentRunning},
	}
	params, _ = json.Marshal(map[string]any{"threadId": unownedChild})
	event, err = c.replyCurrentTimeLocked(context.Background(), nativewire.Event{Kind: nativewire.ServerRequest, Method: "currentTime/read", ID: json.RawMessage(`18`), Token: domain.NewID(), Params: params})
	if err != nil || event.Kind != NativeExtensionEvent || samples != 0 {
		t.Fatal("child with no ownership path to the root received a clock response")
	}
	params, _ = json.Marshal(map[string]any{"threadId": c.thread})
	request := nativewire.Event{Kind: nativewire.Notification, Method: "currentTime/read", Params: params}
	if event, err = c.replyCurrentTimeLocked(context.Background(), request); err != nil || event.Kind != NativeExtensionEvent || samples != 0 {
		t.Fatal("notification obtained request authority")
	}
	request.Kind, request.ID, request.Token = nativewire.ServerRequest, json.RawMessage(`17`), domain.NewID()
	if event, err = (&Client{}).replyCurrentTimeLocked(context.Background(), request); err != nil || event.Kind != NativeExtensionEvent || samples != 0 {
		t.Fatal("unbound execution obtained clock authority")
	}
	c.execution.paused = true
	if _, err = c.replyCurrentTimeLocked(context.Background(), request); err == nil || samples != 0 {
		t.Fatal("paused execution acquired service authority")
	}
	if validationStage("currentTime/read") != validationService {
		t.Fatal("service diagnostic classification changed")
	}
}
