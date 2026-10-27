// SPDX-License-Identifier: Apache-2.0
// Fixtures adapted from the pinned opencodex Zed tests; see LICENSE.upstream.
package zed

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

func build(t *testing.T, p Provider, w inference.Wire, body string) object {
	t.Helper()
	a, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.Build(context.Background(), inference.AdapterInput{Operation: inference.Generate, Wire: w, Body: json.RawMessage(body), Target: inference.Target{BaseURL: "https://cloud.zed.dev/", Model: "test-model"}, Credential: inference.Credential{Secret: "test-llm-token"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.URL != "https://cloud.zed.dev/completions" || r.Headers.Get("Authorization") != "Bearer test-llm-token" || r.ResponseWire != a.responseWire() {
		t.Fatalf("unexpected request metadata: %s %s", r.URL, r.ResponseWire)
	}
	if r.Headers.Get("x-zed-client-supports-stream-ended-request-completion-status") != "true" {
		t.Fatal("missing terminal support")
	}
	m, err := decodeObject(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if m["provider"] != string(p) || m["model"] != "test-model" || len(stringOf(m["thread_id"])) != 36 || m["thread_id"] == m["prompt_id"] {
		t.Fatal("invalid envelope")
	}
	return objectOf(m["provider_request"])
}

func TestResponsesNormalization(t *testing.T) {
	r := build(t, OpenAI, inference.Responses, `{"input":[{"role":"developer","content":"instruction"},{"type":"function_call_output","call_id":"call_1","output":"ok"}],"access_programs":{"cyber":["standard"]},"stream":false,"tools":[{"type":"web_search"},{"type":"custom","name":"patch"}],"tool_choice":{"type":"allowed_tools","tools":[{"type":"web_search"},{"type":"custom","name":"patch"}]}}`)
	if r["stream"] != true || r["access_programs"] != nil {
		t.Fatal("private field or stream mismatch")
	}
	input := arrayOf(r["input"])
	if objectOf(input[0])["role"] != "system" || objectOf(arrayOf(objectOf(input[0])["content"])[0])["type"] != "input_text" {
		t.Fatal("normalization missing")
	}
	if objectOf(input[1])["call_id"] != "call_1" || objectOf(input[1])["role"] != nil {
		t.Fatal("tool result shape changed")
	}
	if len(arrayOf(r["tools"])) != 1 || len(arrayOf(objectOf(r["tool_choice"])["tools"])) != 1 {
		t.Fatal("hosted tools not filtered")
	}
}
func TestToolSelectionFiltering(t *testing.T) {
	for _, choice := range []string{`"required"`, `{"type":"web_search"}`, `{"type":"allowed_tools","tools":[{"type":"web_search"}]}`} {
		r := build(t, OpenAI, inference.Responses, `{"input":"hi","tools":[{"type":"web_search"}],"tool_choice":`+choice+`}`)
		if r["tool_choice"] != "none" || len(arrayOf(r["tools"])) != 0 {
			t.Fatalf("choice: %#v", r["tool_choice"])
		}
	}
}
func TestAnthropicNativeToolsAndContent(t *testing.T) {
	r := build(t, Anthropic, inference.Anthropic, `{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{"n":1}}]}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}],"max_tokens":12}`)
	if objectOf(arrayOf(objectOf(arrayOf(r["messages"])[0])["content"])[0])["text"] != "hello" {
		t.Fatal("string not normalized")
	}
	if objectOf(arrayOf(objectOf(arrayOf(r["messages"])[1])["content"])[0])["id"] != "call_1" {
		t.Fatal("tool lost")
	}
}

func TestShortCacheAndExplicitBreakpoints(t *testing.T) {
	r := build(t, Anthropic, inference.Anthropic, `{"system":"instructions","messages":[{"role":"user","content":"one"},{"role":"assistant","content":"two"},{"role":"user","content":"three"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`)
	b, _ := json.Marshal(r)
	if strings.Count(string(b), `"cache_control"`) != 4 || strings.Contains(string(b), `"ttl"`) {
		t.Fatal(string(b))
	}
	r = build(t, Anthropic, inference.Anthropic, `{"messages":[{"role":"user","content":[{"type":"text","text":"one","cache_control":{"type":"ephemeral","ttl":"1h"}},{"type":"text","text":"two"}]}]}`)
	b, _ = json.Marshal(r)
	if strings.Count(string(b), `"cache_control"`) != 1 || !strings.Contains(string(b), `"ttl":"1h"`) {
		t.Fatal(string(b))
	}
}

func TestRejectUnsupportedTranslationAndContinuation(t *testing.T) {
	a, _ := New(Anthropic)
	for _, body := range []string{`{"input":[{"role":"user","content":[{"type":"unknown_part"}]}]}`, `{"input":"hi","previous_response_id":"resp_private"}`} {
		_, err := a.Build(context.Background(), inference.AdapterInput{Operation: inference.Generate, Wire: inference.Responses, Body: json.RawMessage(body), Target: inference.Target{BaseURL: "https://cloud.zed.dev", Model: "claude"}, Credential: inference.Credential{Secret: "llm"}})
		if err == nil {
			t.Fatal("silently lost request semantics")
		}
	}
}

type failureReader struct{}

func (failureReader) Read([]byte) (int, error) { return 0, errors.New("secret-token user-id") }
func (failureReader) Close() error             { return nil }
func TestReadFailureDoesNotEchoCredentials(t *testing.T) {
	s := newEventStream(context.Background(), failureReader{}, OpenAI)
	defer s.Close()
	b, err := io.ReadAll(s)
	if err != nil || !strings.Contains(string(b), "read failed") || strings.Contains(string(b), "secret-token") {
		t.Fatalf("%s: %v", b, err)
	}
}

type tinyReader struct{ text string }

func (r *tinyReader) Read(p []byte) (int, error) {
	if r.text == "" {
		return 0, io.EOF
	}
	p[0] = r.text[0]
	r.text = r.text[1:]
	return 1, nil
}
func (*tinyReader) Close() error { return nil }
func TestSplitUTF8AndCloseBeforeTerminal(t *testing.T) {
	for _, p := range []Provider{Anthropic, OpenAI, XAI, Google} {
		frame := `{"event":{"type":"content_block_delta","delta":{"text":"hello 🌍"}}}`
		if p == Google {
			frame = `{"event":{"candidates":[{"content":{"parts":[{"text":"hello 🌍"}]}}]}}`
		}
		s := newEventStream(context.Background(), &tinyReader{text: frame + "\n{\"status\":\"stream_ended\"}\n"}, p)
		b, err := io.ReadAll(s)
		s.Close()
		if err != nil || !strings.Contains(string(b), "hello 🌍") {
			t.Fatalf("%s: %v", b, err)
		}
	}
	b := &trackedBody{Reader: strings.NewReader(strings.Repeat("{\"event\":{\"type\":\"ping\"}}\n", 100000))}
	s := newEventStream(context.Background(), b, Anthropic)
	var first [1]byte
	if _, err := s.Read(first[:]); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if !b.closed {
		t.Fatal("early consumer close did not release source")
	}
}
func TestCrossWireTools(t *testing.T) {
	for _, p := range []Provider{Anthropic, OpenAI, Google, XAI} {
		t.Run(string(p), func(t *testing.T) {
			r := build(t, p, inference.Chat, `{"messages":[{"role":"system","content":"instructions"},{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup","arguments":"{\"x\":1}"}}]},{"role":"tool","tool_call_id":"c1","content":"result"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"tool_choice":{"type":"function","function":{"name":"lookup"}}}`)
			b, _ := json.Marshal(r)
			if !strings.Contains(string(b), "lookup") || !strings.Contains(string(b), "result") {
				t.Fatal("tool roundtrip lost")
			}
			if p == Google && (r["safetySettings"] != nil || r["model"] != nil || r["stream"] != nil) {
				t.Fatal("invalid Google fields")
			}
		})
	}
}
func TestProviderFallbackAndRefresh(t *testing.T) {
	for _, c := range []struct {
		value, model string
		p            Provider
	}{{"OpenAI", "claude", OpenAI}, {"", "claude-haiku", Anthropic}, {"", "gemini", Google}, {"", "grok", XAI}, {"", "arbitrary", OpenAI}, {"x-ai", "", XAI}} {
		if NormalizeProvider(c.value, c.model) != c.p {
			t.Fatal(c)
		}
	}
	for _, r := range []*http.Response{{StatusCode: 401}, {StatusCode: 200, Header: http.Header{"x-zed-expired-token": []string{""}}}, {StatusCode: 403, Header: http.Header{"X-Zed-Outdated-Token": []string{"true"}}}} {
		if !NeedsTokenRefresh(r) {
			t.Fatal("missing expiry signal")
		}
	}
	if NeedsTokenRefresh(&http.Response{StatusCode: 200}) {
		t.Fatal("spurious refresh")
	}
}
func TestUnsupportedOperationsAndCanceledBuild(t *testing.T) {
	a, _ := New(OpenAI)
	for _, op := range []inference.Operation{inference.Compact, inference.CountTokens, inference.ImageGeneration, inference.Search} {
		_, err := a.Build(context.Background(), inference.AdapterInput{Operation: op})
		if !errors.Is(err, inference.ErrUnsupported) {
			t.Fatalf("%s: %v", op, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Build(ctx, inference.AdapterInput{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }
func streamText(t *testing.T, p Provider, input string) string {
	t.Helper()
	a, _ := New(p)
	b := &trackedBody{Reader: strings.NewReader(input)}
	r, err := a.Decode(context.Background(), &http.Response{StatusCode: 200, Header: http.Header{"Content-Length": []string{"100"}}, Body: b})
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if !b.closed || r.Header.Get("Content-Length") != "" {
		t.Fatal("body ownership or headers")
	}
	return string(data)
}
func TestStreamFramingFailures(t *testing.T) {
	for _, tc := range []struct{ name, input, message string }{
		{"malformed", "{not json\n", "malformed frame"},
		{"array", "[]\n", "malformed frame"},
		{"null-event", "{\"event\":null}\n", "malformed frame"},
		{"eof", "{\"event\":{\"type\":\"ping\"}}\n", "before completion"},
		{"partial", "{\"event\":", "partial frame"},
		{"oversize", strings.Repeat("x", MaxFrameBytes+1), "size limit"},
		{"oversize-newline", strings.Repeat("x", MaxFrameBytes+1) + "\n", "size limit"},
		{"secret-status", "{\"status\":{\"failed\":{\"message\":\"secret-token user-id\"}}}\n", "Zed request failed"},
		{"secret-native", "{\"event\":{\"type\":\"error\",\"error\":{\"message\":\"secret-token user-id\"}}}\n", "Zed request failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := streamText(t, Anthropic, tc.input)
			if !strings.Contains(out, tc.message) || strings.Contains(out, "secret-token") || strings.Contains(out, "user-id") || strings.Contains(out, "message_stop") {
				t.Fatal(out)
			}
		})
	}
}
func TestExplicitTerminalStopsAndPreservesUsage(t *testing.T) {
	out := streamText(t, XAI, ": comment\r\nevent: message\r\ndata: {\"event\":{\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}}\r\n{\"event\":{\"choices\":[],\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":7}}}\n{\"status\":\"stream_ended\"}\n{\"event\":{\"late\":true}}\n")
	if !strings.Contains(out, `"completion_tokens":7`) || !strings.Contains(out, `"prompt_tokens":0`) || strings.Contains(out, "late") || strings.Count(out, "[DONE]") != 1 {
		t.Fatal(out)
	}
	unknown := streamText(t, XAI, "{\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n")
	if strings.Contains(unknown, "usage") {
		t.Fatal("invented usage")
	}
}
func TestResponsesTerminalSnapshotNotOverwritten(t *testing.T) {
	out := streamText(t, OpenAI, "{\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"function_call\",\"call_id\":\"c1\"}],\"usage\":{\"input_tokens\":2}}}\n{\"status\":\"stream_ended\"}\n")
	if strings.Count(out, "response.completed") != 1 || !strings.Contains(out, `"call_id":"c1"`) {
		t.Fatal(out)
	}
}
func TestGoogleToolsAndUsage(t *testing.T) {
	out := streamText(t, Google, "{\"event\":{\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"lookup\",\"args\":{\"x\":1}}}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":3,\"thoughtsTokenCount\":2,\"cachedContentTokenCount\":4}}}\n{\"status\":\"completed\"}\n")
	for _, want := range []string{`"name":"lookup"`, `"finish_reason":"tool_calls"`, `"completion_tokens":5`, `"cached_tokens":4`, `"prompt_tokens":10`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s: %s", want, out)
		}
	}
	unknown := streamText(t, Google, "{\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":-1,\"candidatesTokenCount\":1.5}}\n")
	if strings.Contains(unknown, "prompt_tokens") || strings.Contains(unknown, "completion_tokens") {
		t.Fatal(unknown)
	}
}

func TestRichContentFailsWithoutSilentLoss(t *testing.T) {
	for _, part := range []string{`{"executableCode":{"code":"print(1)"}}`, `{"functionCall":{"name":"lookup","args":[]}}`} {
		out := streamText(t, Google, `{"candidates":[{"content":{"parts":[`+part+`]},"finishReason":"STOP"}]}`+"\n")
		if !strings.Contains(out, "unsupported Google event") {
			t.Fatal(out)
		}
	}
}
func TestCancellationClosesBlockedUpstream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer writer.Close()
	a, _ := New(Anthropic)
	r, err := a.Decode(ctx, &http.Response{StatusCode: 200, Body: reader, Header: make(http.Header)})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(r.Body); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked cancellation")
	}
	r.Body.Close()
}
func TestConcurrentAccountsDoNotShareState(t *testing.T) {
	a, _ := New(XAI)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := a.Decode(context.Background(), &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{\"status\":\"stream_ended\"}\n"))})
			if err != nil {
				t.Error(err)
				return
			}
			defer r.Body.Close()
			_, err = io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
