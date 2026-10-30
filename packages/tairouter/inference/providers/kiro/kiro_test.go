// SPDX-License-Identifier: Apache-2.0
// Synthetic fixtures adapted from pinned opencodex Kiro tests; see LICENSE.upstream.
package kiro

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

func fixtureFrame(kind string, payload string) []byte {
	headers := []byte{}
	for _, h := range [][2]string{{":message-type", "event"}, {":event-type", kind}} {
		headers = append(headers, byte(len(h[0])))
		headers = append(headers, h[0]...)
		headers = append(headers, 7, byte(len(h[1])>>8), byte(len(h[1])))
		headers = append(headers, h[1]...)
	}
	b := make([]byte, 16+len(headers)+len(payload))
	binary.BigEndian.PutUint32(b[:4], uint32(len(b)))
	binary.BigEndian.PutUint32(b[4:8], uint32(len(headers)))
	binary.BigEndian.PutUint32(b[8:12], crc32.ChecksumIEEE(b[:8]))
	copy(b[12:], headers)
	copy(b[12+len(headers):], payload)
	binary.BigEndian.PutUint32(b[len(b)-4:], crc32.ChecksumIEEE(b[:len(b)-4]))
	return b
}
func event(kind string, value object) []byte {
	b, _ := json.Marshal(value)
	return fixtureFrame(kind, string(b))
}
func input(body string) inference.AdapterInput {
	return inference.AdapterInput{Operation: inference.Generate, Wire: inference.Chat, Target: inference.Target{BaseURL: "https://runtime.us-east-1.kiro.dev/", Model: "kiro/claude-sonnet-4-5"}, Credential: inference.Credential{Secret: "ksk_fixture"}, Body: json.RawMessage(body)}
}
func build(t *testing.T, body string) (context.Context, *Adapter, inference.AdapterRequest) {
	t.Helper()
	a := New()
	ctx := a.PrepareContext(context.Background())
	request, err := a.Build(ctx, input(body))
	if err != nil {
		t.Fatal(err)
	}
	return ctx, a, request
}

const plainRequest = `{"messages":[{"role":"user","content":"Hello"}]}`
const toolRequest = `{"messages":[{"role":"user","content":"Use the tool"}],"tools":[{"type":"function","function":{"name":"tool with spaces","parameters":{"type":"object","properties":{"format":{"type":"string","pattern":"x"}},"additionalProperties":false}}}]}`

func readDecoded(t *testing.T, ctx context.Context, a *Adapter, frames ...[]byte) (string, error) {
	t.Helper()
	raw := bytes.Join(frames, nil)
	response := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}
	decoded, err := a.Decode(ctx, response)
	if err != nil {
		return "", err
	}
	defer decoded.Body.Close()
	b, err := io.ReadAll(decoded.Body)
	return string(b), err
}

func TestBuildWireModes(t *testing.T) {
	for _, test := range []struct {
		name, secret, profile string
		builder, cli          bool
	}{
		{"key", "ksk_fixture", "ignored", false, true},
		{"profile", "oauth_fixture", "arn:aws:codewhisperer:eu-west-1:123:profile/test", false, false},
		{"builder", "oauth_fixture", "", true, true},
		{"profileless", "oauth_fixture", "", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			in := input(plainRequest)
			in.Credential.Secret = test.secret
			in.Credential.Headers = http.Header{}
			in.Credential.Headers.Set("X-Amzn-Kiro-Profile-Arn", test.profile)
			if test.builder {
				in.Credential.Headers.Set("X-Tairouter-Kiro-Builder-Id", "true")
			}
			r, err := New().Build(WithRequestState(context.Background()), in)
			if err != nil {
				t.Fatal(err)
			}
			var body object
			if err := json.Unmarshal(r.Body, &body); err != nil {
				t.Fatal(err)
			}
			state := obj(body["conversationState"])
			user := obj(obj(state["currentMessage"])["userInputMessage"])
			if user["modelId"] != "claude-sonnet-4.5" || state["chatTriggerType"] != "MANUAL" {
				t.Fatal(string(r.Body))
			}
			origin := "AI_EDITOR"
			if test.cli {
				origin = "KIRO_CLI"
			}
			if user["origin"] != origin {
				t.Fatal(user)
			}
			if r.ResponseWire != inference.Chat || r.Headers.Get("X-Amz-Target") != "AmazonCodeWhispererStreamingService.GenerateAssistantResponse" {
				t.Fatal(r.Headers)
			}
			if test.builder && body["profileArn"] != builderProfile {
				t.Fatal(body)
			}
			if test.name == "key" && (body["profileArn"] != nil || r.Headers.Get("Tokentype") != "API_KEY") {
				t.Fatal(body, r.Headers)
			}
			if r.Headers.Get("X-Tairouter-Kiro-Builder-Id") != "" {
				t.Fatal("internal metadata leaked")
			}
		})
	}
}

func TestToolSchemaNamesAndHistory(t *testing.T) {
	_, _, request := build(t, toolRequest)
	var body object
	json.Unmarshal(request.Body, &body)
	user := obj(obj(obj(body["conversationState"])["currentMessage"])["userInputMessage"])
	tools := arr(obj(user["userInputMessageContext"])["tools"])
	if len(tools) != 2 {
		t.Fatal(tools)
	}
	spec := obj(obj(tools[0])["toolSpecification"])
	name := str(spec["name"])
	if name == "tool with spaces" || invalidName.MatchString(name) || len(name) > 64 {
		t.Fatal(name)
	}
	schema := obj(obj(spec["inputSchema"])["json"])
	if schema["additionalProperties"] != nil || obj(obj(schema["properties"])["format"])["pattern"] != nil || obj(schema["properties"])["format"] == nil {
		t.Fatal(schema)
	}
	_, _, history := build(t, `{"messages":[{"role":"system","content":"Rules"},{"role":"user","content":"Run"},{"role":"assistant","tool_calls":[{"id":"call.1","function":{"name":"do it","arguments":"{\"path\":\"a\"}"}}]},{"role":"tool","tool_call_id":"call.1","content":""}]}`)
	json.Unmarshal(history.Body, &body)
	state := obj(body["conversationState"])
	entries := arr(state["history"])
	if len(entries) != 2 || !strings.HasPrefix(str(obj(obj(entries[0])["userInputMessage"])["content"]), "Rules") {
		t.Fatal(state)
	}
	call := obj(arr(obj(obj(entries[1])["assistantResponseMessage"])["toolUses"])[0])
	if call["toolUseId"] != "call_1" || obj(call["input"])["path"] != "a" {
		t.Fatal(call)
	}
	user = obj(obj(state["currentMessage"])["userInputMessage"])
	if user["content"] != "The requested tool result is attached." {
		t.Fatal(user)
	}
}

func TestInputRefusals(t *testing.T) {
	for _, body := range []string{
		`{"messages":[{"role":"user","content":"x"}],"tool_choice":"required"}`,
		`{"messages":[{"role":"user","content":"x"}],"service_tier":"priority"}`,
		`{"messages":[{"role":"user","content":"x"}],"response_format":{"type":"json_schema"}}`,
		`{"messages":[{"role":"tool","tool_call_id":"missing","content":"x"}]}`,
		`{"messages":[{"role":"assistant","phase":"final_answer","content":"Done"}]}`,
	} {
		if _, err := New().Build(WithRequestState(context.Background()), input(body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	if _, err := New().Build(context.Background(), input(plainRequest)); err == nil {
		t.Fatal("missing state accepted")
	}
}

func TestReasoningEffortAndInboundWires(t *testing.T) {
	for _, test := range []struct{ model, effort, field string }{
		{"gpt-5.6-sol", "xhigh", "reasoning"}, {"gpt-5.6-terra", "max", "reasoning"}, {"gpt-5.6-terra", "xhigh", ""}, {"claude-opus-5", "high", "output_config"}, {"claude-sonnet-4.5", "high", ""},
	} {
		in := input(`{"messages":[{"role":"user","content":"x"}],"reasoning_effort":"` + test.effort + `"}`)
		in.Target.Model = test.model
		r, err := New().Build(WithRequestState(context.Background()), in)
		if err != nil {
			t.Fatal(err)
		}
		var payload object
		json.Unmarshal(r.Body, &payload)
		if test.field == "" {
			if !strings.Contains(string(r.Body), "thinking_mode") || payload["additionalModelRequestFields"] != nil {
				t.Fatal(string(r.Body))
			}
		} else if obj(obj(payload["additionalModelRequestFields"])[test.field])["effort"] != test.effort {
			t.Fatal(payload)
		}
	}
	for _, test := range []struct {
		wire inference.Wire
		body string
	}{
		{inference.Responses, `{"instructions":"Rules","input":[{"role":"user","content":[{"type":"input_text","text":"Hi"}]}]}`},
		{inference.Anthropic, `{"system":[{"type":"text","text":"Rules"}],"messages":[{"role":"user","content":"Hi"}]}`},
	} {
		in := input(test.body)
		in.Wire = test.wire
		if _, err := New().Build(WithRequestState(context.Background()), in); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFrameIntegrityAndFragmentation(t *testing.T) {
	valid := event("assistantResponseEvent", object{"content": "Hello"})
	// One-byte reads must not change message boundaries.
	f, err := readFrame(&shortReader{Reader: bytes.NewReader(valid)})
	if err != nil || !bytes.Contains(f.payload, []byte("Hello")) {
		t.Fatal(f, err)
	}
	for _, offset := range []int{0, 8, 15, len(valid) - 1} {
		bad := append([]byte(nil), valid...)
		bad[offset] ^= 1
		if _, err := readFrame(bytes.NewReader(bad)); err == nil {
			t.Fatalf("corruption accepted at %d", offset)
		}
	}
	for _, n := range []int{1, 11, 12, len(valid) - 1} {
		if _, err := readFrame(bytes.NewReader(valid[:n])); err == nil {
			t.Fatalf("truncation accepted %d", n)
		}
	}
	large := append([]byte(nil), valid[:12]...)
	binary.BigEndian.PutUint32(large[:4], maxFrameBytes+1)
	if _, err := readFrame(bytes.NewReader(large)); err == nil {
		t.Fatal("oversize accepted")
	}
}

type shortReader struct{ io.Reader }

func (r *shortReader) Read(b []byte) (int, error) {
	if len(b) > 1 {
		b = b[:1]
	}
	return r.Reader.Read(b)
}

func TestTextReasoningUsageAndUnknownEvents(t *testing.T) {
	ctx, adapter, _ := build(t, plainRequest)
	output, err := readDecoded(t, ctx, adapter,
		fixtureFrame("futureEvent", "not JSON"),
		event("assistantResponseEvent", object{"content": "<think"}),
		event("assistantResponseEvent", object{"content": "ing>reason</thinking>answer"}),
		event("reasoningContentEvent", object{"signature": ".KTR~~opaque"}),
		event("meteringEvent", object{"unit": "credit", "usage": .5}),
		event("metadataEvent", object{"stopReason": "END_TURN", "tokenUsage": object{"uncachedInputTokens": 5, "cacheReadInputTokens": 3, "outputTokens": 2, "totalTokens": 10}}),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"reasoning_content":"reason"`, `"content":"answer"`, `"kiro_reasoning":"signature:.KTR~~opaque"`, `"prompt_tokens":8`, `"cached_tokens":3`, "data: [DONE]"} {
		if !strings.Contains(output, want) {
			t.Fatalf("missing %s in %s", want, output)
		}
	}
	if strings.Contains(output, "cache_creation_input_tokens") {
		t.Fatal("unknown cache write became zero")
	}
	ctx, adapter, _ = build(t, plainRequest)
	output, err = readDecoded(t, ctx, adapter, event("assistantResponseEvent", object{"content": "x"}), event("metadataEvent", object{"stopReason": "END_TURN"}))
	if err != nil || strings.Contains(output, `"usage"`) {
		t.Fatal(output, err)
	}
}

func TestToolsAndTerminalCompletion(t *testing.T) {
	ctx, adapter, _ := build(t, toolRequest)
	state := ctx.Value(stateKey{}).(*requestState)
	alias := ""
	for name := range state.aliases {
		alias = name
	}
	output, err := readDecoded(t, ctx, adapter,
		event("toolUseEvent", object{"name": alias, "toolUseId": "t1", "input": `{"format":`}),
		event("toolUseEvent", object{"input": `"x"}`, "stop": true}),
		event("metadataEvent", object{"stopReason": "TOOL_USE"}),
	)
	if err != nil || !strings.Contains(output, `"name":"tool with spaces"`) || !strings.Contains(output, `"finish_reason":"tool_calls"`) {
		t.Fatal(output, err)
	}
	ctx, adapter, _ = build(t, toolRequest)
	output, err = readDecoded(t, ctx, adapter, event("toolUseEvent", object{"name": completionTool, "toolUseId": "final", "input": `{"answer":"Done"}`, "stop": true}), event("metadataEvent", object{"stopReason": "END_TURN"}))
	if err != nil || !strings.Contains(output, `"content":"Done"`) || strings.Contains(output, completionTool) {
		t.Fatal(output, err)
	}
}

func TestStreamFailuresNeverEmitDone(t *testing.T) {
	for _, test := range []struct {
		name, request string
		frames        [][]byte
	}{
		{"partial", plainRequest, [][]byte{event("assistantResponseEvent", object{"content": "partial"})}},
		{"bad JSON", plainRequest, [][]byte{fixtureFrame("assistantResponseEvent", "{")}},
		{"bad content", plainRequest, [][]byte{fixtureFrame("assistantResponseEvent", `{"content":4}`)}},
		{"empty", plainRequest, nil},
		{"progress", toolRequest, [][]byte{event("assistantResponseEvent", object{"content": "working"}), event("metadataEvent", object{"stopReason": "END_TURN"})}},
		{"context", plainRequest, [][]byte{event("metadataEvent", object{"stopReason": "MODEL_CONTEXT_WINDOW_EXCEEDED"})}},
		{"filtered tool", toolRequest, [][]byte{event("metadataEvent", object{"stopReason": "TOOL_USE"})}},
		{"bad usage", plainRequest, [][]byte{event("metadataEvent", object{"tokenUsage": object{"uncachedInputTokens": -1, "outputTokens": 2, "totalTokens": 1}})}},
		{"post final", toolRequest, [][]byte{event("toolUseEvent", object{"name": completionTool, "toolUseId": "f", "input": `{"answer":"Done"}`, "stop": true}), event("assistantResponseEvent", object{"content": "again"})}},
		{"error redaction", plainRequest, [][]byte{event("error", object{"reason": "ThrottlingException", "message": "secret-prompt-token"})}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, adapter, _ := build(t, test.request)
			out, err := readDecoded(t, ctx, adapter, test.frames...)
			if err == nil || strings.Contains(out, "[DONE]") || !strings.Contains(out, `"error"`) || strings.Contains(out, "secret-prompt-token") {
				t.Fatal(out, err)
			}
		})
	}
}

func TestErrorClassification(t *testing.T) {
	for _, test := range []struct {
		status     int
		body, code string
		retry      bool
	}{
		{400, `{"reason":"CONTENT_LENGTH_EXCEEDS_THRESHOLD"}`, "context_length_exceeded", false},
		{400, `{"message":"profileArn required"}`, "kiro_profile_required", false},
		{429, `{"message":"monthly quota exceeded"}`, "insufficient_quota", false},
		{429, `{}`, "rate_limit_exceeded", true},
		{403, `{}`, "permission_denied", false},
		{503, `{}`, "server_is_overloaded", true},
		{504, `secret`, "upstream_server_error", true},
	} {
		got := ClassifyHTTPError(test.status, nil, []byte(test.body))
		if got.Code != test.code || got.Retryable != test.retry {
			t.Fatal(got)
		}
	}
}

type blockingBody struct {
	closed chan struct{}
	once   sync.Once
}

func (b *blockingBody) Read([]byte) (int, error) { <-b.closed; return 0, io.ErrClosedPipe }
func (b *blockingBody) Close() error             { b.once.Do(func() { close(b.closed) }); return nil }
func TestCancellationClosesSource(t *testing.T) {
	base, adapter, _ := build(t, plainRequest)
	ctx, cancel := context.WithCancel(base)
	defer cancel()
	body := &blockingBody{closed: make(chan struct{})}
	decoded, err := adapter.Decode(ctx, &http.Response{StatusCode: 200, Header: http.Header{}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	defer decoded.Body.Close()
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(decoded.Body); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("source not closed")
	}
}

func TestConcurrentRequestsDoNotShareAliases(t *testing.T) {
	a := New()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := a.PrepareContext(context.Background())
			if _, err := a.Build(ctx, input(toolRequest)); err != nil {
				t.Error(err)
				return
			}
			out, err := readDecoded(t, ctx, a, event("toolUseEvent", object{"name": completionTool, "toolUseId": "f", "input": `{"answer":"Done"}`, "stop": true}))
			if err != nil || !strings.Contains(out, "Done") {
				t.Error(out, err)
			}
		}()
	}
	wg.Wait()
}

func TestCompletionSupersedesAnswerShapedText(t *testing.T) {
	ctx, adapter, _ := build(t, toolRequest)
	out, err := readDecoded(t, ctx, adapter,
		event("assistantResponseEvent", object{"content": "Answer duplicated here"}),
		event("toolUseEvent", object{"name": completionTool, "toolUseId": "f", "input": `{"answer":"Only this answer"}`, "stop": true}),
	)
	if err != nil || strings.Contains(out, "duplicated") || !strings.Contains(out, "Only this answer") {
		t.Fatal(out, err)
	}
}

func TestExplicitZeroAndMissingCacheUsage(t *testing.T) {
	s := streamState{usage: object{}}
	if err := s.tokenUsage(object{"uncachedInputTokens": float64(0), "outputTokens": float64(0), "totalTokens": float64(0), "cacheWriteInputTokens": float64(0)}); err != nil {
		t.Fatal(err)
	}
	if s.usage["cache_creation_input_tokens"] != int64(0) || s.usage["prompt_tokens_details"] != nil {
		t.Fatal(s.usage)
	}
}

func TestRecoveryRules(t *testing.T) {
	endpoint := "https://runtime.eu-west-1.kiro.dev/"
	if LegacyEndpoint(endpoint) != "https://q.eu-west-1.amazonaws.com/" {
		t.Fatal(LegacyEndpoint(endpoint))
	}
	for _, value := range []string{"https://gateway.example/", endpoint + "?x=y", endpoint + "path", "http://runtime.eu-west-1.kiro.dev/", "https://runtime.eu-west-1.kiro.dev:443/"} {
		if LegacyEndpoint(value) != "" {
			t.Fatal(value)
		}
	}
	now := time.Unix(1000, 0)
	for _, test := range []struct {
		status, attempt int
		body            string
		used, gateway   bool
		want            RecoveryKind
	}{
		{503, 1, "", false, true, RecoveryEndpoint},
		{503, 1, "", true, true, RecoveryNone},
		{503, 1, "", false, false, RecoveryNone},
		{400, 1, "UnknownOperation", false, false, RecoveryEndpoint},
		{400, 1, "ValidationException", false, true, RecoveryNone},
		{429, 1, "ThrottlingException", false, true, RecoveryThrottle},
		{429, 3, "ThrottlingException", false, true, RecoveryNone},
		{429, 1, "insufficient_quota", false, true, RecoveryNone},
	} {
		p := PlanHTTPRecovery(endpoint, test.status, nil, []byte(test.body), test.attempt, test.used, test.gateway, now)
		if p.Kind != test.want {
			t.Fatal(test, p)
		}
	}
	h := http.Header{"Retry-After": {"0"}}
	if p := PlanHTTPRecovery(endpoint, 429, h, nil, 1, false, true, now); p.Delay != 0 {
		t.Fatal(p)
	}
	h.Set("Retry-After", now.Add(9*time.Second).UTC().Format(http.TimeFormat))
	if p := PlanHTTPRecovery(endpoint, 429, h, nil, 1, false, true, now); p.Delay != 9*time.Second {
		t.Fatal(p)
	}
}

func TestUnknownOrUnfinishedToolFails(t *testing.T) {
	for _, payload := range []object{
		{"toolUseId": "t", "name": "unadvertised", "input": "{}", "stop": true},
		{"toolUseId": "t", "name": completionTool, "input": `{"answer":`},
		{"toolUseId": "t", "name": completionTool, "input": `{"answer":`, "stop": true},
	} {
		ctx, a, _ := build(t, toolRequest)
		out, err := readDecoded(t, ctx, a, event("toolUseEvent", payload))
		if err == nil || strings.Contains(out, "[DONE]") {
			t.Fatal(out, err)
		}
	}
}

func TestHTTPFailureAndConsumerClose(t *testing.T) {
	a := New()
	_, err := a.Decode(context.Background(), &http.Response{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"message":"insufficient_quota private-value"}`))})
	var classified *inference.Error
	if !errors.As(err, &classified) || classified.Code != "insufficient_quota" || strings.Contains(err.Error(), "private-value") {
		t.Fatal(err)
	}
	ctx, a, _ := build(t, plainRequest)
	body := &blockingBody{closed: make(chan struct{})}
	decoded, err := a.Decode(ctx, &http.Response{StatusCode: 200, Header: http.Header{}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if err := decoded.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-body.closed:
	case <-time.After(time.Second):
		t.Fatal("consumer close did not close source")
	}
}

func TestSignatureReplayAndInlineImage(t *testing.T) {
	_, _, r := build(t, `{"messages":[{"role":"user","content":"First"},{"role":"assistant","content":"Answer","kiro_reasoning":"signature:.KTR~~opaque"},{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,`+testPNG(t, 1, 1)+`"}}]}]}`)
	var body object
	json.Unmarshal(r.Body, &body)
	state := obj(body["conversationState"])
	history := arr(state["history"])
	reasoning := obj(obj(obj(history[1])["assistantResponseMessage"])["reasoningContent"])
	if reasoning["signature"] != ".KTR~~opaque" || reasoning["redactedContent"] != nil {
		t.Fatal(reasoning)
	}
	user := obj(obj(state["currentMessage"])["userInputMessage"])
	if len(arr(user["images"])) != 1 || obj(user["userInputMessageContext"])["images"] != nil {
		t.Fatal(user)
	}
}

func TestToolNoneDoesNotAuthorizeHistoryTools(t *testing.T) {
	ctx, a, _ := build(t, `{"messages":[{"role":"user","content":"x"}],"tool_choice":"none","tools":[{"type":"function","function":{"name":"disabled","parameters":{}}}]}`)
	out, err := readDecoded(t, ctx, a, event("toolUseEvent", object{"toolUseId": "t", "name": "disabled", "input": "{}", "stop": true}))
	if err == nil || strings.Contains(out, "[DONE]") {
		t.Fatal(out, err)
	}
}
