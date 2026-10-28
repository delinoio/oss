// SPDX-License-Identifier: Apache-2.0
// Fixtures adapted from pinned opencodex tests/adapters/google; see UPSTREAM-LICENSE.
package google

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

func jsonObject(t *testing.T, s string) object {
	t.Helper()
	m, e := decodeJSON([]byte(s))
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func equalJSON(t *testing.T, got any, want string) {
	t.Helper()
	b, e := json.Marshal(got)
	if e != nil {
		t.Fatal(e)
	}
	var a, c any
	if json.Unmarshal(b, &a) != nil || json.Unmarshal([]byte(want), &c) != nil {
		t.Fatal("invalid test JSON")
	}
	if !reflect.DeepEqual(a, c) {
		t.Fatalf("got %s\nwant %s", b, want)
	}
}

func TestPinnedSchemaNormalization(t *testing.T) {
	cases := []struct {
		name, input, want string
		loss              bool
	}{
		{"annotations", `{"type":"object","properties":{"token":{"type":"string","encrypted":true,"x-mcp-header":"Authorization"},"list":{"type":"array","items":{"type":"string","examples":["a"]}}},"required":["token"]}`, `{"type":"object","properties":{"token":{"type":"string"},"list":{"type":"array","items":{"type":"string"}}},"required":["token"]}`, false},
		{"nullable-ref", `{"type":"object","$defs":{"a/b":{"type":"string","enum":["x","y"]}},"properties":{"choice":{"anyOf":[{"$ref":"#/$defs/a~1b"},{"type":"null"}]}}}`, `{"type":"object","properties":{"choice":{"type":"string","enum":["x","y"],"nullable":true}}}`, false},
		{"legacy-ref", `{"type":"object","definitions":{"a b":{"type":["STRING","null"],"description":"original"}},"properties":{"value":{"$ref":"#/definitions/a%20b","description":"override"}}}`, `{"type":"object","properties":{"value":{"type":"string","nullable":true,"description":"override"}}}`, false},
		{"enum-union", `{"type":"object","properties":{"status":{"description":"status","anyOf":[{"type":"string","enum":["a","b"]},{"type":"string","enum":["b","c"]}]}}}`, `{"type":"object","properties":{"status":{"description":"status","type":"string","enum":["a","b","c"]}}}`, false},
		{"mixed-union", `{"type":"object","properties":{"value":{"description":"keep","anyOf":[{"type":"string"},{"type":"number"}]}}}`, `{"type":"object","properties":{"value":{"description":"keep"}}}`, true},
		{"filtered-enum", `{"type":"object","properties":{"n":{"type":"integer","enum":[1,2]},"v":{"const":"fixed"}},"additionalProperties":false}`, `{"type":"object","properties":{"n":{"type":"integer"},"v":{"enum":["fixed"]}}}`, true},
		{"required-and-array", `{"type":"object","properties":{"values":{"type":"array"}},"required":["values","values",42,"absent"]}`, `{"type":"object","properties":{"values":{"type":"array","items":{"type":"string"}}},"required":["values"]}`, true},
		{"neutral-constraints", `{"type":"object","additionalProperties":true,"minProperties":0,"properties":{"list":{"type":"array","minItems":0,"uniqueItems":false,"prefixItems":[]},"text":{"type":"string","minLength":0}}}`, `{"type":"object","properties":{"list":{"type":"array","items":{"type":"string"}},"text":{"type":"string"}}}`, false},
		{"recursive-ref", `{"type":"object","$defs":{"node":{"type":"object","properties":{"next":{"$ref":"#/$defs/node"}}}},"properties":{"tree":{"$ref":"#/$defs/node"}}}`, `{"type":"object","properties":{"tree":{"type":"object","properties":{"next":{}}}}}`, true},
		{"external-ref", `{"type":"object","properties":{"x":{"$ref":"https://never-fetch.invalid/schema","description":"retained"}}}`, `{"type":"object","properties":{"x":{"description":"retained"}}}`, true},
		{"ref-enum-intersection", `{"type":"object","$defs":{"x":{"enum":["a",1]}},"properties":{"x":{"$ref":"#/$defs/x","enum":["a","b",1]}}}`, `{"type":"object","properties":{"x":{"enum":["a","b"]}}}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := jsonObject(t, tc.input)
			before, _ := json.Marshal(input)
			for _, cca := range []bool{false, true} {
				out, report := SanitizeToolSchema(input, cca)
				equalJSON(t, out, tc.want)
				if report.Lossy != tc.loss {
					t.Fatalf("loss report: %+v", report)
				}
			}
			after, _ := json.Marshal(input)
			if string(before) != string(after) {
				t.Fatal("mutated input")
			}
		})
	}
}

func TestSchemaBoundsAndContentFreeLoss(t *testing.T) {
	properties := object{}
	for i := 0; i < 1100; i++ {
		properties[fmt.Sprintf("field_%04d", i)] = object{"type": "array"}
	}
	out, report := SanitizeToolSchema(object{"type": "object", "properties": properties}, false)
	var walk func(object) int
	walk = func(n object) int {
		count := 1
		if n["type"] == "array" && n["items"] == nil {
			t.Fatal("bare array after budget exhaustion")
		}
		for _, v := range obj(n["properties"]) {
			count += walk(obj(v))
		}
		if child := obj(n["items"]); child != nil {
			count += walk(child)
		}
		return count
	}
	if walk(out) > 1024 || report.Categories["node-budget-widened"] != 1 {
		t.Fatalf("unbounded tree/report: %+v", report)
	}
	nested := object{"type": "string"}
	for i := 0; i < 26; i++ {
		nested = object{"type": "object", "properties": object{"nested": nested}}
	}
	_, report = SanitizeToolSchema(nested, false)
	if report.Categories["depth-limit-widened"] != 1 {
		t.Fatal(report)
	}
	defs := object{}
	for i := 0; i < 18; i++ {
		defs[fmt.Sprint(i)] = object{"$ref": "#/$defs/" + fmt.Sprint(i+1)}
	}
	defs["18"] = object{"type": "string"}
	_, report = SanitizeToolSchema(object{"type": "object", "$defs": defs, "properties": object{"secret-name": object{"$ref": "#/$defs/0", "description": "secret-description"}}}, true)
	if report.Categories["dereference-limit-widened"] != 1 {
		t.Fatal(report)
	}
	b, _ := json.Marshal(report)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "$defs") {
		t.Fatal("content leaked in report")
	}
}

func TestStrictSchemaRejectsUncertainComparison(t *testing.T) {
	values := make([]any, 1100)
	for i := range values {
		values[i] = fmt.Sprint(i)
	}
	schema := object{"type": "object", "$defs": object{"x": object{"enum": values}}, "properties": object{"x": object{"$ref": "#/$defs/x", "enum": values}}}
	body, _ := json.Marshal(object{"messages": []any{}, "tools": []any{object{"type": "function", "function": object{"name": "f", "parameters": schema}}}})
	a := NewAPI(Options{SchemaPolicy: SchemaRejectLossy})
	ctx := a.PrepareContext(context.Background())
	if _, err := a.Build(ctx, input(string(body))); errorCode(err) != "google_tool_schema_loss_refused" {
		t.Fatal(err)
	}
	r, ok := RequestSchemaReport(ctx)
	if !ok || r.UncertainComparisons == 0 || r.Lossy {
		t.Fatalf("uncertainty must not be proven loss: %+v", r)
	}
}

func prepared(t *testing.T, a *Adapter, in inference.AdapterInput) (context.Context, inference.AdapterRequest, object) {
	t.Helper()
	ctx := a.PrepareContext(context.Background())
	r, e := a.Build(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	b, e := decodeJSON(r.Body)
	if e != nil {
		t.Fatal(e)
	}
	return ctx, r, b
}
func TestAliasRequestAndResponseRoundTrip(t *testing.T) {
	name := "9 invalid.tool " + strings.Repeat("x", 80)
	body, _ := json.Marshal(object{"messages": []any{object{"role": "assistant", "tool_calls": []any{object{"id": "a", "type": "function", "function": object{"name": name, "arguments": "{}"}}}}, object{"role": "tool", "tool_call_id": "a", "content": "done"}}, "tools": []any{object{"type": "function", "function": object{"name": name, "parameters": object{"type": "object"}}}}, "tool_choice": object{"type": "function", "function": object{"name": name}}})
	a := NewAPI(Options{})
	in := input(string(body))
	ctx, _, b := prepared(t, a, in)
	wire := text(obj(array(obj(array(b["tools"])[0])["functionDeclarations"])[0])["name"])
	if wire == name || !wireToolName.MatchString(wire) {
		t.Fatal(wire)
	}
	for _, c := range array(b["contents"]) {
		for _, p := range array(obj(c)["parts"]) {
			for _, k := range []string{"functionCall", "functionResponse"} {
				if call := obj(obj(p)[k]); call != nil && call["name"] != wire {
					t.Fatal("history alias mismatch")
				}
			}
		}
	}
	equalJSON(t, obj(obj(b["toolConfig"])["functionCallingConfig"])["allowedFunctionNames"], `["`+wire+`"]`)
	for _, stream := range []bool{false, true} {
		payload := `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"` + wire + `","args":{}},"thoughtSignature":"sig-alpha-opaque-value"}]},"finishReason":"STOP"}]}`
		if stream {
			payload = "data: " + payload + "\n\n"
		}
		r, err := a.Decode(ctx, upstream(payload, stream))
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), name) || !strings.Contains(string(data), signatureA) {
			t.Fatal("lost original name/signature")
		}
	}
	if _, err := a.Build(ctx, in); errorCode(err) != "google_request_context_reused" {
		t.Fatal(err)
	}
	if _, err := a.Build(context.Background(), in); errorCode(err) != "google_request_context_required" {
		t.Fatal("alias must not be unrestorable", err)
	}
}

func TestConcurrentAliasIsolation(t *testing.T) {
	a := NewAPI(Options{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("tool.%d", i)
			b, _ := json.Marshal(object{"messages": []any{}, "tools": []any{object{"type": "function", "function": object{"name": name}}}})
			in := input(string(b))
			ctx, _, body := prepared(t, a, in)
			wire := text(obj(array(obj(array(body["tools"])[0])["functionDeclarations"])[0])["name"])
			r, e := a.Decode(ctx, upstream(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"`+wire+`","args":{}}}]},"finishReason":"STOP"}]}`, false))
			if e != nil {
				t.Error(e)
				return
			}
			data, e := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if e != nil || !strings.Contains(string(data), name) {
				t.Error("request alias contamination", e)
			}
		}(i)
	}
	wg.Wait()
}

func TestProgressLeadPinnedBoundaries(t *testing.T) {
	for input, want := range map[string]string{
		"앞선 배치에서 잘린 google.ts를 마저 읽겠습니다. Let's read google.ts next.": "앞선 배치에서 잘린 google.ts를 마저 읽겠습니다.",
		"1. Read google.ts next.\n2. Then more.":                     "1. Read google.ts next.",
		"確認します。Let's inspect next.":                                  "確認します。",
		"मैं फ़ाइल पढ़ूँगा। Then more.":                              "मैं फ़ाइल पढ़ूँगा।",
		"Reading **the docs.** Then more.":                           "Reading **the docs.**",
		"Check e.g. the 3.5 encoder first. Then more.":               "Check e.g. the 3.5 encoder first.",
		"e.g.\n \nReady. Tail.":                                      "e.g.\n \nReady.",
		"```ts\nReady. Tail.\n```":                                   "",
	} {
		if got := progressLead(input); got != want {
			t.Fatalf("%q => %q; want %q", input, got, want)
		}
	}
	text := strings.Repeat("e.g. E.G. i.e. I.E. etc. ETC. vs. VS.\u2003\n \n", 1024) + "Ready. Tail."
	if got := progressLead(text); got != strings.TrimSuffix(text, " Tail.") {
		t.Fatal("abbreviation chain truncated incorrectly")
	}
}

func TestProgressPruningPreservesFinalAndAfterCall(t *testing.T) {
	for _, model := range []string{"gemini-4-argon", "claude-sonnet-5-5"} {
		for _, phase := range []string{"commentary", "final_answer"} {
			msgs := []message{{role: "assistant", phase: phase, parts: []any{object{"text": "Reading now. Planning tail."}, object{"text": "More planning."}, object{"functionCall": object{"id": "a", "name": "f", "args": object{}}}, object{"text": "After. Keep all."}}}}
			contents, _, _, err := compileMessages(msgs, model)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(contents)
			pruned := model == "gemini-4-argon" && phase != "final_answer"
			if strings.Contains(string(data), "Planning tail.") == pruned || !strings.Contains(string(data), "After. Keep all.") {
				t.Fatal(string(data))
			}
		}
	}
}

func TestModelCapsEffortsAndDefaults(t *testing.T) {
	for _, tc := range []struct {
		model, effort, wire, level string
		cca                        bool
	}{
		{"gemini-4-argon", "ultra", "gemini-4-argon-high", "", true},
		{"gemini-3.8-flash", "", "gemini-3.8-flash-medium", "", true},
		{"gemini-3.1-pro", "low", "gemini-3.1-pro-low", "low", true},
		{"gemini-3.1-pro", "", "gemini-pro-agent", "", true},
		{"gemini-3.6-flash-high", "", "gemini-3.7-flash-tiered", "high", true},
		{"gemini-3.7-flash", "", "gemini-3.7-flash-tiered", "medium", true},
		{"claude-opus-5-5-low", "high", "claude-opus-5-5-low", "", true},
		{"claude-sonnet-4-6", "max", "claude-sonnet-4-6", "high", true},
		{"claude-custom-unknown", "high", "claude-custom-unknown", "", true},
		{"gemini-3.6-flash", "max", "gemini-3.6-flash-tiered", "high", false},
	} {
		wire, level := ResolveModel(tc.model, tc.effort, tc.cca)
		if wire != tc.wire || level != tc.level {
			t.Fatalf("%+v => %s %s", tc, wire, level)
		}
	}
	for model, cap := range map[string]int64{"gemini-3-pro": 65535, "gemini-3.7-flash": 65536, "claude-sonnet-4-6": 64000, "gpt-oss-120b": 32768, "custom-prototype": 128000} {
		in := input(`{"messages":[],"max_tokens":128000,"temperature":99,"top_p":2,"stop":["a","a","","b","c","d","e","f"]}`)
		in.Target.Model = model
		gc := obj(requestBody(t, NewAPI(Options{}), in)["generationConfig"])
		if gc["maxOutputTokens"] != json.Number(fmt.Sprint(cap)) || gc["temperature"] != json.Number("2") || gc["topP"] != json.Number("1") || len(array(gc["stopSequences"])) != 5 {
			t.Fatal(gc)
		}
	}
}

func TestNativeMessagesOrderingAndFinalFrame(t *testing.T) {
	a := NewAntigravity(Options{ShowThinkingSummaries: true})
	in := input(`{"messages":[{"role":"user","content":"hello"}]}`)
	in.Wire = inference.Anthropic
	in.Target.Auth = inference.OAuth
	in.Credential.Headers = map[string][]string{ProjectHeader: {"project"}}
	payload := `{"response":{"candidates":[{"content":{"parts":[{"text":"before"},{"functionCall":{"id":"a","name":"f","args":{}},"thoughtSignature":"sig-alpha-opaque-value"},{"text":"after"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":3,"candidatesTokenCount":2}}}`
	for _, stream := range []bool{false, true} {
		ctx, built, _ := prepared(t, a, in)
		if built.ResponseWire != inference.Anthropic {
			t.Fatal("direct Messages projection not selected")
		}
		body := payload
		if stream {
			body = "data: " + payload
		}
		r, e := a.Decode(ctx, upstream(body, stream))
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		if stream {
			if strings.Count(string(data), "event: message_stop\n") != 1 || strings.Contains(string(data), "[DONE]") {
				t.Fatal(string(data))
			}
			if strings.Index(string(data), "before") > strings.Index(string(data), "tool_use") || strings.Index(string(data), "after") < strings.Index(string(data), "tool_use") {
				t.Fatal("block order lost")
			}
		} else {
			root := jsonObject(t, string(data))
			blocks := array(root["content"])
			if len(blocks) != 3 || obj(blocks[0])["text"] != "before" || obj(blocks[1])["type"] != "tool_use" || obj(blocks[2])["text"] != "after" {
				t.Fatal(root)
			}
			equalJSON(t, root["usage"], `{"input_tokens":7,"cache_read_input_tokens":3,"output_tokens":2,"extra_content":{"google":{"total_token_count":12}}}`)
		}
	}
}

func TestMediaProjectionAndLimits(t *testing.T) {
	payload := `{"candidates":[{"content":{"parts":[{"text":"image"},{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]},"finishReason":"STOP"}]}`
	a := NewAPI(Options{NativeMedia: true})
	r := decoded(t, a, payload)
	msg := obj(obj(array(r["choices"])[0])["message"])
	media := array(obj(obj(msg["extra_content"])["google"])["media"])
	equalJSON(t, media, `[{"index":0,"mime_type":"image/png","encoding":"base64","data":"aGVsbG8="}]`)
	stream := ""
	for i := 0; i < 17; i++ {
		stream += "data: " + payload + "\n\n"
	}
	response, e := a.Decode(context.Background(), upstream(stream, true))
	if e != nil {
		t.Fatal(e)
	}
	data, e := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if errorCode(e) != "google_media_limit" || strings.Contains(string(data), "[DONE]") {
		t.Fatal("media budget did not fail terminally", e)
	}
	for _, bad := range []string{strings.Replace(payload, "aGVsbG8=", "broken!", 1), strings.Replace(payload, "image/png", "application/javascript", 1)} {
		if _, e := a.Decode(context.Background(), upstream(bad, false)); e == nil {
			t.Fatal("accepted malformed media")
		}
	}
	in := input(`{"messages":[]}`)
	in.Target.Model = "gemini-3.1-flash-image"
	body := requestBody(t, a, in)
	equalJSON(t, obj(body["generationConfig"])["responseModalities"], `["TEXT","IMAGE"]`)
}

func TestPinnedDefaultReasoningAndBudgetMapping(t *testing.T) {
	for _, tc := range []struct{ model, body, wire, level string }{
		{"gemini-3.1-pro", `{"messages":[],"reasoning_effort":"medium"}`, "gemini-3.1-pro-low", "low"},
		{"gemini-4-argon", `{"messages":[],"reasoning_effort":"minimal"}`, "gemini-4-argon-low", ""},
		{"gemini-3.7-flash", `{"messages":[],"thinking":{"type":"enabled","budget_tokens":32768}}`, "gemini-3.7-flash-tiered", "high"},
		{"claude-sonnet-4-6", `{"messages":[],"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`, "claude-sonnet-4-6", "high"},
	} {
		a := NewAntigravity(Options{})
		in := input(tc.body)
		in.Target.Model = tc.model
		in.Target.Auth = inference.OAuth
		in.Credential.Headers = map[string][]string{ProjectHeader: {"project"}}
		_, _, body := prepared(t, a, in)
		if body["model"] != tc.wire {
			t.Fatal(body)
		}
		thinking := obj(obj(obj(body["request"])["generationConfig"])["thinkingConfig"])
		if text(thinking["thinkingLevel"]) != tc.level || thinking["thinkingBudget"] != nil {
			t.Fatal(thinking)
		}
		if strings.HasPrefix(tc.model, "gemini-") && thinking["includeThoughts"] != true {
			t.Fatal("CCA summary default missing")
		}
	}
	api := NewAPI(Options{})
	in := input(`{"messages":[],"reasoning_effort":"minimal"}`)
	in.Target.Model = "gemini-3.5-flash"
	if obj(obj(requestBody(t, api, in)["generationConfig"])["thinkingConfig"])["thinkingLevel"] != "low" {
		t.Fatal("minimal was not mapped through the Codex effort ladder")
	}
}

func TestAllowedToolSubsetRetainsOnlySelectedStrictness(t *testing.T) {
	for _, selected := range []string{"strict", "loose"} {
		in := input(`{"messages":[],"tools":[{"type":"function","function":{"name":"strict","strict":true}},{"type":"function","function":{"name":"loose"}}],"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"function","function":{"name":"` + selected + `"}}]}}}`)
		body := requestBody(t, NewAPI(Options{}), in)
		decls := array(obj(array(body["tools"])[0])["functionDeclarations"])
		if len(decls) != 1 || obj(decls[0])["name"] != selected {
			t.Fatal(body)
		}
		if (body["toolConfig"] != nil) != (selected == "strict") {
			t.Fatal("excluded tool affected validation mode")
		}
	}
}

func TestNativeMessagesOpaqueReplay(t *testing.T) {
	for _, hide := range []bool{false, true} {
		a := NewAntigravity(Options{HideThinkingSummaries: hide})
		in := input(`{"messages":[]}`)
		in.Wire = inference.Anthropic
		in.Target.Auth = inference.OAuth
		in.Credential.Headers = map[string][]string{ProjectHeader: {"project"}}
		ctx, _, _ := prepared(t, a, in)
		r, e := a.Decode(ctx, upstream(`{"response":{"candidates":[{"content":{"parts":[{"thought":true,"text":"summary","thoughtSignature":"sig-beta-opaque-value"},{"functionCall":{"id":"a","name":"f","args":{}},"thoughtSignature":"sig-alpha-opaque-value"}]},"finishReason":"STOP"}]}}`, false))
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		message := jsonObject(t, string(data))
		body, _ := json.Marshal(object{"messages": []any{message, object{"role": "user", "content": []any{object{"type": "tool_result", "tool_use_id": "a", "content": "done"}}}}})
		in.Body = body
		_, _, rebuilt := prepared(t, a, in)
		parts := array(obj(array(obj(rebuilt["request"])["contents"])[1])["parts"])
		if len(parts) != 2 || signature(obj(parts[0])) != signatureB || signature(obj(parts[1])) != signatureA {
			t.Fatal("native replay lost or duplicated signed thinking")
		}
	}
}

func TestSchemaObserverCanReadRequestReport(t *testing.T) {
	observed := false
	a := NewAPI(Options{SchemaObserver: func(ctx context.Context, r SchemaLossReport) {
		stored, ok := RequestSchemaReport(ctx)
		if !ok || !stored.Lossy || !r.Lossy {
			t.Error("missing report")
		}
		r.Categories["mutated"] = 1
		observed = true
	}})
	ctx, _, _ := prepared(t, a, input(`{"messages":[],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","additionalProperties":false}}}]}`))
	r, _ := RequestSchemaReport(ctx)
	if !observed || r.Categories["mutated"] != 0 {
		t.Fatal("observer alias or missing call")
	}
}

func TestNativeThinkingSignatureAcrossFrames(t *testing.T) {
	a := NewAntigravity(Options{})
	in := input(`{"messages":[]}`)
	in.Wire = inference.Anthropic
	in.Target.Auth = inference.OAuth
	in.Credential.Headers = map[string][]string{ProjectHeader: {"project"}}
	for _, parts := range [][]string{
		{`[{"thought":true,"text":"first "}]`, `[{"thought":true,"text":"second"}]`, `[{"thought":true,"thoughtSignature":"sig-alpha-opaque-value"}]`},
		{`[{"thought":true,"text":"first second"},{"thought":true,"thoughtSignature":"sig-alpha-opaque-value"}]`},
	} {
		ctx, _, _ := prepared(t, a, in)
		stream := ""
		for _, p := range parts {
			stream += `data: {"response":{"candidates":[{"content":{"parts":` + p + `}}]}}` + "\n\n"
		}
		stream += `data: {"response":{"candidates":[{"finishReason":"STOP"}]}}`
		r, e := a.Decode(ctx, upstream(stream, true))
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		s := string(data)
		if strings.Count(s, "event: content_block_start\n") != 1 || strings.Count(s, "event: content_block_stop\n") != 1 || strings.Count(s, `"type":"signature_delta"`) != 1 {
			t.Fatal(s)
		}
		if strings.Index(s, `"type":"signature_delta"`) > strings.Index(s, "event: content_block_stop\n") {
			t.Fatal("signature emitted after block closed")
		}
	}
}
