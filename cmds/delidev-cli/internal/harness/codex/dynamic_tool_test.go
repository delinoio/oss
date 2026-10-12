// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

const dynamicStartedFixture = `{"type":"dynamicToolCall","id":"dynamic-call","tool":"fixture_tool","namespace":null,"arguments":{"private":"private-argument-sentinel","integer":9007199254740993},"status":"inProgress","contentItems":null,"success":null,"durationMs":null}`

var dynamicCompletedFixture = strings.Replace(strings.Replace(strings.Replace(strings.Replace(dynamicStartedFixture, `"inProgress"`, `"completed"`, 1), `"contentItems":null`, `"contentItems":[{"type":"inputText","text":"<script>inert()</script>"},{"type":"inputImage","imageUrl":"private-image-sentinel"},{"type":"inputAudio","audioUrl":"private-audio-sentinel"},{"type":"inputText","text":"last"}]`, 1), `"success":null`, `"success":false`, 1), `"durationMs":null`, `"durationMs":9007199254740993`, 1)

func dynamicItemEnvelopeFixture(c *Client, turn domain.ID, complete bool, raw string) map[string]any {
	params := map[string]any{"threadId": c.thread, "turnId": turn, "item": json.RawMessage(raw)}
	if complete {
		params["completedAtMs"] = 12
	} else {
		params["startedAtMs"] = 1
	}
	return params
}

func dynamicRequestFixture(c *Client, turn domain.ID, id json.RawMessage) nativewire.Event {
	var item map[string]json.RawMessage
	_ = json.Unmarshal([]byte(dynamicStartedFixture), &item)
	raw, _ := json.Marshal(map[string]any{"threadId": c.thread, "turnId": turn, "callId": "dynamic-call", "tool": "fixture_tool", "namespace": nil, "arguments": item["arguments"]})
	return nativewire.Event{Kind: nativewire.ServerRequest, Method: "item/tool/call", ID: id, Token: domain.NewID(), Params: raw}
}
func TestDynamicToolClosedCamelCaseUnionAndProtectedProjection(t *testing.T) {
	id, s, err := decodeDynamicTool([]byte(dynamicCompletedFixture), true)
	if err != nil || id != "dynamic-call" || s.Success == nil || *s.Success || s.DurationMS == nil || *s.DurationMS != 9007199254740993 || s.Content == nil || len(*s.Content) != 4 {
		t.Fatal("original native outcome/order lost", err)
	}
	raw, _ := json.Marshal(s)
	for _, sentinel := range []string{"private-argument-sentinel", "private-image-sentinel", "private-audio-sentinel"} {
		if bytes.Contains(raw, []byte(sentinel)) {
			t.Fatal("protected content escaped")
		}
	}
	if (*s.Content)[0].Text == nil || *(*s.Content)[0].Text != "<script>inert()</script>" || (*s.Content)[1].Kind != domain.DynamicImage || (*s.Content)[2].Kind != domain.DynamicAudio || *(*s.Content)[3].Text != "last" {
		t.Fatal("ordered content changed")
	}
	for _, raw := range []string{
		strings.Replace(dynamicStartedFixture, `"namespace":null,`, "", 1),
		strings.Replace(dynamicStartedFixture, `"id":"dynamic-call"`, `"id":"dynamic-call","id":"duplicate"`, 1),
		strings.Replace(dynamicStartedFixture, `"arguments":{`, `"Arguments":{`, 1),
		strings.Replace(dynamicCompletedFixture, `"inputText"`, `"input_text"`, 1),
		strings.Replace(dynamicCompletedFixture, `"imageUrl"`, `"image_url"`, 1),
		strings.Replace(dynamicCompletedFixture, `"imageUrl":"private-image-sentinel"`, `"imageUrl":null`, 1),
		strings.Replace(dynamicCompletedFixture, `"success":false`, `"success":"false"`, 1),
		strings.Replace(dynamicCompletedFixture, `"completed"`, `"unknown"`, 1),
		strings.Replace(dynamicCompletedFixture, `"durationMs":9007199254740993`, `"durationMs":-1`, 1),
		strings.Replace(dynamicCompletedFixture, `"text":"last"`, `"text":"\ud800"`, 1),
		strings.Replace(dynamicCompletedFixture, `"text":"last"`, `"text":"`+strings.Repeat("x", domain.MaxMessageText+1)+`"`, 1),
	} {
		if _, _, err := decodeDynamicTool([]byte(raw), true); err == nil {
			t.Fatal("ambiguous or excessive dynamic item accepted")
		}
	}
}
func TestDynamicRequestRequiresOriginalStartedIdentity(t *testing.T) {
	for _, change := range []string{"namespace", "tool", "arguments", "turn", "duplicate", "question-id", "sidechat"} {
		t.Run(change, func(t *testing.T) {
			c, turn := observationClient()
			c.dynamicToolsEnabled = true
			c.mode = ThreadProtocol
			c.execution.paused = false
			if _, err := observeFixture(c, "item/started", dynamicItemEnvelopeFixture(c, turn, false, dynamicStartedFixture)); err != nil {
				t.Fatal(err)
			}
			e := dynamicRequestFixture(c, turn, []byte(`7`))
			switch change {
			case "namespace":
				e.Params = bytes.Replace(e.Params, []byte(`"namespace":null`), []byte(`"namespace":"foreign"`), 1)
			case "tool":
				e.Params = bytes.Replace(e.Params, []byte(`fixture_tool`), []byte(`foreign`), 1)
			case "arguments":
				e.Params = bytes.Replace(e.Params, []byte(`9007199254740993`), []byte(`9007199254740992`), 1)
			case "turn":
				e.Params = bytes.Replace(e.Params, []byte(turn), []byte(domain.NewID()), 1)
			case "duplicate":
				if _, err := c.observeDynamicRequestLocked(e); err != nil {
					t.Fatal(err)
				}
			case "question-id":
				c.execution.interactions.native = map[string]domain.ID{"n:7": domain.NewID()}
			case "sidechat":
				c.sidechat = ReadOnlySidechatV1
			}
			observed, err := c.observeDynamicRequestLocked(e)
			if change == "sidechat" {
				if err != nil || observed.Kind != NativeExtensionEvent {
					t.Fatal("Sidechat obtained responder")
				}
			} else if err == nil {
				t.Fatal("foreign request obtained reply authority")
			}
		})
	}
}
func (f *threadFixture) dynamicReply(id, raw json.RawMessage) bool {
	var reply dynamicUnavailableResponse
	if domain.Decode(raw, &reply) != nil || reply.Success || len(reply.Content) != 1 || reply.Content[0].Type != domain.DynamicText || reply.Content[0].Text != domain.DynamicToolUnavailableText {
		return false
	}
	file, err := os.OpenFile(os.Getenv("DELIDEV_CODEX_CAPTURE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return false
	}
	err = json.NewEncoder(file).Encode(map[string]any{"method": "dynamic-response", "id": id, "result": raw})
	return file.Close() == nil && err == nil
}
func ownedDynamicFixture(t *testing.T, id any) (*Client, string, domain.ID, Event) {
	t.Helper()
	c, capture, _, _ := boundTurnFixture(t, "dynamic")
	c.dynamicToolsEnabled = true
	accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	fixtureSignal(t, c, "notify", map[string]any{"method": "item/started", "params": dynamicItemEnvelopeFixture(c, accepted.TurnID, false, dynamicStartedFixture)})
	nextKind(t, c, DynamicToolEvent)
	nativeID, _ := json.Marshal(id)
	request := dynamicRequestFixture(c, accepted.TurnID, nativeID)
	fixtureSignal(t, c, "dynamic", map[string]any{"requestId": id, "params": json.RawMessage(request.Params)})
	return c, capture, accepted.TurnID, nextKind(t, c, DynamicToolEvent)
}
func TestDynamicUnavailableOneOriginalNegativeReplyAndIndependentResolution(t *testing.T) {
	for _, id := range []any{json.Number("7"), "7", json.Number("9223372036854775807")} {
		t.Run(stringMustJSON(id), func(t *testing.T) {
			c, capture, turn, event := ownedDynamicFixture(t, id)
			token := event.DynamicTool.Request.Token
			claim := domain.NewID()
			commits := 0
			reply, err := c.ReplyDynamicUnavailable(context.Background(), token, turn, claim, func() error { commits++; return nil })
			if err != nil || reply.Delivery != domain.DynamicTransmitted || commits != 1 || c.execution.active != turn || c.dynamicArrivals[token].Resolved {
				t.Fatal("reply changed independent authority", err)
			}
			if _, err := c.ReplyDynamicUnavailable(context.Background(), token, turn, domain.NewID(), func() error { commits++; return nil }); err == nil || commits != 1 {
				t.Fatal("request replay repeated reply")
			}
			fixtureSignal(t, c, "metadata", map[string]any{})
			rows := capturedDynamicReplies(t, capture)
			if len(rows) != 1 {
				t.Fatal("negative reply attempt repeated")
			}
			wireID := rows[0]["id"]
			if string(wireID) != stringMustJSON(id) {
				t.Fatal("wire request kind/precision lost")
			}
			fixtureSignal(t, c, "notify", map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": c.thread, "requestId": id}})
			resolved := nextKind(t, c, DynamicToolEvent)
			if resolved.DynamicTool.Stage != domain.DynamicResolved || resolved.DynamicTool.Token != token || c.execution.active != turn {
				t.Fatal("resolution became turn completion")
			}
			fixtureSignal(t, c, "notify", map[string]any{"method": "item/completed", "params": dynamicItemEnvelopeFixture(c, turn, true, dynamicCompletedFixture)})
			completed := nextKind(t, c, DynamicToolEvent)
			if completed.DynamicTool.Snapshot.Success == nil || *completed.DynamicTool.Snapshot.Success || c.execution.active != turn {
				t.Fatal("negative result became root failure")
			}
		})
	}
}
func stringMustJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }
func TestDynamicCanceledAndCommitLossNeverRepeatAttempt(t *testing.T) {
	for _, stage := range []string{"before", "after-claim", "after-send"} {
		t.Run(stage, func(t *testing.T) {
			c, _, turn, event := ownedDynamicFixture(t, json.Number("7"))
			token := event.DynamicTool.Request.Token
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			commits := 0
			if stage == "before" {
				cancel()
			}
			reply, err := c.ReplyDynamicUnavailable(ctx, token, turn, domain.NewID(), func() error {
				commits++
				if stage == "after-claim" {
					return errors.New("fixture durable write failure")
				}
				return nil
			})
			if stage == "before" && (err == nil || commits != 0 || reply.Delivery != domain.DynamicNotSent) || stage == "after-claim" && (err == nil || commits != 1 || reply.Delivery != domain.DynamicUncertain) || stage == "after-send" && (err != nil || reply.Delivery != domain.DynamicTransmitted) {
				t.Fatal("attempt boundary changed", err)
			}
			if stage != "before" {
				cancel()
				if _, err := c.ReplyDynamicUnavailable(context.Background(), token, turn, domain.NewID(), func() error { commits++; return nil }); err == nil || commits != 1 {
					t.Fatal("uncertainty/cancellation repeated reply")
				}
			}
		})
	}
}
func TestDynamicProtectedHistoryThroughContinuationContextAndFork(t *testing.T) {
	c, capture, checkpoint, page := continuationFixture(t, "dynamic-history")
	turn := page["data"].([]any)[0].(map[string]any)
	turn["items"] = append(turn["items"].([]any), json.RawMessage(dynamicCompletedFixture))
	page["nextCursor"], page["backwardsCursor"] = nil, nil
	fixtureSignal(t, c, "history", map[string]any{"page": page})
	if _, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ContinueAfterSuccess); err != nil {
		t.Fatal(err)
	}
	history, err := c.compactionTurnsLocked(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, managed := range []bool{false, true} {
		c.managedForkHistory = managed
		fork, err := c.forkTurnsLocked(context.Background(), c.thread)
		if err != nil || len(fork) != 1 || historyDigest(fork) != historyDigest(history) {
			t.Fatal("protected original history changed", err)
		}
	}
	c.sidechat = ReadOnlySidechatV1
	if _, err := c.forkTurnsLocked(context.Background(), c.thread); err == nil {
		t.Fatal("restricted Sidechat adopted dynamic history")
	}
	for _, sentinel := range []string{"private-argument-sentinel", "private-image-sentinel", "private-audio-sentinel"} {
		if !bytes.Contains(history[0], []byte(sentinel)) {
			t.Fatal("protected evidence lost")
		}
	}
	for _, method := range []string{"turn/start", "turn/steer", "thread/fork"} {
		if len(requestsOf(t, capture, method)) != 0 {
			t.Fatal("observation dispatched native work")
		}
	}
}

func capturedDynamicReplies(t *testing.T, path string) []map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var replies []map[string]json.RawMessage
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var row map[string]json.RawMessage
		if json.Unmarshal(line, &row) != nil {
			t.Fatal("invalid capture")
		}
		var method string
		_ = json.Unmarshal(row["method"], &method)
		if method == "dynamic-response" {
			replies = append(replies, row)
		}
	}
	return replies
}

func TestDynamicOriginalRequestMayPrecedeLifecycleWithoutDispatch(t *testing.T) {
	c, turn := observationClient()
	c.dynamicToolsEnabled = true
	c.mode = ThreadProtocol
	c.execution.paused = false
	request := dynamicRequestFixture(c, turn, []byte(`7`))
	event, err := c.observeDynamicRequestLocked(request)
	if err != nil || event.DynamicTool == nil || event.DynamicTool.Stage != domain.DynamicRequested {
		t.Fatal("original request required a fabricated lifecycle", err)
	}
	if _, err := observeFixture(c, "item/started", dynamicItemEnvelopeFixture(c, turn, false, dynamicStartedFixture)); err != nil {
		t.Fatal("original later start rejected", err)
	}
	if _, err := c.observeDynamicRequestLocked(request); err == nil {
		t.Fatal("request repeated")
	}
}

func TestDynamicUnresolvedFailedContextDoesNotBlockIndependentCleanup(t *testing.T) {
	for _, status := range []TurnStatus{TurnFailed, TurnInterrupted, TurnCompleted} {
		t.Run(string(status), func(t *testing.T) {
			c, _, source, _ := continuationFixture(t, "dynamic-cleanup")
			source.Status = status
			c.execution.active = ""
			c.dynamicArrivals = map[domain.ID]*dynamicArrival{domain.NewID(): {}}
			proof, err := c.RetainContinuationContext(context.Background(), source)
			if status == TurnCompleted {
				if err == nil {
					t.Fatal("unsettled dynamic request gained successful continuation proof")
				}
			} else if err != nil || proof != nil {
				t.Fatal("failed root without context blocked independent cleanup", err)
			}
			if !c.dynamicRequestsBlockInput() {
				t.Fatal("cleanup erased unsettled dynamic evidence")
			}
		})
	}
}
func TestDynamicOriginalWireSpellingAndPublicPrecisionRemainSeparate(t *testing.T) {
	for _, raw := range []json.RawMessage{json.RawMessage("-0"), json.RawMessage("-9223372036854775808"), json.RawMessage("9223372036854775807"), json.RawMessage("9007199254740993"), json.RawMessage(`"7"`)} {
		c, turn := observationClient()
		c.control = make(chan struct{}, 1)
		c.mode, c.execution.paused, c.dynamicToolsEnabled = ThreadProtocol, false, true
		request := dynamicRequestFixture(c, turn, raw)
		event, err := c.observeDynamicRequestLocked(request)
		if err != nil {
			t.Fatal(err)
		}
		original, err := c.DynamicRequestID(context.Background(), request.Token)
		if err != nil || !bytes.Equal(original, raw) || event.DynamicTool.Request.Validate() != nil {
			t.Fatal("original private spelling or public precision changed", err)
		}
		if string(raw) == "-0" && event.DynamicTool.Request.IDValue != "0" {
			t.Fatal("public number is not canonical")
		}
	}
}
