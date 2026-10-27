// SPDX-License-Identifier: Apache-2.0
package google

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

const signatureA = "sig-alpha-opaque-value"
const signatureB = "sig-beta-opaque-value"

func input(body string) inference.AdapterInput {
	return inference.AdapterInput{
		Principal:  inference.Principal{OrganizationID: "org-a", UserID: "user-a"},
		Target:     inference.Target{Model: "gemini-3.1-pro-preview", Auth: inference.APIKey},
		Credential: inference.Credential{AccountID: "account-a", Secret: "selected-secret"},
		Operation:  inference.Generate, Wire: inference.Chat, Body: json.RawMessage(body),
	}
}
func requestBody(t *testing.T, a *Adapter, in inference.AdapterInput) object {
	t.Helper()
	request, err := a.Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body, err := decodeJSON(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
func upstream(body string, stream bool) *http.Response {
	contentType := "application/json"
	if stream {
		contentType = "text/event-stream"
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}, "Content-Length": {"123"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func decoded(t *testing.T, a *Adapter, body string) object {
	t.Helper()
	response, err := a.Decode(context.Background(), upstream(body, false))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	root, err := decodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func errorCode(err error) string {
	var e *inference.Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func TestAuthenticationAndEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name      string
		adapter   *Adapter
		cca       bool
		model, ua string
	}{
		{"api", NewAPI(Options{}), false, "gemini-3.1-pro", ""},
		{"antigravity", NewAntigravity(Options{}), true, "gemini-3.7-flash-tiered", "antigravity/ide/2.5.5"},
		{"argon", NewAntigravity(Options{}), true, "gemini-4-argon-high", "antigravity/cli/1.3.4"},
		{"cli-http", NewGeminiCLI(Options{}), true, "gemini-3.1-pro", "antigravity/ide/2.5.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input(`{"messages":[{"role":"user","content":"hello"}],"stream":true}`)
			in.Target.Model = tc.model
			in.Target.Headers = http.Header{"Authorization": {"wrong"}, "X-Api-Key": {"wrong"}, "X-Goog-Api-Key": {"wrong"}}
			in.Credential.Headers = http.Header{ProjectHeader: {"discovered-project"}}
			if tc.cca {
				in.Target.Auth = inference.OAuth
			}
			r, err := tc.adapter.Build(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			if r.ResponseWire != inference.Chat || !strings.HasSuffix(r.URL, ":streamGenerateContent?alt=sse") {
				t.Fatalf("unexpected request: %s", r.URL)
			}
			if r.Headers.Get(ProjectHeader) != "" || r.Headers.Get("X-Api-Key") != "" {
				t.Fatal("internal or unrelated credential header leaked")
			}
			body, _ := decodeJSON(r.Body)
			if tc.cca {
				if r.Headers.Get("Authorization") != "Bearer selected-secret" || r.Headers.Get("X-Goog-Api-Key") != "" {
					t.Fatal("wrong OAuth headers")
				}
				if !strings.HasPrefix(r.Headers.Get("User-Agent"), tc.ua) {
					t.Fatal("wrong client family")
				}
				wireModel, _ := ResolveModel(tc.model, "", true)
				if body["project"] != "discovered-project" || body["model"] != wireModel || body["userAgent"] != "antigravity" || body["requestType"] != "agent" {
					t.Fatal(body)
				}
				if !strings.HasPrefix(text(obj(body["request"])["sessionId"]), "-") || body["sessionId"] != nil {
					t.Fatal("incorrect session location")
				}
			} else if r.Headers.Get("X-Goog-Api-Key") != "selected-secret" || r.Headers.Get("Authorization") != "" {
				t.Fatal("wrong API headers")
			}
			if in.Target.Headers.Get("Authorization") != "wrong" {
				t.Fatal("mutated caller headers")
			}
		})
	}
}

func TestAccountScopedSessionAndProject(t *testing.T) {
	a := NewAntigravity(Options{})
	in := input(`{"messages":[{"role":"user","content":"same"}]}`)
	in.Target.Auth = inference.OAuth
	if _, err := a.Build(context.Background(), in); errorCode(err) != "google_project_required" {
		t.Fatal(err)
	}
	in.Credential.Headers = http.Header{ProjectHeader: {"p"}}
	first := text(obj(requestBody(t, a, in)["request"])["sessionId"])
	if again := text(obj(requestBody(t, a, in)["request"])["sessionId"]); first != again {
		t.Fatal("unstable session")
	}
	in.Principal.OrganizationID = "org-b"
	if other := text(obj(requestBody(t, a, in)["request"])["sessionId"]); first == other {
		t.Fatal("cross-tenant session collision")
	}
}

// Fixtures follow google-signature-history-roundtrip, google-tool-result-adjacency,
// google-buffered-stop-reason and google-provider-metadata-roundtrip at the pin.
func TestToolHistoryPreservesParallelSignaturesAndOrder(t *testing.T) {
	in := input(`{"messages":[{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"first","arguments":"{\"x\":1}"},"extra_content":{"google":{"thought_signature":"sig-alpha-opaque-value"}}},{"id":"b","type":"function","function":{"name":"second","arguments":"{}"},"extra_content":{"google":{"thought_signature":"sig-beta-opaque-value"}}}]},{"role":"tool","tool_call_id":"b","content":"B"},{"role":"tool","tool_call_id":"a","content":"A"}]}`)
	body := requestBody(t, NewAPI(Options{}), in)
	contents := array(body["contents"])
	if len(contents) != 3 || obj(array(obj(contents[0])["parts"])[0])["text"] != "(continue)" {
		t.Fatal(contents)
	}
	parts := array(obj(contents[1])["parts"])
	if signature(obj(parts[0])) != signatureA || signature(obj(parts[1])) != signatureB {
		t.Fatal("signatures were merged")
	}
	results := array(obj(contents[2])["parts"])
	if obj(obj(results[0])["functionResponse"])["id"] != "a" || obj(obj(results[1])["functionResponse"])["id"] != "b" {
		t.Fatal("results not adjacent in call order")
	}
}

func TestForeignSignatureAndMissingResult(t *testing.T) {
	in := input(`{"messages":[{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"first","arguments":"{}"},"extra_content":{"google":{"thought_signature":"call_foreign_signature_id"}}}]}]}`)
	contents := array(requestBody(t, NewAPI(Options{}), in)["contents"])
	call := obj(array(obj(contents[1])["parts"])[0])
	if signature(call) != "skip_thought_signature_validator" {
		t.Fatal(call)
	}
	response := obj(obj(array(obj(contents[2])["parts"])[0])["functionResponse"])
	if obj(response["response"])["result"] != "[missing tool_result for this tool_use in history]" {
		t.Fatal(response)
	}
}

func TestRequestWiresAndStructuredOutput(t *testing.T) {
	for _, tc := range []struct {
		wire inference.Wire
		body string
	}{
		{inference.Chat, `{"messages":[{"role":"system","content":"rules"},{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]},"strict":true}}],"response_format":{"type":"json_schema","json_schema":{"schema":{"type":"object","additionalProperties":false}}},"reasoning_effort":"high"}`},
		{inference.Responses, `{"instructions":"rules","input":"hi","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"},"strict":true}],"text":{"format":{"type":"json_schema","schema":{"type":"object","additionalProperties":false}}},"reasoning":{"effort":"high"}}`},
		{inference.Anthropic, `{"system":"rules","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}],"tool_choice":{"type":"any"},"thinking":{"type":"enabled","budget_tokens":1024}}`},
	} {
		t.Run(string(tc.wire), func(t *testing.T) {
			in := input(tc.body)
			in.Wire = tc.wire
			body := requestBody(t, NewAPI(Options{}), in)
			if body["systemInstruction"] == nil || body["tools"] == nil || body["toolConfig"] == nil {
				t.Fatal(body)
			}
			gc := obj(body["generationConfig"])
			if gc["thinkingConfig"] == nil {
				t.Fatal("reasoning dropped")
			}
			if tc.wire != inference.Anthropic && obj(gc["responseJsonSchema"])["additionalProperties"] != false {
				t.Fatal("output schema was sanitized as a tool schema")
			}
		})
	}
}

func TestRequestRefusals(t *testing.T) {
	for _, body := range []string{
		`{"messages":[],"unknown":true}`,
		`{"messages":[],"tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object","additionalProperties":false}}}]}`,
		`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://private.invalid/image"}}]}]}`,
		`{"messages":[],"response_format":{"type":"json_schema"}}`,
		`{"messages":[]} {"messages":[]}`,
	} {
		if _, err := NewAPI(Options{SchemaPolicy: SchemaRejectLossy}).Build(context.Background(), input(body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewAPI(Options{}).Build(ctx, input(`{"messages":[]}`)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestReasoningVisibilityAndSignatureCarry(t *testing.T) {
	body := `{"candidates":[{"content":{"parts":[{"thought":true,"text":"opaque thought","thoughtSignature":"sig-alpha-opaque-value"},{"functionCall":{"name":"one","args":{"n":9007199254740993}}},{"functionCall":{"name":"two","args":{}},"thoughtSignature":"sig-beta-opaque-value"},{"text":"visible"}]},"finishReason":"STOP"}]}`
	for _, show := range []bool{false, true} {
		root := decoded(t, NewAPI(Options{ShowThinkingSummaries: show}), body)
		msg := obj(obj(array(root["choices"])[0])["message"])
		if msg["content"] != "visible" {
			t.Fatal("thought leaked into content")
		}
		if (msg["reasoning_content"] != nil) != show {
			t.Fatal("incorrect summary visibility")
		}
		calls := array(msg["tool_calls"])
		if signature(obj(calls[0])) != signatureA || signature(obj(calls[1])) != signatureB {
			t.Fatal("lost per-call signature")
		}
		if !strings.Contains(text(obj(obj(calls[0])["function"])["arguments"]), "9007199254740993") {
			t.Fatal("argument precision lost")
		}
		if len(array(obj(obj(msg["extra_content"])["google"])["parts"])) != 1 {
			t.Fatal("opaque reasoning lost")
		}
		if len(obj(root["usage"])) != 0 {
			t.Fatal("unknown usage became zero")
		}
	}
}

func TestStopsAndUsage(t *testing.T) {
	for reason, want := range map[string]string{"STOP": "stop", "MAX_TOKENS": "length", "SAFETY": "content_filter", "RECITATION": "content_filter"} {
		root := decoded(t, NewAPI(Options{}), `{"candidates":[{"finishReason":"`+reason+`"}],"usageMetadata":{"promptTokenCount":0,"cachedContentTokenCount":0,"thoughtsTokenCount":2}}`)
		if obj(array(root["choices"])[0])["finish_reason"] != want {
			t.Fatal(root)
		}
		u := obj(root["usage"])
		if u["completion_tokens"] != nil || u["total_tokens"] != nil || text(u["prompt_tokens"]) != "" {
			t.Fatal(u)
		}
		if u["prompt_tokens"] != json.Number("0") || obj(u["completion_tokens_details"])["reasoning_tokens"] != json.Number("2") {
			t.Fatal(u)
		}
	}
}

func TestMalformedResponses(t *testing.T) {
	for _, body := range []string{
		`{"candidates":{}}`, `{"candidates":[null]}`, `{"candidates":[{"content":{"parts":[null]}}]}`,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":" "}}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"f","args":[]}}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"AAAA"}}]},"finishReason":"STOP"}]}`,
		`{"candidates":[{"finishReason":"MALFORMED_FUNCTION_CALL"}]}`,
		`{"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}`,
		`{"usageMetadata":{"promptTokenCount":-1}}`, `{"error":{"message":"secret upstream text"}}`,
	} {
		if _, err := NewAPI(Options{}).Decode(context.Background(), upstream(body, false)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	if _, err := NewAntigravity(Options{}).Decode(context.Background(), upstream(`{"candidates":[]}`, false)); errorCode(err) != "google_missing_response_wrapper" {
		t.Fatal(err)
	}
	if _, err := NewAPI(Options{MaxBodyBytes: 32}).Decode(context.Background(), upstream(strings.Repeat(" ", 33), false)); errorCode(err) != "google_response_too_large" {
		t.Fatal(err)
	}
}

func TestStreamingUsageOnlyTailAndCRLF(t *testing.T) {
	stream := ": ping\r\n\r\ndata: null\r\n\r\ndata: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}}\r\n\r\n" +
		"data: {\"response\":{\"candidates\":null}}\n\n" +
		"data: {\"response\":{\"candidates\":[{\"finishReason\":\"STOP\"}]}}\n\n" +
		"data: {\"response\":{\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2}}}\n\n"
	r, err := NewAntigravity(Options{}).Decode(context.Background(), upstream(stream, true))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Count(s, "[DONE]") != 1 || !strings.Contains(s, `"content":"hello"`) || !strings.Contains(s, `"prompt_tokens":3`) || !strings.Contains(s, `"completion_tokens":2`) {
		t.Fatal(s)
	}
	if r.Header.Get("Content-Length") != "" {
		t.Fatal("stale length")
	}
}

func TestStreamingTerminalErrors(t *testing.T) {
	for _, stream := range []string{
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial\"}]}}]}\n\n",
		"data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\nevent: error\ndata: {\"error\":\"private\"}\n\n",
		"data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\ndata: {bad}\n\n",
		"data: " + strings.Repeat("x", 200),
	} {
		r, err := NewAPI(Options{MaxFrameBytes: 128}).Decode(context.Background(), upstream(stream, true))
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err == nil || strings.Contains(string(data), "[DONE]") {
			t.Fatalf("false success: %s, %v", data, err)
		}
	}
}

type blockedBody struct {
	once   sync.Once
	closed chan struct{}
}

func (b *blockedBody) Read([]byte) (int, error) { <-b.closed; return 0, io.ErrClosedPipe }
func (b *blockedBody) Close() error             { b.once.Do(func() { close(b.closed) }); return nil }
func TestCancellationInterruptsBlockedRead(t *testing.T) {
	for _, stream := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		body := &blockedBody{closed: make(chan struct{})}
		response := upstream("", stream)
		response.Body = body
		done := make(chan error, 1)
		go func() {
			r, err := NewAPI(Options{}).Decode(ctx, response)
			if err == nil {
				_, err = io.ReadAll(r.Body)
				_ = r.Body.Close()
			}
			done <- err
		}()
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("cancellation did not release read")
		}
	}
}

func TestHTTPFailureRemainsExecutorOwned(t *testing.T) {
	r := upstream(`{"error":{"status":"RESOURCE_EXHAUSTED"}}`, false)
	r.StatusCode = 429
	r.Header.Set("Retry-After", "7")
	out, err := NewAPI(Options{}).Decode(context.Background(), r)
	if err != nil || out != r || out.Header.Get("Retry-After") != "7" {
		t.Fatal("HTTP failure changed")
	}
	_ = out.Body.Close()
}

func TestHeaderOverlayRemovesUnrelatedCredentials(t *testing.T) {
	in := input(`{"messages":[]}`)
	in.Target.Headers = http.Header{"Authorization": {"unrelated-bearer"}, "X-Api-Key": {"unrelated-key"}, "Cookie": {"unrelated-cookie"}}
	in.Credential.Headers = http.Header{ProjectHeader: {"private-project"}}
	built, err := NewAPI(Options{}).Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	// Match the parent executor's header overlay, including nil removal entries.
	headers := in.Target.Headers.Clone()
	for k, values := range in.Credential.Headers {
		headers[k] = values
	}
	for k, values := range built.Headers {
		headers.Del(k)
		for _, value := range values {
			headers.Add(k, value)
		}
	}
	for _, k := range []string{"Authorization", "X-Api-Key", "Cookie", ProjectHeader} {
		if headers.Get(k) != "" {
			t.Fatalf("leaked %s", k)
		}
	}
	if headers.Get("X-Goog-Api-Key") != "selected-secret" {
		t.Fatal("selected key missing")
	}
}

func TestOrphanAndDuplicateResultsRetainMarkedText(t *testing.T) {
	in := input(`{"messages":[{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"a","content":"first"},{"role":"tool","tool_call_id":"a","content":"duplicate"},{"role":"tool","tool_call_id":"orphan","content":"unmatched"},{"role":"user","content":"barrier"},{"role":"tool","tool_call_id":"a","content":"late"}]}`)
	body := requestBody(t, NewAPI(Options{}), in)
	data, _ := json.Marshal(body)
	for _, want := range []string{"first", "duplicate", "unmatched", "late", "tool_result without adjacent tool_use"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("lost %s", want)
		}
	}
	contents := array(body["contents"])
	last := array(obj(contents[len(contents)-1])["parts"])
	if obj(last[0])["functionResponse"] != nil {
		t.Fatal("late result crossed barrier")
	}
}

func TestCallIDAllocatorReservesConformingCollision(t *testing.T) {
	msgs := []message{{role: "assistant", parts: []any{object{"functionCall": object{"id": "call:a", "name": "a", "args": object{}}}}}}
	normalizeCallIDs(msgs)
	alias := text(obj(obj(msgs[0].parts[0])["functionCall"])["id"])
	msgs = []message{{role: "assistant", parts: []any{
		object{"functionCall": object{"id": "call:a", "name": "a", "args": object{}}},
		object{"functionCall": object{"id": alias, "name": "b", "args": object{}}},
	}}, {role: "tool", resultID: "call:a", result: "a"}, {role: "tool", resultID: alias, result: "b"}}
	normalizeCallIDs(msgs)
	first := text(obj(obj(msgs[0].parts[0])["functionCall"])["id"])
	second := text(obj(obj(msgs[0].parts[1])["functionCall"])["id"])
	if first == second || first != msgs[1].resultID || second != msgs[2].resultID {
		t.Fatal("call/result identity collision")
	}
}

func TestResponsesAndAnthropicToolReplay(t *testing.T) {
	for _, tc := range []struct {
		wire inference.Wire
		body string
	}{
		{inference.Responses, `{"input":[{"type":"function_call","call_id":"a","name":"f","arguments":"{}","extra_content":{"google":{"thought_signature":"sig-alpha-opaque-value"}}},{"type":"function_call_output","call_id":"a","output":"done"}]}`},
		{inference.Anthropic, `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"summary","signature":"sig-beta-opaque-value"},{"type":"tool_use","id":"a","name":"f","input":{},"extra_content":{"google":{"thought_signature":"sig-alpha-opaque-value"}}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"done"}]}]}`},
	} {
		in := input(tc.body)
		in.Wire = tc.wire
		body := requestBody(t, NewAPI(Options{}), in)
		encoded, _ := json.Marshal(body)
		if !strings.Contains(string(encoded), signatureA) || !strings.Contains(string(encoded), "functionResponse") {
			t.Fatalf("lost replay on %s", tc.wire)
		}
	}
}

func TestUsageRevisionsDoNotKeepStaleDerivedTotal(t *testing.T) {
	u := object{}
	if err := mergeUsage(u, object{"promptTokenCount": json.Number("2"), "candidatesTokenCount": json.Number("1")}); err != nil {
		t.Fatal(err)
	}
	if err := mergeUsage(u, object{"candidatesTokenCount": json.Number("4")}); err != nil {
		t.Fatal(err)
	}
	if u["total_tokens"] != int64(6) {
		t.Fatal(u)
	}
}

func TestStreamToolSignaturesAndMultilineFrame(t *testing.T) {
	stream := "data: {\"candidates\":[{\"content\":{\"parts\":[\n" +
		"data: {\"thought\":true,\"text\":\"hidden\",\"thoughtSignature\":\"sig-alpha-opaque-value\"}]}}]}\n\n" +
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"f\",\"args\":{}}}]},\"finishReason\":\"STOP\"}]}\n\n"
	r, err := NewAPI(Options{}).Decode(context.Background(), upstream(stream, true))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"thought_signature":"`+signatureA+`"`) || strings.Contains(string(data), `"content":"hidden"`) {
		t.Fatal(string(data))
	}
}

func TestCloseReleasesUnreadStream(t *testing.T) {
	body := &blockedBody{closed: make(chan struct{})}
	response := upstream("", true)
	response.Body = body
	r, err := NewAPI(Options{}).Decode(context.Background(), response)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("body not closed")
	}
}

func TestBufferedSignatureAndSummaryRoundTrip(t *testing.T) {
	a := NewAPI(Options{ShowThinkingSummaries: true})
	response := decoded(t, a, `{"candidates":[{"content":{"parts":[{"thought":true,"text":"summary","thoughtSignature":"sig-beta-opaque-value"},{"functionCall":{"id":"a","name":"f","args":{}},"thoughtSignature":"sig-alpha-opaque-value"}]},"finishReason":"STOP"}]}`)
	msg := obj(obj(array(response["choices"])[0])["message"])
	body, _ := json.Marshal(object{"messages": []any{msg, object{"role": "tool", "tool_call_id": "a", "content": "done"}}})
	in := input(string(body))
	request := requestBody(t, a, in)
	parts := array(obj(array(request["contents"])[1])["parts"])
	if len(parts) != 2 || signature(obj(parts[0])) != signatureB || signature(obj(parts[1])) != signatureA {
		t.Fatal("opaque state lost on actual output replay")
	}
}
