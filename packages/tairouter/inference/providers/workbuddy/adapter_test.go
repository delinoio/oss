// SPDX-License-Identifier: Apache-2.0
package workbuddy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

func fixture() inference.AdapterInput {
	return inference.AdapterInput{Principal: inference.Principal{OrganizationID: "org", UserID: "user"}, Target: inference.Target{Provider: "workbuddy-ai", BaseURL: BaseURL, Model: "fixture-model", Wire: Wire, Headers: http.Header{"Authorization": {"foreign"}, "Host": {"evil"}, "X-Api-Key": {"foreign"}}}, Credential: inference.Credential{AccountID: "account", Secret: "fixture-access", Headers: http.Header{"X-User-Id": {"fixture-user"}, "X-Domain": {"international"}, "X-Enterprise-Id": {"fixture-team"}}}, Operation: inference.Generate, Wire: inference.Chat, Body: json.RawMessage(`{"model":"default","messages":[{"role":"user","content":"Hello"}],"stream":false}`)}
}
func build(t *testing.T, a *Adapter, in inference.AdapterInput) (inference.AdapterRequest, object) {
	t.Helper()
	r, e := a.Build(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	m, e := decode(r.Body)
	if e != nil {
		t.Fatal(e)
	}
	return r, m
}
func TestPinnedRequest(t *testing.T) {
	in := fixture()
	in.Body = json.RawMessage(`{"messages":[{"role":"developer","content":"policy"},{"role":"user","content":"Hello"}],"max_completion_tokens":42,"tools":[{"type":"function","function":{"name":"lookup.item","parameters":{"properties":{"limit":{"enum":[1,null]}}}}}],"tool_choice":{"type":"function","function":{"name":"lookup.item"}}}`)
	r, b := build(t, New(), in)
	if r.URL != BaseURL+"/chat/completions" || r.ResponseWire != inference.Chat || b["stream"] != true || b["model"] != "fixture-model" {
		t.Fatal(r.URL, b)
	}
	if r.Headers.Get("Authorization") != "Bearer fixture-access" || r.Headers.Get("X-User-Id") != "fixture-user" || r.Headers.Get("X-Tenant-Id") != "fixture-team" || r.Headers.Get("X-Agent-Type") != "main" || r.Headers.Get("Host") != "" || r.Headers.Get("X-Api-Key") != "" {
		t.Fatal("identity override")
	}
	if obj(arr(b["messages"])[0])["role"] != "system" || b["max_tokens"] != json.Number("42") || b["max_completion_tokens"] != nil {
		t.Fatal(b)
	}
	f := obj(obj(arr(b["tools"])[0])["function"])
	alias := str(f["name"])
	if alias == "lookup.item" || !safeName.MatchString(alias) || b["tool_choice"] != alias || obj(f["parameters"])["type"] != "object" {
		t.Fatal(b)
	}
	if strings.Contains(string(in.Body), "wb_") {
		t.Fatal("input mutated")
	}
	payload := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"` + alias + `","arguments":"{}"}}]},"finish_reason":null}]}\n\ndata: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}\n\ndata: [DONE]\n\n`
	response, e := New().DecodeWithInput(context.Background(), in, sse(strings.ReplaceAll(payload, `\n`, "\n")))
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	out, e := io.ReadAll(response.Body)
	if e != nil || !strings.Contains(string(out), "lookup.item") || strings.Contains(string(out), alias) {
		t.Fatal(string(out), e)
	}
}
func sse(s string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(s))}
}
func TestDestinationAndMetadata(t *testing.T) {
	for _, url := range []string{"https://other.example/v2", "http://www.workbuddy.ai/v2", "https://www.workbuddy.ai:8443/v2", BaseURL + "?x=1", BaseURL + "#x", "https://a@www.workbuddy.ai/v2", "https://www.workbuddy.ai/evil"} {
		in := fixture()
		in.Target.BaseURL = url
		if _, e := New().Build(context.Background(), in); e == nil {
			t.Fatal(url)
		}
	}
	for _, key := range []string{"X-User-Id", "X-Domain", "X-Enterprise-Id"} {
		in := fixture()
		in.Credential.Headers.Set(key, "bad\nheader")
		if _, e := New().Build(context.Background(), in); e == nil {
			t.Fatal(key)
		}
	}
	in := fixture()
	in.Credential.Headers = nil
	if _, e := New().Build(context.Background(), in); e == nil {
		t.Fatal("missing identity accepted")
	}
}
func TestConversationBinding(t *testing.T) {
	a := New()
	in := fixture()
	r, _ := build(t, a, in)
	in.Body = json.RawMessage(`{"messages":[{"role":"user","content":"Hello"},{"role":"assistant","tool_calls":[{"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call","content":"result"}]}`)
	r2, _ := build(t, New(), in)
	if r.Headers.Get("X-Conversation-ID") != r2.Headers.Get("X-Conversation-ID") || r.Headers.Get("X-Conversation-Request-ID") != r2.Headers.Get("X-Conversation-Request-ID") {
		t.Fatal("tool continuation changed turn")
	}
	in.Body = json.RawMessage(`{"messages":[{"role":"user","content":"Hello"},{"role":"user","content":"Next"}]}`)
	r3, _ := build(t, a, in)
	if r3.Headers.Get("X-Conversation-Request-ID") == r2.Headers.Get("X-Conversation-Request-ID") {
		t.Fatal("next turn reused")
	}
	in.Credential.AccountID = "other"
	r4, _ := build(t, a, in)
	if r4.Headers.Get("X-Conversation-ID") == r3.Headers.Get("X-Conversation-ID") {
		t.Fatal("account scope reused")
	}
}
func TestSchemaAndNoTools(t *testing.T) {
	var v any
	json.Unmarshal([]byte(`{"anyOf":[{"properties":{"x":{"enum":[1,"one"]},"y":{"items":[{"type":"String"},false]}}}],"default":{"type":"String"},"properties":{"pattern":{"pattern":"\\0x\\01\\\\0"}},"required":"invalid"}`), &v)
	p, e := ToolParameters(v)
	if e != nil {
		t.Fatal(e)
	}
	if obj(p["default"])["type"] != "String" || obj(obj(p["properties"])["y"])["items"] == nil || p["anyOf"] != nil || p["required"] != nil {
		t.Fatal(p)
	}
	deep := object{}
	next := deep
	for i := 0; i < 66; i++ {
		child := object{}
		next["items"] = child
		next = child
	}
	if _, e := ToolParameters(deep); e == nil {
		t.Fatal("depth accepted")
	}
	in := fixture()
	in.Body = json.RawMessage(`{"messages":[],"tools":[],"tool_choice":"none"}`)
	_, b := build(t, New(), in)
	if b["tools"] != nil || b["tool_choice"] != nil || obj(arr(b["messages"])[0])["content"] != "You are a helpful assistant." {
		t.Fatal(b)
	}
}
func TestCrossWireAndConcurrentIsolation(t *testing.T) {
	a := New()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := fixture()
			in.Wire = inference.Responses
			in.Body = json.RawMessage(`{"model":"m","input":"Hello","max_output_tokens":32,"tools":[{"type":"function","name":"lookup.item","parameters":{}}],"tool_choice":{"type":"function","name":"lookup.item"}}`)
			_, b := build(t, a, in)
			if b["max_tokens"] != json.Number("32") {
				t.Error(b)
			}
		}()
	}
	wg.Wait()
	in := fixture()
	in.Wire = inference.Anthropic
	in.Body = json.RawMessage(`{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":5,"tools":[{"name":"lookup.item","input_schema":{"type":"object"}}]}`)
	build(t, a, in)
}
func TestStreamBoundsErrorsAndCancellation(t *testing.T) {
	for _, raw := range []string{"data: not-json\n\n", "data: " + strings.Repeat("x", maxEvent) + "\n\n", "data: {\"error\":{\"message\":\"private\"}}\n\n"} {
		r, e := New().Decode(context.Background(), sse(raw))
		if e != nil {
			t.Fatal(e)
		}
		_, e = io.ReadAll(r.Body)
		r.Body.Close()
		if e == nil {
			t.Fatal("bad stream accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	r, e := New().Decode(ctx, &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader})
	if e != nil {
		t.Fatal(e)
	}
	cancel()
	_, e = io.ReadAll(r.Body)
	r.Body.Close()
	writer.Close()
	if e == nil {
		t.Fatal("cancellation ignored")
	}
	for _, code := range []string{"11128", "11140", "14018"} {
		d, e := New().RecoverHTTP(context.Background(), fixture(), inference.HTTPFailure{Status: 400, Body: []byte(`{"code":` + code + `,"msg":"private"}`)})
		if e != nil || d.Action != inference.RecoveryStop || strings.Contains(d.Error.Code, "private") {
			t.Fatal(d, e)
		}
	}
}

func TestCustomNamespaceAndFragmentedAliases(t *testing.T) {
	in := fixture()
	in.Wire = inference.Responses
	in.Body = json.RawMessage(`{"input":"hi","tools":[{"type":"namespace","name":"workspace","tools":[{"type":"custom","name":"exec.shell"}]}],"tool_choice":{"type":"custom","name":"exec.shell","namespace":"workspace"}}`)
	a := New()
	_, b := build(t, a, in)
	f := obj(obj(arr(b["tools"])[0])["function"])
	alias := str(f["name"])
	first, _ := json.Marshal(object{"choices": []any{object{"index": 0, "delta": object{"tool_calls": []any{object{"index": 0, "id": "call", "type": "function", "function": object{"name": alias[:10]}}}}}}})
	second, _ := json.Marshal(object{"choices": []any{object{"index": 0, "delta": object{"tool_calls": []any{object{"index": 0, "function": object{"name": alias[10:], "arguments": `{"input":"echo hi"}`}}}}, "finish_reason": "tool_calls"}}})
	r, e := a.DecodeWithInput(context.Background(), in, sse("data: "+string(first)+"\n\ndata: "+string(second)+"\n\ndata: [DONE]\n\n"))
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	out, e := io.ReadAll(r.Body)
	if e != nil || !strings.Contains(string(out), `"tool_kind":"custom"`) || !strings.Contains(string(out), `"tool_namespace":"workspace"`) || !strings.Contains(string(out), `workspace__exec.shell`) {
		t.Fatal(string(out), e)
	}
	in = fixture()
	in.Body = json.RawMessage(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"custom","custom":{"name":"exec.shell"}}],"tool_choice":{"type":"custom","custom":{"name":"exec.shell"}}}`)
	_, b = build(t, a, in)
	if obj(arr(b["tools"])[0])["type"] != "function" {
		t.Fatal(b)
	}
}

func TestSessionPrecedenceAndPersonalIdentity(t *testing.T) {
	a := New()
	in := fixture()
	in.Credential.Headers.Del("X-Enterprise-Id")
	in.Target.Headers.Set("X-Tenant-Id", "foreign")
	in.ClientHeaders = http.Header{"Thread-Id": {"own"}, "X-Codex-Parent-Thread-Id": {"parent"}, "Session_id": {"fallback"}}
	r, _ := build(t, a, in)
	if r.Headers.Get("X-Tenant-Id") != "" || r.Headers.Get("X-Enterprise-Id") != "" {
		t.Fatal("personal account inherited team")
	}
	in.ClientHeaders.Set("X-Codex-Parent-Thread-Id", "different-parent")
	r2, _ := build(t, a, in)
	if r2.Headers.Get("X-Conversation-ID") != r.Headers.Get("X-Conversation-ID") {
		t.Fatal("own thread did not take priority")
	}
	in.ClientHeaders.Set("Thread-Id", "other-own")
	r3, _ := build(t, a, in)
	if r3.Headers.Get("X-Conversation-ID") == r.Headers.Get("X-Conversation-ID") {
		t.Fatal("own thread ignored")
	}
}

func TestKnownDiagnosticsPreserveRecovery(t *testing.T) {
	for _, status := range []int{401, 429, 500, 503} {
		d, e := New().RecoverHTTP(context.Background(), fixture(), inference.HTTPFailure{Status: status, Body: []byte(`{"code":11140,"msg":"private"}`)})
		if e != nil || d.Action != inference.RecoveryDefault || d.Error.Code != "workbuddy_account_restricted" {
			t.Fatal(status, d, e)
		}
	}
}

func TestSSEHeartbeatAndTerminalBoundary(t *testing.T) {
	r, e := New().Decode(context.Background(), sse(": heartbeat\n\ndata:   \n\ndata:   [DONE]  \n\ndata: invalid-after-terminal\n\n"))
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	out, e := io.ReadAll(r.Body)
	if e != nil || string(out) != "data: [DONE]\n\n" {
		t.Fatal(string(out), e)
	}
}
