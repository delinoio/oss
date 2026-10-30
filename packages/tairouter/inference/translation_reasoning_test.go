// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func translationObject(t *testing.T, s string) object {
	t.Helper()
	m, e := decode([]byte(s))
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func translationJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func TestTranslationReasoningRoundTrips(t *testing.T) {
	fixtures := []struct {
		name    string
		msg     object
		needles []string
	}{
		{"devin", object{"role": "assistant", "reasoning_content": "private thought", "reasoning_signature": "devin-sig1:anthropic:opaque"}, []string{"private thought", "devin-sig1:anthropic:opaque"}},
		{"kiro", object{"role": "assistant", "reasoning_content": "summary", "kiro_reasoning": "signature:.KTR~~opaque"}, []string{"summary", "signature:.KTR~~opaque"}},
		{"redacted", translationObject(t, `{"role":"assistant","reasoning_details":[{"type":"reasoning.encrypted","data":"redacted-one","format":"anthropic-claude-v1"},{"type":"reasoning.text","text":"exact signed text","signature":"abcdefghijklmnop==","format":"anthropic-claude-v1"}]}`), []string{"redacted-one", "exact signed text", "abcdefghijklmnop=="}},
		{"google", translationObject(t, `{"role":"assistant","extra_content":{"google":{"parts":[{"thought":true,"text":"hidden","thoughtSignature":"opaque-google"}]}}}`), []string{"hidden", "opaque-google"}},
	}
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			body := object{"model": "m", "messages": []any{f.msg}}
			responses, e := translateRequest(body, Chat, Responses)
			if e != nil {
				t.Fatal(e)
			}
			anthropic, e := translateRequest(responses, Responses, Anthropic)
			if e != nil {
				t.Fatal(e)
			}
			back, e := translateRequest(anthropic, Anthropic, Chat)
			if e != nil {
				t.Fatal(e)
			}
			// Decode the final canonical envelope before checking opaque values.
			items, e := messagesToResponses(arr(back["messages"]))
			if e != nil {
				t.Fatal(e)
			}
			decoded := translationJSON(back)
			for _, v := range items {
				if enc := str(obj(v)["encrypted_content"]); strings.HasPrefix(enc, reasoningPrefix) {
					env, e := decodeReasoning(enc)
					if e != nil {
						t.Fatal(e)
					}
					decoded += translationJSON(env)
				}
			}
			for _, want := range f.needles {
				if !strings.Contains(decoded, want) {
					t.Fatalf("lost %q: %s", want, decoded)
				}
			}
			if f.name == "google" && strings.Contains(translationJSON(back), `"content":"hidden"`) {
				t.Fatal("opaque thought became visible content")
			}
		})
	}
}
func TestTranslationNativeCiphertextAndHiddenText(t *testing.T) {
	text := "hidden exact text"
	for _, item := range []object{
		{"type": "reasoning", "id": "rs_native", "encrypted_content": "native-ciphertext", "summary": []any{}},
		reasoningItem("", reasoningEnvelope{Signature: "abcdefghijklmnop==", Text: &text}),
	} {
		msgs, e := toMessages(object{"input": []any{item}}, Responses)
		if e != nil {
			t.Fatal(e)
		}
		back, e := messagesToResponses(msgs)
		if e != nil {
			t.Fatal(e)
		}
		if translationJSON(back[0]) != translationJSON(item) {
			t.Fatalf("changed reasoning: %s", translationJSON(back))
		}
		blocks := reasoningToAnthropic(item)
		if len(blocks) != 1 {
			t.Fatal(blocks)
		}
		restored := anthropicReasoning(obj(blocks[0]))
		if str(item["encrypted_content"]) == "native-ciphertext" && restored["encrypted_content"] != "native-ciphertext" {
			t.Fatal(restored)
		}
	}
}
func TestTranslationCustomNamespaceAndSignatures(t *testing.T) {
	request := translationObject(t, `{"model":"m","input":[],"tools":[{"type":"namespace","name":"work","tools":[{"type":"custom","name":"exec","description":"raw","format":{"type":"text"}}]}]}`)
	for _, wire := range []Wire{Chat, Anthropic} {
		translated, e := translateRequest(request, Responses, wire)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(translationJSON(translated), "work__exec") {
			t.Fatal(translated)
		}
	}
	msg := translationObject(t, `{"role":"assistant","tool_calls":[{"id":"call1","type":"function","function":{"name":"work__exec","arguments":"{\"input\":\"echo 🦀\\nraw\"}"},"provider_metadata":{"tool_kind":"custom","tool_name":"exec","tool_namespace":"work"},"extra_content":{"google":{"thought_signature":"call-bound"}}}]}`)
	r, e := parseResult(object{"choices": []any{object{"message": msg}}}, Chat)
	if e != nil {
		t.Fatal(e)
	}
	if e = validateTools(r, request, Responses); e != nil {
		t.Fatal(e)
	}
	output := r.response(Responses)
	item := obj(arr(output["output"])[0])
	if item["type"] != "custom_tool_call" || item["namespace"] != "work" || item["input"] != "echo 🦀\nraw" {
		t.Fatal(item)
	}
	if str(obj(obj(item["extra_content"])["google"])["thought_signature"]) != "call-bound" {
		t.Fatal(item)
	}
	back, e := translateRequest(object{"input": arr(output["output"])}, Responses, Anthropic)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(translationJSON(back), "call-bound") {
		t.Fatal(back)
	}
}
func TestTranslationCustomCatalogRestoration(t *testing.T) {
	r, e := parseResult(translationObject(t, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"exec","arguments":"{\"input\":\"hello\"}"}}]}}]}`), Chat)
	if e != nil {
		t.Fatal(e)
	}
	if e = validateTools(r, translationObject(t, `{"tools":[{"type":"custom","name":"exec"}]}`), Responses); e != nil {
		t.Fatal(e)
	}
	if obj(r.Items[0])["type"] != "custom_tool_call" {
		t.Fatal(r.Items)
	}
	bad := translationObject(t, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c","function":{"name":"exec","arguments":"{\"input\":7}"},"provider_metadata":{"tool_kind":"custom"}}]}}]}`)
	if _, e := parseResult(bad, Chat); e == nil {
		t.Fatal("nonstring custom input accepted")
	}
}
func TestTranslationCacheUsageMatrix(t *testing.T) {
	u := Usage{}
	a, b, c, d := int64(20), int64(5), int64(7), int64(3)
	u.InputTokens = &a
	u.OutputTokens = &b
	u.CacheReadTokens = &c
	u.CacheWriteTokens = &d
	for _, w := range []Wire{Chat, Responses, Anthropic} {
		m := translationObject(t, translationJSON(object{"usage": usageObject(u, w)}))
		got := readUsage(m, w)
		if translationJSON(got) != translationJSON(u) {
			t.Fatalf("%s: %s", w, translationJSON(got))
		}
	}
	for _, w := range []Wire{Chat, Responses, Anthropic} {
		if got := readUsage(object{}, w); got.InputTokens != nil || got.OutputTokens != nil || got.CacheWriteTokens != nil {
			t.Fatal(got)
		}
	}
}
func TestTranslationLateSignatureOrdering(t *testing.T) {
	h, _, _, _, usage := harness(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w,
			"data: {\"id\":\"upstream-private\",\"choices\":[{\"delta\":{\"reasoning_content\":\"thought\"}}]}\n\n"+
				"data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n"+
				"data: {\"choices\":[{\"delta\":{\"reasoning_signature\":\"devin-sig1:anthropic:late\"}}]}\n\n"+
				"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"cache_creation_input_tokens\":3}}\n\ndata: [DONE]\n\n")
	}, Chat)
	h.c.ProviderStates = receiptMemory{}
	out := call(h, "/v1/messages", `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	body := out.Body.String()
	if out.Code != 200 {
		t.Fatal(body)
	}
	sig := strings.Index(body, `"type":"signature_delta"`)
	stop := strings.Index(body, `event: content_block_stop`)
	text := strings.Index(body, `"text":"answer"`)
	if sig < 0 || stop < sig || text < stop {
		t.Fatalf("invalid signature order: %s", body)
	}
	if len(usage.records) != 1 || usage.records[0].ResponseID == "" || usage.records[0].ResponseID == "upstream-private" {
		t.Fatal(usage.records)
	}
	if usage.records[0].Usage.CacheWriteTokens == nil || *usage.records[0].Usage.CacheWriteTokens != 3 {
		t.Fatal(usage.records)
	}
}
func TestTranslationErrorUsageSurvives(t *testing.T) {
	for _, wire := range []Wire{Chat, Responses, Anthropic} {
		t.Run(string(wire), func(t *testing.T) {
			rec := &UsageRecord{}
			w := httptest.NewRecorder()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			event := object{"type": "error", "usage": object{"cache_creation_input_tokens": json.Number("9")}}
			if wire == Responses {
				event = object{"type": "response.failed", "response": object{"usage": object{"cache_creation_input_tokens": json.Number("9")}}}
			}
			resp := &http.Response{Body: http.NoBody}
			resp.Body = ioNopCloser("data: " + translationJSON(event) + "\n\n")
			h := &Handler{}
			e := h.deliverStream(ctx, w, Principal{}, Chat, Generate, object{"stream": true}, Target{Wire: wire}, resp, rec, cancel, time.Second)
			if e == nil || rec.Usage.CacheWriteTokens == nil || *rec.Usage.CacheWriteTokens != 9 {
				t.Fatalf("error=%v usage=%+v", e, rec.Usage)
			}
		})
	}
}

type translationReader struct{ *strings.Reader }

func (translationReader) Close() error       { return nil }
func ioNopCloser(s string) translationReader { return translationReader{strings.NewReader(s)} }
func TestTranslationStreamBoundsAndFraming(t *testing.T) {
	for _, w := range []Wire{Chat, Responses, Anthropic} {
		s := newTranslationStream(w)
		if e := s.consume(object{"index": json.Number("999999999")}); e == nil {
			t.Fatal("unbounded index accepted")
		}
	}
	s := newTranslationStream(Chat)
	for _, raw := range []string{
		`{"choices":[{"delta":{"reasoning_content":"think"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"run","arguments":"{"},"extra_content":{"google":{"thought_signature":"opaque"}}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]}}]}`,
		`{"choices":[{"delta":{"kiro_reasoning":"signature:late"}}]}`,
	} {
		if e := s.consume(translationObject(t, raw)); e != nil {
			t.Fatal(e)
		}
	}
	r, e := s.result()
	if e != nil {
		t.Fatal(e)
	}
	out := r.response(Responses)
	items := arr(out["output"])
	if len(items) != 2 || obj(items[0])["type"] != "reasoning" || obj(items[1])["type"] != "function_call" {
		t.Fatal(out)
	}
	if !strings.Contains(translationJSON(out), "opaque") {
		t.Fatal(out)
	}
	env, e := decodeReasoning(str(obj(items[0])["encrypted_content"]))
	if e != nil || env.Kiro != "signature:late" {
		t.Fatal(env, e)
	}
}

func TestTranslationMultipleReasoningAndHiddenSummary(t *testing.T) {
	hidden := "not-visible-signed-text"
	items := []any{reasoningItem("first", reasoningEnvelope{Signature: "first-signature"}), reasoningItem("", reasoningEnvelope{Signature: "hidden-signature", Text: &hidden}), reasoningItem("last", reasoningEnvelope{Kiro: "opaque-last"})}
	r, e := parseResult(object{"id": "r", "output": items}, Responses)
	if e != nil {
		t.Fatal(e)
	}
	for _, wire := range []Wire{Chat, Anthropic} {
		out := r.response(wire)
		raw := translationJSON(out)
		if strings.Contains(raw, hidden) {
			t.Fatalf("hidden signed text leaked on %s: %s", wire, raw)
		}
		restored, e := parseResult(translationObject(t, raw), wire)
		if e != nil {
			t.Fatal(e)
		}
		if len(restored.Items) != 3 {
			t.Fatalf("lost reasoning items on %s: %s", wire, translationJSON(restored.Items))
		}
	}
}
func TestTranslationToolSearchAndHostedCells(t *testing.T) {
	body := translationObject(t, `{"model":"m","tools":[{"type":"tool_search"}],"input":[{"type":"tool_search_call","call_id":"search1","arguments":{"query":"network","limit":3}},{"type":"tool_search_output","call_id":"search1","status":"completed","tools":[{"type":"function","name":"fetch","parameters":{"type":"object"}}]}]}`)
	for _, wire := range []Wire{Chat, Anthropic} {
		translated, e := translateRequest(body, Responses, wire)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(translationJSON(translated), "input_schema") && wire == Anthropic {
			t.Fatal(translated)
		}
		back, e := translateRequest(translated, wire, Responses)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(translationJSON(back), `"type":"tool_search_output"`) {
			t.Fatal(back)
		}
	}
	for _, kind := range []string{"web_search", "image_generation", "video_generation"} {
		item := object{"type": kind + "_call", "id": "hosted1", "status": "completed", "result": object{"url": "https://artifacts.example/signed-fixture"}}
		r, e := parseResult(object{"output": []any{item}}, Responses)
		if e != nil {
			t.Fatal(e)
		}
		for _, wire := range []Wire{Chat, Anthropic} {
			out := r.response(wire)
			back, e := parseResult(translationObject(t, translationJSON(out)), wire)
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, v := range back.Items {
				q := obj(v)
				if q["type"] == kind+"_call" {
					found = true
					if translationJSON(q["result"]) != translationJSON(item["result"]) {
						t.Fatal(q)
					}
				}
			}
			if !found {
				t.Fatalf("lost hosted %s on %s: %s", kind, wire, translationJSON(back.Items))
			}
		}
	}
}
func TestTranslationRichBlocksAndToolErrors(t *testing.T) {
	body := translationObject(t, `{"messages":[{"role":"assistant","content":[{"type":"text","text":"answer","citations":[{"type":"web_search_result_location","url":"https://example.test","title":"source","encrypted_index":"opaque-citation"}]},{"type":"tool_use","id":"c","name":"read","input":{"n":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","is_error":true,"content":[{"type":"text","text":"failed"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"YWJj"}}]}]}]}`)
	chat, e := translateRequest(body, Anthropic, Chat)
	if e != nil {
		t.Fatal(e)
	}
	responses, e := translateRequest(chat, Chat, Responses)
	if e != nil {
		t.Fatal(e)
	}
	back, e := translateRequest(responses, Responses, Anthropic)
	if e != nil {
		t.Fatal(e)
	}
	s := translationJSON(back)
	for _, want := range []string{"opaque-citation", "9007199254740993", `"is_error":true`, "YWJj"} {
		if !strings.Contains(s, want) {
			t.Fatalf("lost %s: %s", want, s)
		}
	}
}
func TestAggregateResponseHelpers(t *testing.T) {
	for _, wire := range []Wire{Chat, Responses, Anthropic} {
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: ioNopCloser(streamFixture(wire))}
		b, e := AggregateResponse(context.Background(), resp, wire)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(string(b), "hello") {
			t.Fatal(string(b))
		}
		for _, to := range []Wire{Chat, Responses, Anthropic} {
			out, e := TranslateResponseJSON(b, Chat, to)
			if e != nil || !strings.Contains(string(out), "hello") {
				t.Fatal(string(out), e)
			}
		}
	}
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: ioNopCloser("data: {\"type\":\"error\",\"usage\":{\"cache_creation_input_tokens\":5}}\n\n")}
	b, e := AggregateResponse(context.Background(), resp, Chat)
	if e == nil || !strings.Contains(string(b), `"cache_creation_input_tokens":5`) || strings.Contains(string(b), "choices") {
		t.Fatal(string(b), e)
	}
}
func TestTranslationNativeReceiptBeforeTerminal(t *testing.T) {
	h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"native\",\"choices\":[{\"delta\":{\"reasoning_signature\":\"part\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"reasoning_signature\":\"-two\"}}]}\n\ndata: [DONE]\n\n")
	}, Chat)
	out := call(h, "/v1/chat/completions", `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if strings.Contains(out.Body.String(), "[DONE]") {
		t.Fatal("terminal success preceded failed receipt save")
	}
	h.c.ProviderStates = receiptMemory{}
	out = call(h, "/v1/chat/completions", `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if !strings.Contains(out.Body.String(), "[DONE]") {
		t.Fatal(out.Body.String())
	}
	if len(h.c.ProviderStates.(receiptMemory)) != 1 {
		t.Fatal("fragmented signature created multiple receipts")
	}
}

func TestHostedCodecOutputMatrix(t *testing.T) {
	body := translationObject(t, `{"id":"hosted","choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"hosted_outputs":[{"type":"web_search_call","id":"s","status":"completed","action":{"query":"fixture"},"sources":[{"url":"https://example.test","title":"source"}]},{"type":"image_generation_call","id":"i","status":"completed","artifacts":[{"id":"artifact1","url":"https://artifacts.test/image","content_type":"image/png"}]},{"type":"video_generation_call","id":"v","status":"completed","artifacts":[{"id":"artifact2","url":"https://artifacts.test/video","content_type":"video/mp4"}]}]}`)
	r, e := parseResult(body, Chat)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Tools) != 0 {
		t.Fatal("hosted output became client tools")
	}
	for _, wire := range []Wire{Chat, Responses, Anthropic} {
		out := r.response(wire)
		raw := translationJSON(out)
		for _, want := range []string{"artifact1", "artifact2", "https://example.test"} {
			if !strings.Contains(raw, want) {
				t.Fatalf("%s lost %s", wire, want)
			}
		}
		if wire == Chat && strings.Contains(raw, `"tool_calls"`) {
			t.Fatal("hosted output leaked as a function")
		}
		recorder := httptest.NewRecorder()
		emitter := streamEmitter{w: recorder, wire: wire, id: "public", model: "m"}
		if e := emitter.finish(r); e != nil {
			t.Fatal(e)
		}
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: ioNopCloser(recorder.Body.String())}
		got, e := (HostedCodec{}).Collect(context.Background(), resp, wire, 1<<20)
		if e != nil {
			t.Fatal(e)
		}
		m := translationObject(t, string(got))
		if len(arr(m["hosted_outputs"])) != 3 {
			t.Fatalf("%s lost streamed hosted cells: %s", wire, got)
		}
	}
	small := &http.Response{StatusCode: 200, Header: http.Header{}, Body: ioNopCloser(translationJSON(body))}
	if _, e := (HostedCodec{}).Collect(context.Background(), small, Chat, 5); e == nil {
		t.Fatal("hosted aggregate ignored caller limit")
	}
}
func TestTranslationModelAndUsageDetails(t *testing.T) {
	r, e := parseResult(translationObject(t, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":7,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":1},"extra_content":{"google":{"thoughts_token_count":1}}}}`), Chat)
	if e != nil {
		t.Fatal(e)
	}
	out := r.response(Responses)
	usage := obj(out["usage"])
	if obj(usage["output_tokens_details"])["reasoning_tokens"] != json.Number("1") || obj(obj(usage["extra_content"])["google"])["thoughts_token_count"] != json.Number("1") {
		t.Fatal(usage)
	}
	u := readUsage(translationObject(t, `{"usage":{"input_tokens":9223372036854775807,"cache_read_input_tokens":1}}`), Anthropic)
	if u.InputTokens != nil {
		t.Fatal("cache addition overflow accepted")
	}
}

func TestTranslationToolKindAdmissionAndStructuredFormat(t *testing.T) {
	body := translationObject(t, `{"tools":[{"type":"function","name":"exec","parameters":{"type":"object"}}]}`)
	r, e := parseResult(translationObject(t, `{"output":[{"type":"custom_tool_call","call_id":"c","name":"exec","input":"raw"}]}`), Responses)
	if e != nil {
		t.Fatal(e)
	}
	if e = validateTools(r, body, Responses); e == nil {
		t.Fatal("provider changed declared tool kind")
	}
	r, e = parseResult(translationObject(t, `{"output":[{"type":"web_search_call","id":"s","action":{"query":"q"}}]}`), Responses)
	if e != nil {
		t.Fatal(e)
	}
	if validateTools(r, body, Responses) == nil {
		t.Fatal("undeclared hosted output accepted")
	}
	request := translationObject(t, `{"messages":[],"response_format":{"type":"json_schema","json_schema":{"name":"result","strict":true,"schema":{"type":"object","properties":{"number":{"type":"integer","minimum":9007199254740993}},"required":["number"]}}}}`)
	translated, e := translateRequest(request, Chat, Responses)
	if e != nil {
		t.Fatal(e)
	}
	f := obj(obj(translated["text"])["format"])
	if f["schema"] == nil || f["json_schema"] != nil {
		t.Fatal(f)
	}
	back, e := translateRequest(translated, Responses, Chat)
	if e != nil {
		t.Fatal(e)
	}
	if translationJSON(back["response_format"]) != translationJSON(request["response_format"]) {
		t.Fatal(back)
	}
}
func TestTranslationNativeReasoningReceiptBindsText(t *testing.T) {
	s := newStreamReceipts(Chat)
	for _, raw := range []string{`{"choices":[{"delta":{"reasoning_content":"signed "}}]}`, `{"choices":[{"delta":{"content":"visible unrelated"}}]}`, `{"choices":[{"delta":{"reasoning_content":"text","reasoning_signature":"sig"}}]}`} {
		if e := s.consume(translationObject(t, raw)); e != nil {
			t.Fatal(e)
		}
	}
	values, e := s.values()
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(translationJSON(values), "visible unrelated") {
		t.Fatal("native receipt retained visible text")
	}
	h := &Handler{c: Config{Authorizer: &testAuth{}, ProviderStates: receiptMemory{}}}
	scope := scopedAdapterState{h: h, p: Principal{OrganizationID: "o", UserID: "u"}, t: Target{Provider: "p", Model: "m"}, c: Credential{AccountID: "a"}}
	ctx := context.WithValue(context.Background(), replayScopeKey{}, scope)
	if e = h.CaptureReplayReceipts(ctx, values); e != nil {
		t.Fatal(e)
	}
	history := translationObject(t, `{"messages":[{"role":"assistant","reasoning_content":"signed text","reasoning_signature":"sig"}]}`)
	if e = h.validateReplay(ctx, history, scope); e != nil {
		t.Fatal(e)
	}
	obj(arr(history["messages"])[0])["reasoning_content"] = "changed"
	if e = h.validateReplay(ctx, history, scope); e == nil {
		t.Fatal("changed signed text replayed")
	}
}

func TestTranslationStreamReasoningDetailsWithoutIndexes(t *testing.T) {
	state := newTranslationStream(Chat)
	event := translationObject(t, `{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.encrypted","data":"red-one","format":"anthropic-claude-v1"},{"type":"reasoning.encrypted","data":"red-two","format":"anthropic-claude-v1"},{"type":"reasoning.text","text":"thought","signature":"signed","format":"anthropic-claude-v1"}]}}]}`)
	if e := state.consume(event); e != nil {
		t.Fatal(e)
	}
	r, e := state.result()
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Items) != 3 {
		t.Fatalf("unindexed details collapsed: %s", translationJSON(r.Items))
	}
	recorder := httptest.NewRecorder()
	emitter := streamEmitter{w: recorder, wire: Chat, id: "r"}
	if e = emitter.finish(r); e != nil {
		t.Fatal(e)
	}
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: ioNopCloser(recorder.Body.String())}
	out, e := AggregateResponse(context.Background(), resp, Chat)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(out), "red-one") || !strings.Contains(string(out), "red-two") {
		t.Fatal(string(out))
	}
}

func TestTranslationLoadedCatalogDedupAndRefusal(t *testing.T) {
	tools := []any{object{"type": "function", "name": "fetch", "parameters": object{"type": "object"}}}
	b := object{"tools": tools, "input": []any{object{"type": "additional_tools", "tools": tools}}}
	out, e := translateRequest(b, Responses, Chat)
	if e != nil || len(arr(out["tools"])) != 1 {
		t.Fatal(out, e)
	}
	b["tools"] = append(tools, object{"type": "function", "name": "fetch", "parameters": object{"type": "object", "required": []any{"x"}}})
	if _, e = translateRequest(b, Responses, Chat); e == nil {
		t.Fatal("conflicting declaration accepted")
	}
	r, e := parseResult(translationObject(t, `{"choices":[{"message":{"role":"assistant","content":null,"refusal":"cannot answer"}}]}`), Chat)
	if e != nil {
		t.Fatal(e)
	}
	back := r.response(Chat)
	msg := obj(obj(arr(back["choices"])[0])["message"])
	if msg["refusal"] != "cannot answer" || msg["content"] != nil {
		t.Fatal(msg)
	}
	rich := translationObject(t, `{"input":[{"type":"function_call_output","call_id":"c","output":[{"type":"input_text","text":"result"},{"type":"input_image","image_url":"https://images.test/fixture.png"}]}]}`)
	if _, e := translateRequest(rich, Responses, Anthropic); e != nil {
		t.Fatal(e)
	}
}
