// SPDX-License-Identifier: Apache-2.0
package commandcode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

func fixtureInput(body string) inference.AdapterInput {
	return inference.AdapterInput{Operation: inference.Generate, Wire: inference.Chat, Target: inference.Target{Provider: "command-code", BaseURL: "https://api.commandcode.ai", Model: "glm-5.2", Auth: inference.OAuth}, Credential: inference.Credential{Secret: "fixture", AccountID: "account"}, Body: json.RawMessage(body)}
}
func TestProprietaryPayloadAndPairing(t *testing.T) {
	in := fixtureInput(`{"model":"glm-5.2","reasoning_effort":"ultra","messages":[{"role":"system","content":"rules"},{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"first","arguments":"{}"}},{"id":"b","type":"function","function":{"name":"second","arguments":"{}"}}]},{"role":"tool","tool_call_id":"a","content":[{"type":"text","text":"done"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]},{"role":"user","content":"next"},{"role":"tool","tool_call_id":"orphan","content":"lost"}]}`)
	r, err := New().Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if r.URL != "https://api.commandcode.ai/alpha/generate" || r.Headers.Get("Authorization") != "Bearer fixture" || r.Headers.Get("User-Agent") != "cli" {
		t.Fatalf("bad transport %+v", r.Headers)
	}
	var b object
	json.Unmarshal(r.Body, &b)
	p := obj(b["params"])
	if p["stream"] != true || p["model"] != "zai-org/GLM-5.2" || p["reasoning_effort"] != "max" {
		t.Fatalf("params %v", p)
	}
	ms := arr(p["messages"])
	want := []string{"assistant", "tool", "tool", "user", "user", "user"}
	if len(ms) != len(want) {
		t.Fatalf("messages %s", r.Body)
	}
	for i, v := range want {
		if obj(ms[i])["role"] != v {
			t.Fatalf("order %s", r.Body)
		}
	}
	result := obj(arr(obj(ms[2])["content"])[0])
	if obj(result["output"])["type"] != "error-text" || result["toolCallId"] != "b" {
		t.Fatalf("missing synthesized result %v", result)
	}
	config := obj(b["config"])
	if config["workingDir"] != nil || len(arr(config["structure"])) != 0 || b["memory"] != "" || b["taste"] != nil || b["skills"] != nil {
		t.Fatalf("local context leaked %v", config)
	}
}
func TestInboundWires(t *testing.T) {
	for _, tc := range []struct {
		wire inference.Wire
		body string
	}{{inference.Chat, `{"messages":[{"role":"user","content":"hi"}]}`}, {inference.Responses, `{"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`}, {inference.Anthropic, `{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"max_tokens":100}`}} {
		in := fixtureInput(tc.body)
		in.Wire = tc.wire
		r, e := New().Build(context.Background(), in)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(string(r.Body), `"text":"hi"`) {
			t.Fatalf("%s: %s", tc.wire, r.Body)
		}
	}
}
func TestEffortPolicy(t *testing.T) {
	for _, tc := range []struct{ model, effort, want string }{{"glm-5.2", "ultra", "max"}, {"glm-5.2", "low", ""}, {"deepseek-v4-flash", "xhigh", "xhigh"}, {"meta/muse-spark-1.2", "ultra", ""}, {"unlisted", "high", ""}} {
		if got := SupportedEffort(Policy{}, tc.model, tc.effort); got != tc.want {
			t.Fatalf("%+v got %s", tc, got)
		}
	}
	p := Policy{Authoritative: true, ReasoningEfforts: map[string][]string{"zai-org/GLM-5.2": {"low", "ultra"}}}
	if SupportedEffort(p, "glm-5.2", "ultra") != "ultra" {
		t.Fatal("authoritative effort rewritten")
	}
	p.RejectedEfforts = map[string][]string{"zai-org/GLM-5.2": {"ultra"}}
	if SupportedEffort(p, "glm-5.2", "ultra") != "" {
		t.Fatal("rejected rung returned")
	}
	ctx := WithPolicy(context.Background(), p)
	p.ReasoningEfforts["zai-org/GLM-5.2"][0] = "mutated"
	if SupportedEffort(policy(ctx), "glm-5.2", "low") != "low" {
		t.Fatal("mutable policy escaped")
	}
}
func TestRefusalsAndRepair(t *testing.T) {
	a := New()
	in := fixtureInput(`{"messages":[],"reasoning_effort":"high"}`)
	for _, tc := range []struct {
		status     int
		body, code string
	}{{403, `{"success":false,"error":{"code":"FORBIDDEN","message":"MODEL_NOT_IN_PLAN: reasoning_effort credits"}}`, "model_not_in_plan"}, {403, `{"success":false,"error":{"code":"FORBIDDEN","message":"MODEL_NOT_IN_PLAN_OTHER"}}`, ""}, {400, `{"success":false,"error":{"code":"BAD_REQUEST","message":"You have insufficient credits to make this request. refill"}}`, "insufficient_credits"}} {
		d, e := a.RecoverHTTP(context.Background(), in, inference.HTTPFailure{Status: tc.status, Body: []byte(tc.body)})
		if e != nil {
			t.Fatal(e)
		}
		if tc.code == "" {
			if d.Error != nil {
				t.Fatal(d)
			}
		} else if d.Error == nil || d.Error.Code != tc.code {
			t.Fatalf("decision %+v", d)
		}
	}
	r, e := a.Build(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	f := inference.HTTPFailure{Status: 400, Body: []byte(`{"error":"unsupported effort"}`)}
	if _, changed, _ := a.RepairRequest(context.Background(), in, r, f); changed {
		t.Fatal("repair without profile evidence")
	}
	ctx := WithPolicy(context.Background(), Policy{RefreshedEfforts: map[string][]string{"zai-org/GLM-5.2": {"max"}}})
	next, changed, e := a.RepairRequest(ctx, in, r, f)
	if e != nil || !changed || strings.Contains(string(next.Body), "reasoning_effort") {
		t.Fatalf("repair %v %v %s", changed, e, next.Body)
	}
	ctx = WithPolicy(context.Background(), Policy{Authoritative: true, ReasoningEfforts: map[string][]string{"zai-org/GLM-5.2": {"high"}}, RefreshedEfforts: map[string][]string{"zai-org/GLM-5.2": {"max"}}})
	if _, changed, _ := a.RepairRequest(ctx, in, r, f); changed {
		t.Fatal("authoritative request downgraded")
	}
}
func decodeFixture(t *testing.T, in inference.AdapterInput, body string) string {
	t.Helper()
	r, e := New().DecodeWithInput(context.Background(), in, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))})
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	b, e := io.ReadAll(r.Body)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func TestNDJSONEvents(t *testing.T) {
	out := decodeFixture(t, fixtureInput(`{"messages":[]}`), "null\n[]\njunk\n"+`{"type":"text-delta","text":"hello"}`+"\n"+`data: {"type":"reasoning-delta","text":"think"}`+"\n"+`{"type":"tool-call","toolCallId":"c","toolName":"run","input":{"x":1}}`+"\n"+`{"type":"finish-step","finishReason":"tool-calls","usage":{"inputTokens":4,"outputTokens":2,"inputTokenDetails":{"cacheReadTokens":3,"cacheWriteTokens":1}}}`+"\n"+`{"type":"finish","usage":{"inputTokens":99}}`)
	for _, s := range []string{`"content":"hello"`, `"reasoning_content":"think"`, `"name":"run"`, `"finish_reason":"tool_calls"`, `"cached_tokens":3`, `"cache_creation_tokens":1`} {
		if !strings.Contains(out, s) {
			t.Fatalf("missing %s in %s", s, out)
		}
	}
	if strings.Contains(out, `"prompt_tokens":99`) || strings.Count(out, "[DONE]") != 1 {
		t.Fatal(out)
	}
	out = decodeFixture(t, fixtureInput(`{}`), `{"type":"finish","finishReason":"error","totalUsage":{"inputTokens":7,"outputTokens":2}}`)
	if !strings.Contains(out, `"error"`) || !strings.Contains(out, `"prompt_tokens":7`) || strings.Contains(out, `"finish_reason":"stop"`) {
		t.Fatal(out)
	}
}
func TestMiMoRestoration(t *testing.T) {
	in := fixtureInput(`{"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"n":{"type":"integer","minimum":1}},"required":["n"],"additionalProperties":false}}}]}`)
	in.Target.Model = "xiaomi/mimo-v2.6-flash"
	text := `<tool_call><function=lookup><parameter=n>3</parameter></function></tool_call>`
	events := []object{{"type": "text-start", "id": "x"}, {"type": "text-delta", "id": "x", "text": text[:8]}, {"type": "text-delta", "id": "x", "text": text[8:]}, {"type": "text-end", "id": "x"}, {"type": "finish", "finishReason": "stop"}}
	build := func(events []object) string {
		var b strings.Builder
		for _, e := range events {
			v, _ := json.Marshal(e)
			b.Write(v)
			b.WriteByte('\n')
		}
		return b.String()
	}
	out := decodeFixture(t, in, build(events))
	if !strings.Contains(out, `"name":"lookup"`) || strings.Contains(out, "tool_call&gt;") || strings.Contains(out, "<tool_call>") {
		t.Fatal(out)
	}
	events = append(events[:4], object{"type": "tool-call", "toolCallId": "native", "toolName": "lookup", "input": object{"n": 3}}, events[4])
	out = decodeFixture(t, in, build(events))
	if strings.Count(out, `"name":"lookup"`) != 1 || strings.Contains(out, "<tool_call>") {
		t.Fatal(out)
	}
}
func TestCancellationAndConcurrentDecode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rd, wr := io.Pipe()
	r, e := New().Decode(ctx, &http.Response{StatusCode: 200, Header: make(http.Header), Body: rd})
	if e != nil {
		t.Fatal(e)
	}
	cancel()
	done := make(chan struct{})
	go func() { r.Body.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("producer not joined")
	}
	wr.Close()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decodeFixture(t, fixtureInput(`{}`), `{"type":"text-delta","text":"isolated"}`)
		}()
	}
	wg.Wait()
}

func TestMiMoBlockBoundariesAndSchema(t *testing.T) {
	in := fixtureInput(`{"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"n":{"type":"integer","minimum":1}},"required":["n"],"additionalProperties":false}}}]}`)
	in.Target.Model = "xiaomi/mimo-v2.6-flash"
	envelope := `<tool_call><function=lookup><parameter=n>3</parameter></function></tool_call>`
	for _, tc := range []struct {
		name             string
		events           []object
		calls            int
		contains, absent string
	}{
		{"mismatched native arguments", []object{{"type": "text-delta", "text": envelope}, {"type": "tool-call", "toolName": "lookup", "input": object{"n": float64(9)}}, {"type": "finish", "finishReason": "stop"}}, 1, "parameter=n", ""},
		{"schema refusal stays text", []object{{"type": "text-delta", "text": strings.Replace(envelope, ">3<", ">0<", 1)}, {"type": "finish", "finishReason": "stop"}}, 0, "parameter=n", ""},
		{"post prose not executable", []object{{"type": "text-delta", "text": "Example: " + envelope}, {"type": "finish", "finishReason": "stop"}}, 0, "Example:", ""},
		{"split post prose echo", []object{{"type": "text-delta", "text": "Example: <too"}, {"type": "text-delta", "text": envelope[len("<too"):]}, {"type": "tool-call", "toolName": "lookup", "input": object{"n": float64(3)}}, {"type": "finish", "finishReason": "stop"}}, 1, "Example:", "parameter=n"},
		{"block boundary cannot create call", []object{{"type": "text-start", "id": "a"}, {"type": "text-delta", "id": "a", "text": "<tool_"}, {"type": "text-end", "id": "a"}, {"type": "text-start", "id": "b"}, {"type": "text-delta", "id": "b", "text": envelope[len("<tool_"):]}, {"type": "finish", "finishReason": "stop"}}, 0, "parameter=n", ""},
		{"interleaved reasoning", []object{{"type": "text-delta", "id": "a", "text": envelope[:20]}, {"type": "reasoning-delta", "text": "thought"}, {"type": "text-delta", "id": "a", "text": envelope[20:]}, {"type": "finish", "finishReason": "stop"}}, 1, "thought", "parameter=n"},
		{"error does not restore", []object{{"type": "text-delta", "text": envelope}, {"type": "finish", "finishReason": "error"}}, 0, "parameter=n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			for _, e := range tc.events {
				v, _ := json.Marshal(e)
				b.Write(v)
				b.WriteByte('\n')
			}
			out := decodeFixture(t, in, b.String())
			if strings.Count(out, `"name":"lookup"`) != tc.calls || !strings.Contains(out, tc.contains) || tc.absent != "" && strings.Contains(out, tc.absent) {
				t.Fatal(out)
			}
		})
	}
}
func TestFreeformAndAllowedTools(t *testing.T) {
	in := fixtureInput(`{"tools":[{"type":"custom","custom":{"name":"exec","description":"code"}},{"type":"function","function":{"name":"other","parameters":{"type":"object"}}}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","function":{"name":"exec"}}]},"messages":[]}`)
	in.Target.Model = "xiaomi/mimo-v2.6-flash"
	r, e := New().Build(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	var b object
	json.Unmarshal(r.Body, &b)
	p := obj(b["params"])
	if len(arr(p["tools"])) != 1 || obj(arr(p["tools"])[0])["name"] != "exec" || !strings.Contains(str(p["system"]), "required") {
		t.Fatalf("%s", r.Body)
	}
	out := decodeFixture(t, in, "{\"type\":\"text-delta\",\"text\":\"<tool_call><function=exec>await tools.read();</parameter></tool_call>\"}\n{\"type\":\"finish\",\"finishReason\":\"stop\"}\n")
	if !strings.Contains(out, `"name":"exec"`) || !strings.Contains(out, `await tools.read();`) {
		t.Fatal(out)
	}
}
func TestParserBounds(t *testing.T) {
	in := fixtureInput(`{}`)
	r, e := New().DecodeWithInput(context.Background(), in, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 1<<20) + "\n"))})
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	if _, e = io.ReadAll(r.Body); e == nil {
		t.Fatal("oversized line accepted")
	}
	in.Target.Model = "xiaomi/mimo-v2.6-flash"
	f := newToolFilter(in)
	text := "<tool_call>" + strings.Repeat("x", heldLimit)
	out := f.event(object{"type": "text-delta", "text": text})
	if len(out) != 1 || f.bytes > heldLimit {
		t.Fatalf("held bound exceeded %d", f.bytes)
	}
}
