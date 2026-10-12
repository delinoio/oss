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

func boundCurrentTimeFixture(t *testing.T, mode string) (*Client, string, ThreadResult, ThreadSettings) {
	t.Helper()
	c, capture, result, settings := boundTurnFixture(t, mode)
	// Initialization and StartThread leave these original notifications in wire
	// order. Verify both before testing the next time request or its metadata.
	for _, kind := range []MetadataKind{RemoteControlDisabled, ThreadIdentityChecked} {
		if event := nextKind(t, c, MetadataEvent); event.Metadata != kind {
			t.Fatalf("original startup observation missing: want %s, got %s", kind, event.Metadata)
		}
	}
	return c, capture, result, settings
}

func currentTimeRequest(t *testing.T, c *Client) nativewire.Event {
	t.Helper()
	fixtureSignal(t, c, "current-time", map[string]any{"requestId": 17, "params": map[string]any{"threadId": c.thread}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	event, err := c.wire.Next(ctx)
	if err != nil || event.Method != "currentTime/read" || event.Kind != nativewire.ServerRequest {
		t.Fatal("original time request missing", err)
	}
	return event
}

func TestCurrentTimeWholeWorkerSecondsDoNotAcknowledgeOrCompleteTurn(t *testing.T) {
	c, capture, _, _ := boundCurrentTimeFixture(t, "current-time")
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
	following, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal("ordinary following turn was blocked", err)
	}
	nextKind(t, c, MessageCompletedEvent)
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	if event := nextKind(t, c, TurnCompletedEvent); event.TurnID != following.TurnID || samples != 1 {
		t.Fatal("technical response changed the following turn")
	}
	nextKind(t, c, ThreadStatusEvent)
}

func TestCurrentTimeStaleReplacedAndStoppedRequestsNeverResample(t *testing.T) {
	c, _, _, _ := boundCurrentTimeFixture(t, "current-time")
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
	if _, err := c.replyCurrentTimeLocked(context.Background(), replacement); domain.SafeError(err).Code != domain.Conflict || samples != 1 {
		t.Fatal("fresh arrival token bypassed the answered-ID claim")
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

func currentTimeResponses(t *testing.T, capture string) []map[string]any {
	t.Helper()
	var responses []map[string]any
	for _, row := range capturedThreads(t, capture) {
		if row["method"] == "current-time-response" {
			responses = append(responses, row)
		}
	}
	return responses
}

func TestCurrentTimeFreshArrivalReplayThroughNextEventWritesOnce(t *testing.T) {
	c, capture, _, _ := boundCurrentTimeFixture(t, "current-time")
	samples := 0
	c.currentTimeClock = func() time.Time { samples++; return time.Unix(1700000000, 123) }
	signal := func() {
		fixtureSignal(t, c, "current-time", map[string]any{"requestId": 17, "params": map[string]any{"threadId": c.thread}})
	}
	signal()
	if event := nextKind(t, c, MetadataEvent); event.Metadata != CurrentTimeReplySent || samples != 1 {
		t.Fatal("original response was not sent")
	}
	// The fixture read barrier observes the first pipe write before re-emitting
	// the same original ID. Nativewire assigns the replacement a fresh token.
	signal()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.NextEvent(ctx); domain.SafeError(err).Code != domain.RecoveryRequired || samples != 1 || !c.execution.paused || c.problem == nil {
		t.Fatal("fresh-token replay escaped the original recovery fence", err)
	}
	fixtureSignal(t, c, "metadata", map[string]any{})
	if len(currentTimeResponses(t, capture)) != 1 || len(c.currentTimeClaims) != 1 {
		t.Fatal("replayed ID produced another pipe response or lost its claim")
	}
}

func TestCurrentTimeNumericAndTextIDsHaveSeparateClaims(t *testing.T) {
	c, capture, _, _ := boundCurrentTimeFixture(t, "current-time")
	samples := 0
	c.currentTimeClock = func() time.Time { samples++; return time.Unix(1700000000, 123) }
	for _, id := range []any{17, "17", int64(9223372036854775807), int64(-9223372036854775808)} {
		fixtureSignal(t, c, "current-time", map[string]any{"requestId": id, "params": map[string]any{"threadId": c.thread}})
		if event := nextKind(t, c, MetadataEvent); event.Metadata != CurrentTimeReplySent {
			t.Fatal("distinct native ID was not answered")
		}
	}
	fixtureSignal(t, c, "metadata", map[string]any{})
	responses := currentTimeResponses(t, capture)
	if samples != 4 || len(c.currentTimeClaims) != 4 || len(responses) != 4 || responses[0]["id"] != float64(17) || responses[1]["id"] != "17" {
		t.Fatal("numeric and text identities were conflated")
	}
}

func TestCurrentTimeClaimBoundNeverEvictsAnsweredIdentity(t *testing.T) {
	c, capture, _, _ := boundCurrentTimeFixture(t, "current-time")
	c.currentTimeClaims = make(map[currentTimeRequestKey]struct{}, maxCurrentTimeClaims)
	for i := 0; i < maxCurrentTimeClaims; i++ {
		c.currentTimeClaims[currentTimeRequestKey{kind: NumberRequestID, number: int64(1000 + i)}] = struct{}{}
	}
	samples := 0
	c.currentTimeClock = func() time.Time { samples++; return time.Now() }
	request := currentTimeRequest(t, c)
	if _, err := c.replyCurrentTimeLocked(context.Background(), request); domain.SafeError(err).Code != domain.ResourceExhausted || samples != 0 || len(c.currentTimeClaims) != maxCurrentTimeClaims {
		t.Fatal("claim bound evicted an ID or sampled another response", err)
	}
	// Feed this retained original arrival through the serialized controller to
	// prove exhaustion pauses execution without granting a replacement response.
	c.pendingEvent = &request
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.NextEvent(ctx); domain.SafeError(err).Code != domain.RecoveryRequired || !c.execution.paused || samples != 0 {
		t.Fatal("claim exhaustion did not fail closed", err)
	}
	fixtureSignal(t, c, "metadata", map[string]any{})
	if len(currentTimeResponses(t, capture)) != 0 {
		t.Fatal("exhausted ledger wrote a response")
	}
}

func TestCurrentTimeUnownedAndCanceledArrivalsDoNotClaimID(t *testing.T) {
	c, _, _, _ := boundCurrentTimeFixture(t, "current-time")
	request := currentTimeRequest(t, c)
	samples := 0
	c.currentTimeClock = func() time.Time { samples++; return time.Now() }
	forged := request
	forged.Token = domain.NewID()
	if _, err := c.replyCurrentTimeLocked(context.Background(), forged); domain.SafeError(err).Code != domain.Conflict || len(c.currentTimeClaims) != 0 || samples != 0 {
		t.Fatal("unowned arrival consumed the original ID", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.replyCurrentTimeLocked(ctx, request); err == nil || len(c.currentTimeClaims) != 0 || samples != 0 {
		t.Fatal("pre-canceled arrival consumed the original ID", err)
	}
	if event, err := c.replyCurrentTimeLocked(context.Background(), request); err != nil || event.Metadata != CurrentTimeReplySent || len(c.currentTimeClaims) != 1 || samples != 1 {
		t.Fatal("original unclaimed arrival was not preserved", err)
	}
}

func TestCurrentTimeRetiredArrivalNeverSamplesOrClaims(t *testing.T) {
	c, _, _, _ := boundCurrentTimeFixture(t, "current-time")
	request := currentTimeRequest(t, c)
	if retired, err := c.wire.RetireRequest(request); err != nil || !retired {
		t.Fatal("original arrival was not retired", err)
	}
	samples := 0
	c.currentTimeClock = func() time.Time { samples++; return time.Now() }
	if _, err := c.replyCurrentTimeLocked(context.Background(), request); domain.SafeError(err).Code != domain.Conflict || samples != 0 || len(c.currentTimeClaims) != 0 {
		t.Fatal("retired arrival acquired time authority", err)
	}
}

func TestCurrentTimePipeFailureRetainsIDClaimAndJoinsOriginalConnection(t *testing.T) {
	c, capture, _, _ := boundCurrentTimeFixture(t, "current-time-closed-input")
	request := currentTimeRequest(t, c)
	samples := 0
	c.currentTimeClock = func() time.Time { samples++; return time.Unix(1700000000, 123) }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := waitForTurnFixtureMarker(ctx, capture+".current-time-input-closed"); err != nil {
		t.Fatal("fixture did not close its original read pipe", err)
	}
	if _, err := c.replyCurrentTimeLocked(ctx, request); domain.SafeError(err).Code != domain.RecoveryRequired || samples != 1 || len(c.currentTimeClaims) != 1 {
		t.Fatal("failed pipe lost the original clock claim", err)
	}
	if _, err := c.replyCurrentTimeLocked(context.Background(), request); err == nil || samples != 1 || len(c.currentTimeClaims) != 1 {
		t.Fatal("uncertain response was resampled", err)
	}
	if err := c.Close(); err != nil || len(currentTimeResponses(t, capture)) != 0 {
		t.Fatal("failed original response was transmitted or cleanup was not joined", err)
	}
}
