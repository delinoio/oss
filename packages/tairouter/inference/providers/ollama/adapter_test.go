// SPDX-License-Identifier: Apache-2.0
package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

func input(body string) inference.AdapterInput {
	return inference.AdapterInput{Operation: inference.Generate, Wire: inference.Chat, Body: json.RawMessage(body), Target: inference.Target{BaseURL: "https://ollama.com/v1", Model: "glm-5.3", Auth: inference.APIKey}, Credential: inference.Credential{Secret: "fixture"}}
}
func build(t *testing.T, in inference.AdapterInput) object {
	t.Helper()
	r, e := New().Build(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	b, e := decode(r.Body)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func response(ct, body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {ct}}, Body: io.NopCloser(strings.NewReader(body))}
}
func TestNativeRequest(t *testing.T) {
	in := input(`{"messages":[{"role":"developer","content":"system"},{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGk="}}]},{"role":"assistant","content":"answer","reasoning_content":"reason"}],"reasoning_effort":"high","max_tokens":17,"temperature":0.2,"top_p":0.8,"stop":["end"],"presence_penalty":0.1,"frequency_penalty":0.2,"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"},"strict":true}}]}`)
	r, e := New().Build(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	if r.URL != "https://ollama.com/api/chat" || r.Headers.Get("Authorization") != "Bearer fixture" || r.ResponseWire != inference.Chat {
		t.Fatalf("request %+v", r)
	}
	b, _ := decode(r.Body)
	if b["think"] != "high" || obj(b["options"])["num_predict"] != json.Number("17") {
		t.Fatal(b)
	}
	m := arr(b["messages"])
	if obj(m[0])["role"] != "system" || arr(obj(m[1])["images"])[0] != "aGk=" || obj(m[2])["thinking"] != "reason" {
		t.Fatal(m)
	}
	if obj(obj(arr(b["tools"])[0])["function"])["strict"] != nil {
		t.Fatal("strict leaked")
	}
}
func TestRequestRefusalsAndFormats(t *testing.T) {
	for _, body := range []string{`{"messages":[],"response_format":{"type":"json_object"}}`, `{"messages":[],"tools":[{}],"tool_choice":"required"}`, `{"messages":[{"role":"tool","tool_call_id":"orphan","content":"x"}]}`, `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/image"}}]}]}`, `{"messages":[{"role":"user","content":[{"type":"input_audio"}]}]}`} {
		if _, e := New().Build(context.Background(), input(body)); e == nil {
			t.Fatal(body)
		}
	}
	for _, f := range []string{`{"type":"json_object"}`, `{"type":"json_schema","json_schema":{"schema":{"type":"object"}}}`} {
		in := input(`{"messages":[],"response_format":` + f + `}`)
		in.Target.BaseURL = "https://custom.test/api"
		if build(t, in)["format"] == nil {
			t.Fatal("missing format")
		}
	}
	for _, base := range []string{"http://ollama.com", "https://www.ollama.com", "https://ollama.com:444", "https://custom.test/v1", "https://custom.test/?secret=x", "https://user:pass@custom.test", "https://custom.test/%61pi"} {
		in := input(`{"messages":[]}`)
		in.Target.BaseURL = base
		if _, e := New().Build(context.Background(), in); e == nil {
			t.Fatal(base)
		}
	}
	in := input(`{"messages":[]}`)
	in.Target.BaseURL = "http://127.0.0.1:11434/v1"
	in.Target.Headers = http.Header{"X-Api-Key": {"old"}, "Authorization": {"old"}}
	r, e := New().Build(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	if r.Headers.Get("Authorization") != "" || r.Headers.Get("X-Api-Key") != "" {
		t.Fatal(r.Headers)
	}
	in.Target.BaseURL = "http://custom.test"
	if _, e := New().Build(context.Background(), in); e == nil {
		t.Fatal("plaintext credential")
	}
}
func TestReplayAdjacencyAndLateAttribution(t *testing.T) {
	b := build(t, input(`{"messages":[{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"weather","arguments":"{}"}}]},{"role":"user","content":"intervening"},{"role":"tool","tool_call_id":"a","content":"first"},{"role":"assistant","content":"settled"},{"role":"tool","tool_call_id":"a","content":"later"}]}`))
	m := arr(b["messages"])
	if obj(m[1])["role"] != "tool" || obj(m[1])["tool_name"] != "weather" || obj(m[2])["content"] != "intervening" || !strings.Contains(str(obj(m[4])["content"]), "previously issued tool") {
		t.Fatal(m)
	}
	b = build(t, input(`{"messages":[{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"weather","arguments":"{}"}}]}]}`))
	if !strings.Contains(str(obj(arr(b["messages"])[1])["content"]), "execution status unknown") {
		t.Fatal(b)
	}
}
func TestJSONTerminalAndThinking(t *testing.T) {
	a := New()
	for _, body := range []string{`{"done":false,"message":{"content":"partial"}}`, `{"message":{}}`, `{"done":true,"message":{"tool_calls":[{"function":{"name":"x","arguments":"{}"}}]}}`, `{"done":true,"message":{"content":4}}`, `{"done":true} trailing`} {
		if r, e := a.Decode(context.Background(), response("application/json", body)); e == nil {
			r.Body.Close()
			t.Fatal(body)
		}
	}
	r, e := a.Decode(context.Background(), response("application/json", `{"done":true,"model":"m","message":{"content":"answer","thinking":"reason"},"prompt_eval_count":2,"eval_count":3}`))
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	data, _ := io.ReadAll(r.Body)
	b, _ := decode(data)
	msg := obj(obj(arr(b["choices"])[0])["message"])
	if msg["reasoning_content"] != "reason" || obj(b["usage"])["completion_tokens"] != json.Number("3") {
		t.Fatal(string(data))
	}
}
func TestStreamLateIdentityAndReplacement(t *testing.T) {
	body := `{"message":{"thinking":"reason","content":"hello"},"done":false}
{"message":{"tool_calls":[{"function":{"index":0,"name":"weather","arguments":{"city":"Seoul"}}}]},"done":false}
{"message":{"tool_calls":[{"id":"late-id","function":{"index":0,"name":"weather","arguments":{"city":"Busan"}}}]},"done":false}
{"done":true,"prompt_eval_count":3,"eval_count":5}`
	r, e := New().Decode(context.Background(), response("application/x-ndjson", body))
	if e != nil {
		t.Fatal(e)
	}
	data, e := io.ReadAll(r.Body)
	if e != nil {
		t.Fatal(e)
	}
	s := string(data)
	if !strings.Contains(s, "late-id") || !strings.Contains(s, "Busan") || strings.Contains(s, "Seoul") || strings.Count(s, `"tool_calls":[`) != 1 || !strings.Contains(s, "[DONE]") {
		t.Fatal(s)
	}
}
func TestStreamInvalidAndBounds(t *testing.T) {
	for _, body := range []string{`{"message":{"content":"partial"}}`, "{bad}\n", `{"done":"true"}`, `{"message":{"tool_calls":[{"function":{"name":"x","arguments":{}}}]}}`, strings.Repeat("x", MaxFrameBytes+1), "{\"message\":{\"content\":\"\xff\"}}\n"} {
		r, e := New().Decode(context.Background(), response("application/x-ndjson", body))
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(r.Body)
		if e == nil || strings.Contains(string(data), "[DONE]") || strings.Contains(string(data), `"tool_calls":[`) {
			t.Fatalf("accepted malformed stream len=%d data=%s err=%v", len(body), data, e)
		}
	}
	calls := []any{}
	for i := 0; i <= MaxPendingCalls; i++ {
		calls = append(calls, object{"function": object{"index": i, "name": "f", "arguments": object{}}})
	}
	b, _ := json.Marshal(object{"message": object{"tool_calls": calls}, "done": true})
	r, e := New().Decode(context.Background(), response("application/x-ndjson", string(b)))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = io.ReadAll(r.Body); e == nil {
		t.Fatal("call cap")
	}
}
func TestContextualIDsAndParallel(t *testing.T) {
	in := input(`{"messages":[{"role":"assistant","tool_calls":[{"id":"old","function":{"name":"f","arguments":"{}"}}]}],"parallel_tool_calls":false}`)
	payload := `{"done":true,"message":{"tool_calls":[{"id":"old","function":{"name":"f","arguments":{}}}]}}`
	r, e := New().DecodeWithInput(context.Background(), in, response("application/json", payload))
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(r.Body)
	if strings.Contains(string(data), `"id":"old"`) {
		t.Fatal("reused history ID")
	}
	payload = `{"done":true,"message":{"tool_calls":[{"id":"one","function":{"index":0,"name":"f","arguments":{}}},{"id":"two","function":{"index":0,"name":"f","arguments":{}}}]}}`
	if _, e = New().DecodeWithInput(context.Background(), in, response("application/json", payload)); e == nil {
		t.Fatal("parallel refusal")
	}
}
func TestConcurrentAdapter(t *testing.T) {
	a := New()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := input(`{"messages":[{"role":"user","content":"hello"}]}`)
			if _, e := a.Build(context.Background(), in); e != nil {
				t.Error(e)
			}
			r, e := a.DecodeWithInput(context.Background(), in, response("application/x-ndjson", "{\"done\":true,\"message\":{\"content\":\"hi\"}}\n"))
			if e != nil {
				t.Error(e)
				return
			}
			if _, e = io.ReadAll(r.Body); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
}

func TestResolvedPolicy(t *testing.T) {
	for _, tc := range []struct {
		effort, policy string
		want           any
	}{
		{"ultra", `{"supported_reasoning":["low","medium","high"]}`, "high"},
		{"high", `{"reasoning_map":{"high":"__omit__"}}`, nil},
		{"ultra", `{"supported_reasoning":["low","high"],"reasoning_map":{"high":"__omit__"}}`, nil},
		{"ultra", `{"supported_reasoning":["low","high"],"reasoning_map":{"max":"medium"}}`, "medium"},
		{"minimal", `{}`, "low"},
		{"high", `{"no_reasoning":true}`, false},
	} {
		in := input(`{"messages":[],"reasoning_effort":"` + tc.effort + `"}`)
		in.Credential.Headers = http.Header{PolicyHeader: {tc.policy}}
		r, e := New().Build(context.Background(), in)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := decode(r.Body)
		if b["think"] != tc.want {
			t.Fatalf("%s %s got=%v want=%v", tc.effort, tc.policy, b["think"], tc.want)
		}
		if r.Headers.Get(PolicyHeader) != "" {
			t.Fatal("metadata leaked")
		}
	}
	in := input(`{"messages":[],"temperature":1,"top_p":1,"presence_penalty":1}`)
	in.Credential.Headers = http.Header{PolicyHeader: {`{"default_max_output_tokens":42,"no_temperature":true,"no_top_p":true,"no_penalty":true}`}}
	o := obj(build(t, in)["options"])
	if len(o) != 1 || o["num_predict"] != json.Number("42") {
		t.Fatal(o)
	}
}

type closeCounter struct {
	io.Reader
	mu     sync.Mutex
	closed int
}

func (c *closeCounter) Close() error { c.mu.Lock(); defer c.mu.Unlock(); c.closed++; return nil }
func TestDecoderClosesBodyOnce(t *testing.T) {
	source := &closeCounter{Reader: strings.NewReader("{\"done\":true}\n")}
	r := response("application/x-ndjson", "")
	r.Body = source
	out, e := New().Decode(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	io.ReadAll(out.Body)
	out.Body.Close()
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.closed != 1 {
		t.Fatalf("closed %d times", source.closed)
	}
}
func TestCanceledDecoderUnblocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer writer.Close()
	r := response("application/x-ndjson", "")
	r.Body = reader
	out, e := New().Decode(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := io.ReadAll(out.Body); done <- e }()
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("missing cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not release reader")
	}
}
func TestLateAttributionLimit(t *testing.T) {
	messages := []any{object{"role": "assistant", "tool_calls": []any{object{"id": "a", "function": object{"name": "f", "arguments": "{}"}}}}, object{"role": "assistant", "tool_calls": []any{object{"id": "b", "function": object{"name": "g", "arguments": "{}"}}}}}
	for i := 0; i < 6000; i++ {
		messages = append(messages, object{"role": "tool", "tool_call_id": "a", "content": "x"})
	}
	if _, e := nativeMessages(messages); e == nil {
		t.Fatal("late attribution limit not enforced")
	}
}

func TestNamespacedAndCustomTools(t *testing.T) {
	in := input(`{"messages":[{"role":"assistant","tool_calls":[{"id":"call","type":"custom","custom":{"name":"run","namespace":"code","input":"print(1)"}}]}],"tools":[{"type":"namespace","name":"code","tools":[{"type":"custom","custom":{"name":"run","description":"run text"}}]}]}`)
	b := build(t, in)
	fn := obj(obj(arr(b["tools"])[0])["function"])
	if fn["name"] != "code__run" {
		t.Fatal(fn)
	}
	call := obj(obj(arr(obj(arr(b["messages"])[0])["tool_calls"])[0])["function"])
	if call["name"] != "code__run" || obj(call["arguments"])["input"] != "print(1)" {
		t.Fatal(call)
	}
}
