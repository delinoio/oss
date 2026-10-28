// SPDX-License-Identifier: Apache-2.0
package policy

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/providers"
)

func preset(t *testing.T, id, model string, wire providers.Wire, overrides string) Resolved {
	t.Helper()
	p, ok := providers.Lookup(id)
	if !ok {
		t.Fatal(id)
	}
	if wire == "" {
		wire = p.Wire
	}
	c := Config{ProviderID: id, Model: model, BaseURL: p.BaseURL, Wire: wire, Inbound: providers.InboundChat, Registry: p.Metadata, Overrides: json.RawMessage(overrides)}
	r, e := Resolve(c)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func body(t *testing.T, s string) map[string]any {
	t.Helper()
	b, e := decode([]byte(s))
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func apply(t *testing.T, p Resolved, s string) map[string]any {
	t.Helper()
	in := body(t, s)
	before, _ := json.Marshal(in)
	out, e := p.TransformRequest(in)
	if e != nil {
		t.Fatal(e)
	}
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Fatal("mutated input")
	}
	return out
}
func TestDestinationWireResolution(t *testing.T) {
	for _, id := range []string{"commandcode", "tokenlab"} {
		p := preset(t, id, "claude-future", "", `{"modelAdapters":{"claude-future":"openai-responses"}}`)
		if p.Wire != providers.Anthropic {
			t.Fatal(p)
		}
		raw, _ := providers.Lookup(id)
		q, e := Resolve(Config{ProviderID: id, Model: "claude-future", BaseURL: "https://gateway.example/v1", Wire: providers.Chat, Registry: raw.Metadata})
		if e != nil || q.Wire != providers.Chat || q.TransportMatchedRegistry {
			t.Fatal(q, e)
		}
	}
	p := preset(t, "zai", "glm-5.3", "", "")
	if p.Endpoint != "https://api.z.ai/api/v1/responses" {
		t.Fatal(p.Endpoint)
	}
	raw, _ := providers.Lookup("tokenlab")
	for _, tc := range []struct {
		in   providers.Inbound
		want providers.Wire
	}{{providers.InboundResponses, providers.Responses}, {providers.InboundChat, providers.Chat}} {
		p, e := Resolve(Config{ProviderID: raw.ID, Model: "gpt-6-astra", BaseURL: raw.BaseURL, Wire: raw.Wire, Inbound: tc.in, Registry: raw.Metadata})
		if e != nil || p.Wire != tc.want {
			t.Fatal(p, e)
		}
	}
	for _, id := range []string{"dsh-account", "workbuddy-ai"} {
		p := preset(t, id, "any", "", `{"modelAdapters":{"any":"openai-chat"}}`)
		if string(p.Wire) != id {
			t.Fatal(p)
		}
	}
}
func TestVolcengineTriState(t *testing.T) {
	s := `{"input":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"kept"}],"summary":[]},{"type":"message","role":"user","content":"hi"}]}`
	for _, tc := range []struct {
		override string
		want     int
	}{{"", 1}, {`{"dropResponsesReasoningItems":false,"preserveResponsesReasoningContent":true}`, 2}, {`{"dropResponsesReasoningItems":true}`, 1}} {
		out := apply(t, preset(t, "volcengine-coding-plan", "deepseek-v4-flash", "", tc.override), s)
		if len(list(out["input"])) != tc.want {
			t.Fatal(out)
		}
	}
	p := preset(t, "volcengine-coding-plan", "deepseek-v4-flash", "", `{"dropResponsesReasoningItems":false,"preserveResponsesReasoningContent":false}`)
	out := apply(t, p, s)
	if len(list(obj(list(out["input"])[0])["content"])) != 0 {
		t.Fatal(out)
	}
}
func TestMoonshotFoldAndSchema(t *testing.T) {
	input := `{"messages":[{"role":"developer","content":"rules"}],"tools":[{"type":"function","function":{"name":"x","parameters":{"$defs":{"base":{"type":"object","required":["a"],"properties":{"a":{"type":"string"}},"minProperties":2}},"$ref":"#/$defs/base","required":["b"],"properties":{"b":{"type":"number"}},"minProperties":1}}}]}`
	p := preset(t, "moonshot", "kimi-k3", "", "")
	out := apply(t, p, input)
	if obj(list(out["messages"])[0])["role"] != "system" {
		t.Fatal(out)
	}
	schema := obj(obj(obj(list(out["tools"])[0])["function"])["parameters"])
	if schema["$ref"] != nil || len(list(schema["required"])) != 2 || len(obj(schema["properties"])) != 2 || str(schema["type"]) != "object" {
		t.Fatal(schema)
	}
	if n, _ := number(schema["minProperties"]); n != 2 {
		t.Fatal(schema)
	}
	p = preset(t, "moonshot", "kimi-k3", "", `{"foldDeveloperRoleToSystem":false}`)
	out = apply(t, p, input)
	if obj(list(out["messages"])[0])["role"] != "developer" {
		t.Fatal(out)
	}
}
func TestThinkingAndReplay(t *testing.T) {
	for _, tc := range []struct{ id, model string }{{"deepseek", "deepseek-v4-flash"}, {"cline-pass", "cline-pass/deepseek-v4-flash"}} {
		p := preset(t, tc.id, tc.model, providers.Chat, "")
		p.fields["preserveReasoningContentModels"] = nil
		out := apply(t, p, `{"messages":[{"role":"assistant","content":"narration","tool_calls":[{"id":"c"}]}]}`)
		m := obj(list(out["messages"])[0])
		if m["reasoning_content"] != " " {
			t.Fatal(out)
		}
		if tc.id == "cline-pass" && m["content"] != "" {
			t.Fatal(out)
		}
	}
	p := preset(t, "minimax", "MiniMax-M3", "", "")
	out := apply(t, p, `{"reasoning_effort":"high","messages":[{"role":"assistant","reasoning_details":[{"type":"reasoning.text","text":"keep"}],"tool_calls":[{"id":"c"}]}]}`)
	if out["reasoning_split"] != true || obj(out["thinking"])["type"] != "adaptive" || out["reasoning_effort"] != nil {
		t.Fatal(out)
	}
	if obj(list(out["messages"])[0])["reasoning_content"] != nil {
		t.Fatal(out)
	}
	p = preset(t, "xiaomi", "mimo-v2.6-pro", "", "")
	out, e := p.TransformChatRequest(body(t, `{"reasoning":{"enabled":false},"reasoning_effort":"high"}`))
	if e != nil || obj(out["thinking"])["type"] != "disabled" || out["reasoning_effort"] != nil {
		t.Fatal(out, e)
	}
}
func TestAdjacencyPreservesAmbiguity(t *testing.T) {
	p, e := Resolve(Config{BaseURL: "https://example.test/v1", Wire: providers.Responses, Overrides: json.RawMessage(`{"requiresAdjacentResponsesToolResults":true}`)})
	if e != nil {
		t.Fatal(e)
	}
	out := apply(t, p, `{"input":[{"type":"function_call","call_id":"a"},{"type":"function_call","call_id":"b"},{"type":"message","content":"later"},{"type":"function_call_output","call_id":"a","output":"a"},{"type":"function_call_output","call_id":"b","output":"b"}]}`)
	a := list(out["input"])
	if obj(a[2])["call_id"] != "a" || obj(a[3])["call_id"] != "b" || obj(a[4])["type"] != "message" {
		t.Fatal(a)
	}
	for _, s := range []string{`{"input":[{"type":"function_call","call_id":"a"},{"type":"message"}]}`, `{"input":[{"type":"function_call_output","call_id":"a"},{"type":"function_call","call_id":"a"}]}`, `{"input":[{"type":"function_call","call_id":"a"},{"type":"function_call","call_id":"a"},{"type":"function_call_output","call_id":"a"}]}`} {
		in := body(t, s)
		out, e := p.TransformRequest(in)
		if e != nil || !reflect.DeepEqual(in, out) {
			t.Fatal(in, out, e)
		}
	}
}
func TestRawPolicySamplingAndOptOuts(t *testing.T) {
	p, e := Resolve(Config{BaseURL: "https://example.test/v1", Model: "x", Wire: providers.Chat, Inbound: providers.InboundChat, Overrides: json.RawMessage(`{"noTemperatureModels":["x"],"noTopPModels":["x"],"noStopModels":["x"],"noPenaltyModels":["x"],"noJsonSchemaModels":["x"],"supportsServiceTier":false,"supportsVerbosity":false,"parallelToolCalls":false}`)})
	if e != nil {
		t.Fatal(e)
	}
	out := apply(t, p, `{"temperature":1,"top_p":1,"stop":"x","presence_penalty":1,"frequency_penalty":1,"service_tier":"priority","verbosity":"high","response_format":{"type":"json_schema"},"tools":[{}],"parallel_tool_calls":true}`)
	for _, k := range []string{"temperature", "top_p", "stop", "presence_penalty", "frequency_penalty", "service_tier", "verbosity", "parallel_tool_calls"} {
		if out[k] != nil {
			t.Fatal(k, out)
		}
	}
	if obj(out["response_format"])["type"] != "json_object" {
		t.Fatal(out)
	}
	p.Model = "x:tag"
	out = apply(t, p, `{"response_format":{"type":"json_schema"},"temperature":1}`)
	if obj(out["response_format"])["type"] != "json_schema" || out["temperature"] != nil {
		t.Fatal(out)
	}
}
func TestConcurrentDetachedPolicy(t *testing.T) {
	p := preset(t, "volcengine-coding-plan", "x", "", "")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				b := map[string]any{"input": []any{map[string]any{"type": "reasoning"}}}
				if _, e := p.TransformRequest(b); e != nil {
					t.Error(e)
				}
				meta := p.Metadata()
				if len(meta) > 0 {
					meta[0] = '!'
				}
			}
		}()
	}
	wg.Wait()
}
func TestBareRepairBufferedAndSSE(t *testing.T) {
	text := "before\n<tool_call>\n<function=patch>\n*** Begin Patch\n*** End Patch\n</function>\n</tool_call>\nafter"
	tools := map[string]string{"patch": "patch"}
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": text}, "finish_reason": "stop"}}})
	out, e := RepairChatResponse(raw, "mimo-v2.6-pro", tools)
	if e != nil {
		t.Fatal(e)
	}
	b := body(t, string(out))
	m := obj(obj(list(b["choices"])[0])["message"])
	if len(list(m["tool_calls"])) != 1 || str(m["content"]) != "before\n\nafter" {
		t.Fatal(string(out))
	}
	var wire strings.Builder
	for _, part := range []string{text[:23], text[23:]} {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": part}, "finish_reason": nil}}})
		wire.WriteString("data: " + string(b) + "\n\n")
	}
	wire.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	resp := WrapChatResponse(&http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire.String()))}, "mimo-v2.6-pro", tools)
	data, e := io.ReadAll(resp.Body)
	if e != nil {
		t.Fatal(e)
	}
	_ = resp.Body.Close()
	if !strings.Contains(string(data), `"tool_calls"`) || !strings.Contains(string(data), "[DONE]") {
		t.Fatal(string(data))
	}
	for _, unsafe := range []string{"```xml\n" + text + "\n```", "<tool_call><function=other>x</function></tool_call>", "<tool_call><function=patch><tool_call><function=patch>x</function></tool_call></function></tool_call>", "<tool_call_abc><function=patch>x</function></tool_call_abc>"} {
		if len(recoverBare(unsafe, tools)) != 0 {
			t.Fatal(unsafe)
		}
	}
}
func TestFreeformDeclarations(t *testing.T) {
	m := FreeformTools(json.RawMessage(`{"tools":[{"type":"namespace","name":"edit","tools":[{"type":"custom","name":"patch"}]},{"type":"function","function":{"name":"not_free","parameters":{"properties":{"input":{"type":"string"}}}}}]}`))
	if m["edit__patch"] != "patch" || len(m) != 1 {
		t.Fatal(m)
	}
}

func TestItemIDRepairAndToolIdentity(t *testing.T) {
	p := preset(t, "deepseek", "deepseek-v4-flash", "", "")
	p.Wire = providers.Responses
	rewrite := p.NewResponseRewriter()
	add, e := rewrite(body(t, `{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"uuid"}}`))
	if e != nil {
		t.Fatal(e)
	}
	id := str(obj(add["item"])["id"])
	if !strings.HasPrefix(id, "rs_ocx_") {
		t.Fatal(add)
	}
	delta, e := rewrite(body(t, `{"type":"response.content_part.added","output_index":0,"item_id":"uuid"}`))
	if e != nil || delta["item_id"] != id {
		t.Fatal(delta, e)
	}
	done, e := rewrite(body(t, `{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning"}}`))
	if e != nil || obj(done["item"])["id"] != id {
		t.Fatal(done, e)
	}
	tool, e := rewrite(body(t, `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"uuid","call_id":"original"}}`))
	if e != nil || obj(tool["item"])["id"] != "uuid" || obj(tool["item"])["call_id"] != "original" {
		t.Fatal(tool, e)
	}
}
func TestAnthropicEscapeAndRestore(t *testing.T) {
	p := preset(t, "umans", "claude-test", "", "")
	out := apply(t, p, `{"tools":[{"name":"Read","input_schema":{"type":"object"}}],"tool_choice":{"type":"tool","name":"Read"},"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"Read","id":"a","input":{}}]}]}`)
	if obj(list(out["tools"])[0])["name"] != "cx_Read" {
		t.Fatal(out)
	}
	r, e := p.NewResponseRewriter()(body(t, `{"content":[{"type":"tool_use","name":"cx_Read","id":"a","input":{}}]}`))
	if e != nil || obj(list(r["content"])[0])["name"] != "Read" {
		t.Fatal(r, e)
	}
}
func TestWebSearchFieldsAndPairing(t *testing.T) {
	p := preset(t, "xai", "grok-4.7", "", "")
	p.Wire = providers.Responses
	out := apply(t, p, `{"tools":[{"type":"web_search","external_web_access":true,"search_context_size":"high","user_location":{"country":"US"}}],"input":[{"type":"function_call","name":"inspect","call_id":"a"}]}`)
	tool := obj(list(out["tools"])[0])
	if tool["external_web_access"] != nil || tool["search_context_size"] != nil || tool["user_location"] == nil {
		t.Fatal(tool)
	}
	if len(list(out["input"])) != 2 || !strings.Contains(str(obj(list(out["input"])[1])["output"]), "execution status unknown") {
		t.Fatal(out)
	}
}
func TestRegistryChoicesAndAuthDefault(t *testing.T) {
	p, _ := providers.Lookup("moonshot")
	r, e := Resolve(Config{ProviderID: p.ID, Model: "kimi-k3", BaseURL: "https://api.moonshot.cn/v1", Wire: p.Wire, Registry: p.Metadata})
	if e != nil || !r.TransportMatchedRegistry {
		t.Fatal(r, e)
	}
	out := apply(t, r, `{"messages":[{"role":"developer","content":"rule"}]}`)
	if obj(list(out["messages"])[0])["role"] != "system" {
		t.Fatal(out)
	}
}
func TestEmptyOutputsPreserveMedia(t *testing.T) {
	p, e := Resolve(Config{BaseURL: "https://example.test", Wire: providers.Responses, Overrides: json.RawMessage(`{"annotateEmptyToolOutputs":true}`)})
	if e != nil {
		t.Fatal(e)
	}
	out := apply(t, p, `{"input":[{"type":"function_call_output","call_id":"a","output":" "},{"type":"function_call_output","call_id":"b","output":[{"type":"input_image","image_url":"https://example.test/a"}]}]}`)
	a := list(out["input"])
	if obj(a[0])["output"] != emptyOutput || len(list(obj(a[1])["output"])) != 1 {
		t.Fatal(a)
	}
}

// Exercise every shipped entry of the implemented classification policies against
// outbound bodies. This asserts transformations, not registry roster presence.
func TestShippedClassificationPolicies(t *testing.T) {
	sampling := map[string][]string{"noTemperatureModels": {"temperature"}, "noTopPModels": {"top_p"}, "noStopModels": {"stop"}, "noPenaltyModels": {"presence_penalty", "frequency_penalty"}}
	for _, entry := range providers.Catalog() {
		if entry.Excluded {
			continue
		}
		fields, e := decode(entry.Metadata)
		if e != nil {
			t.Fatal(e)
		}
		for key, params := range sampling {
			for _, model := range stringList(fields[key]) {
				t.Run(entry.ID+"/"+key+"/"+model, func(t *testing.T) {
					p := preset(t, entry.ID, model, "", "")
					p.Wire = providers.Chat
					b := map[string]any{}
					for _, param := range params {
						b[param] = 1
					}
					out, e := p.TransformRequest(b)
					if e != nil {
						t.Fatal(e)
					}
					for _, param := range params {
						if _, ok := out[param]; ok {
							t.Fatal(param, out)
						}
					}
				})
			}
		}
		for _, model := range stringList(fields["reasoningSplitModels"]) {
			t.Run(entry.ID+"/split/"+model, func(t *testing.T) {
				p := preset(t, entry.ID, model, "", "")
				p.Wire = providers.Chat
				out := apply(t, p, `{"messages":[]}`)
				if out["reasoning_split"] != true {
					t.Fatal(out)
				}
			})
		}
		for _, model := range stringList(fields["noReasoningModels"]) {
			t.Run(entry.ID+"/no-reasoning/"+model, func(t *testing.T) {
				p := preset(t, entry.ID, model, "", "")
				p.Wire = providers.Chat
				out := apply(t, p, `{"reasoning":{"effort":"high"},"thinking":{"type":"enabled"},"reasoning_effort":"high"}`)
				for _, key := range []string{"reasoning", "thinking", "reasoning_effort"} {
					if out[key] != nil {
						t.Fatal(out)
					}
				}
			})
		}
	}
}
func TestItemIDConfigReplacement(t *testing.T) {
	p := preset(t, "deepseek", "deepseek-v4-flash", "", `{"responsesItemIdRepair":{}}`)
	p.Wire = providers.Responses
	r, e := p.NewResponseRewriter()(body(t, `{"output":[{"type":"message","id":"bad"}]}`))
	if e != nil || obj(list(r["output"])[0])["id"] != "bad" {
		t.Fatal(r, e)
	}
	raw, _ := providers.Lookup("deepseek")
	_, e = Resolve(Config{BaseURL: raw.BaseURL, Wire: raw.Wire, Registry: raw.Metadata, Overrides: json.RawMessage(`{"responsesItemIdRepair":false}`)})
	if e == nil {
		t.Fatal("accepted obsolete bool")
	}
}
func TestModelPolicyLimitsAndAxes(t *testing.T) {
	p, e := Resolve(Config{BaseURL: "https://example.test", Model: "x", Overrides: json.RawMessage(`{"contextWindow":100,"modelMaxInputTokens":{"x":200},"defaultMaxOutputTokens":30,"modelCapabilities":{"x":{"supportsThinkingToggle":true,"supportsReasoningEffort":false,"defaultThinkingEnabled":true}},"modelReasoningEfforts":{"x":["high"]}}`)})
	if e != nil {
		t.Fatal(e)
	}
	m := p.ModelPolicy()
	if m.MaxInputTokens == nil || *m.MaxInputTokens != 100 || m.MaxOutputTokens == nil || *m.MaxOutputTokens != 30 || len(m.ReasoningEfforts) != 0 || m.SupportsReasoning == nil || !*m.SupportsReasoning {
		t.Fatal(m)
	}
}

func TestStrictResponsesPlaintextBackfill(t *testing.T) {
	p := preset(t, "deepseek", "deepseek-v4-flash", "", "")
	p.Wire = providers.Responses
	out := apply(t, p, `{"input":[{"type":"reasoning","encrypted_content":"foreign","summary":[{"type":"summary_text","text":"known summary"}]},{"type":"function_call","call_id":"a","name":"x"},{"type":"function_call_output","call_id":"a","output":"ok"},{"type":"function_call","call_id":"b","name":"x"},{"type":"function_call_output","call_id":"b","output":"ok"}]}`)
	a := list(out["input"])
	r := obj(a[0])
	if r["encrypted_content"] != nil || obj(list(r["content"])[0])["text"] != "known summary" || obj(a[3])["type"] != "reasoning" {
		t.Fatal(out)
	}
}
func TestMiMoNoneDisablesThinking(t *testing.T) {
	p := preset(t, "xiaomi", "mimo-v2.6-pro", "", "")
	b, e := p.TransformChatRequest(body(t, `{"reasoning_effort":"none"}`))
	if e != nil || obj(b["thinking"])["type"] != "disabled" {
		t.Fatal(b, e)
	}
}

func TestDeepSeekArtifactSchemaScope(t *testing.T) {
	p := preset(t, "deepseek", "deepseek-v4-flash", "", "")
	p.Wire = providers.Chat
	out := apply(t, p, `{"tools":[{"type":"function","function":{"name":"Artifact","strict":true,"parameters":{"type":"object","properties":{"pattern":{"type":"string","pattern":"bad","default":{"pattern":"keep"}}},"anyOf":[{"type":"string"}]}}},{"type":"function","function":{"name":"Other","parameters":{"pattern":"keep"}}}]}`)
	tools := list(out["tools"])
	f := obj(obj(tools[0])["function"])
	s := obj(f["parameters"])
	prop := obj(obj(s["properties"])["pattern"])
	if f["strict"] != nil || s["anyOf"] != nil || prop["pattern"] != nil || obj(prop["default"])["pattern"] != "keep" || obj(obj(obj(tools[1])["function"])["parameters"])["pattern"] != "keep" {
		t.Fatal(out)
	}
}

func TestCustomToolsLowerAndRestore(t *testing.T) {
	p := preset(t, "xai", "grok-4.7", "", "")
	p.Wire = providers.Responses
	raw := `{"tools":[{"type":"namespace","name":"edit","tools":[{"type":"custom","name":"patch","format":{"type":"text"}}]}],"tool_choice":{"type":"custom","namespace":"edit","name":"patch"},"input":[{"type":"custom_tool_call","namespace":"edit","name":"patch","call_id":"c","input":"line\nraw"},{"type":"custom_tool_call_output","call_id":"c","output":"ok"}]}`
	out := apply(t, p, raw)
	tool := obj(list(out["tools"])[0])
	if tool["type"] != "function" || tool["name"] != "edit__patch" || obj(out["tool_choice"])["type"] != "function" {
		t.Fatal(out)
	}
	a := list(out["input"])
	if obj(a[0])["type"] != "function_call" || obj(a[1])["type"] != "function_call_output" {
		t.Fatal(out)
	}
	args := body(t, str(obj(a[0])["arguments"]))
	if args["input"] != "line\nraw" {
		t.Fatal(args)
	}
	rewrite := p.NewResponseRewriterForRequest(json.RawMessage(raw))
	b, e := rewrite(body(t, `{"output":[{"type":"function_call","id":"fc_1","call_id":"c","name":"edit__patch","arguments":"{\"input\":\"line\\nraw\"}"}]}`))
	if e != nil {
		t.Fatal(e)
	}
	call := obj(list(b["output"])[0])
	if call["type"] != "custom_tool_call" || call["id"] != "ctc_1" || call["input"] != "line\nraw" || call["namespace"] != "edit" || call["name"] != "patch" {
		t.Fatal(b)
	}
	add, e := rewrite(body(t, `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"c","name":"edit__patch","arguments":""}}`))
	if e != nil || obj(add["item"])["type"] != "custom_tool_call" {
		t.Fatal(add, e)
	}
	delta, e := rewrite(body(t, `{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{\"input\":\"hi\"}"}`))
	if e != nil || delta["delta"] != "" || delta["type"] != "response.custom_tool_call_input.delta" {
		t.Fatal(delta, e)
	}
	done, e := rewrite(body(t, `{"type":"response.function_call_arguments.done","output_index":0,"item_id":"fc_1"}`))
	if e != nil || done["input"] != "hi" || done["type"] != "response.custom_tool_call_input.done" {
		t.Fatal(done, e)
	}
	_, e = rewrite(body(t, `{"output":[{"type":"function_call","name":"edit__patch","arguments":"bad"}]}`))
	if e == nil {
		t.Fatal("malformed custom arguments accepted")
	}
}
func TestCustomToolCollisionFails(t *testing.T) {
	p := preset(t, "xai", "grok-4.7", "", "")
	p.Wire = providers.Responses
	_, e := p.TransformRequest(body(t, `{"tools":[{"type":"custom","name":"edit__patch"},{"type":"namespace","name":"edit","tools":[{"type":"function","name":"patch"}]}]}`))
	if e == nil {
		t.Fatal("ambiguous custom name accepted")
	}
}

func TestTranslatedThinkingBudgetAndEmptyLadder(t *testing.T) {
	p := preset(t, "neuralwatt", "qwen3.5-plus", "", "")
	p.Wire = providers.Chat
	p.Inbound = providers.InboundResponses
	out := apply(t, p, `{"reasoning_effort":"high","max_completion_tokens":1000}`)
	if n, _ := number(out["thinking_budget"]); n != 750 || out["reasoning_effort"] != nil {
		t.Fatal(out)
	}
	p = preset(t, "chutes", "any", "", "")
	p.Wire = providers.Chat
	out = apply(t, p, `{"reasoning_effort":"high"}`)
	if out["reasoning_effort"] != nil {
		t.Fatal(out)
	}
}

func TestTransportPolicyProjection(t *testing.T) {
	p := preset(t, "deepseek", "deepseek-v4-flash", "", "")
	if policy := p.ResponsesTerminalRepair(); policy == nil || policy.GraceMS != 5000 {
		t.Fatal(policy)
	}
	goPolicy := preset(t, "opencode-go", "gpt-6-luna", "", "")
	if v := goPolicy.ChatEOFTolerance(); v == nil || !*v {
		t.Fatal(v)
	}
	unknown, e := Resolve(Config{BaseURL: "https://example.test"})
	if e != nil || unknown.ResponsesTerminalRepair() != nil || unknown.ChatEOFTolerance() != nil {
		t.Fatal(unknown, e)
	}
}

func TestAnthropicMessagesEndpointNormalization(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://api.anthropic.com", "https://api.anthropic.com/v1/messages"},
		{"https://api.anthropic.com///", "https://api.anthropic.com/v1/messages"},
		{"https://api.anthropic.com/v1", "https://api.anthropic.com/v1/messages"},
		{"https://api.anthropic.com/v1/messages/", "https://api.anthropic.com/v1/messages"},
		{"https://proxy.example/Prefix/V1/MESSAGES///", "https://proxy.example/Prefix/v1/messages"},
		{"https://proxy.example/anthropic", "https://proxy.example/anthropic/v1/messages"},
		{"https://api.commandcode.ai/provider/v1", "https://api.commandcode.ai/provider/v1/messages"},
		{"https://api.tokenlab.sh/v1", "https://api.tokenlab.sh/v1/messages"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			p, e := Resolve(Config{BaseURL: tc.base, Wire: providers.Anthropic})
			if e != nil || p.Endpoint != tc.want || p.BaseURL != tc.base {
				t.Fatalf("endpoint=%q base=%q error=%v; want %q", p.Endpoint, p.BaseURL, e, tc.want)
			}
		})
	}
	for id, want := range map[string]string{"anthropic": "https://api.anthropic.com/v1/messages", "umans": "https://api.code.umans.ai/v1/messages", "xiaomi": "https://api.xiaomimimo.com/anthropic/v1/messages"} {
		t.Run(id, func(t *testing.T) {
			p := preset(t, id, "model", "", "")
			if p.Endpoint != want {
				t.Fatalf("endpoint=%q want %q", p.Endpoint, want)
			}
		})
	}
}
