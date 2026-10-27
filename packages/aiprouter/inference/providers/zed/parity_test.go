// SPDX-License-Identifier: Apache-2.0
package zed

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

func TestCrossWireImagesAndDocuments(t *testing.T) {
	requests := map[inference.Wire]string{
		inference.Chat:      `{"messages":[{"role":"user","content":[{"type":"text","text":"inspect"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1hZ2U="}},{"type":"file","file":{"file_data":"data:application/pdf;base64,cGRm","filename":"spec.pdf"}}]}]}`,
		inference.Responses: `{"input":[{"role":"user","content":[{"type":"input_text","text":"inspect"},{"type":"input_image","image_url":"data:image/png;base64,aW1hZ2U="},{"type":"input_file","file_data":"data:application/pdf;base64,cGRm","filename":"spec.pdf"}]}]}`,
		inference.Anthropic: `{"messages":[{"role":"user","content":[{"type":"text","text":"inspect"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1hZ2U="}},{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"cGRm"}}]}]}`,
	}
	for wire, body := range requests {
		for _, provider := range []Provider{Anthropic, OpenAI, XAI, Google} {
			t.Run(string(wire)+"/"+string(provider), func(t *testing.T) {
				b := build(t, provider, wire, body)
				data := jsonString(b)
				for _, want := range []string{"inspect", "aW1hZ2U=", "cGRm"} {
					if !strings.Contains(data, want) {
						t.Fatalf("lost %s: %s", want, data)
					}
				}
				if provider == Google && !strings.Contains(data, "inlineData") {
					t.Fatal(data)
				}
			})
		}
	}
}

func TestSignedReasoningAndToolOrder(t *testing.T) {
	sig := "signatureABCDEF0123456789"
	envelope := "ocxr1:" + base64.StdEncoding.EncodeToString([]byte(`{"sig":"`+sig+`","txt":"hidden original","red":["opaque"]}`))
	body := `{"input":[{"type":"reasoning","encrypted_content":"` + envelope + `","summary":[{"type":"summary_text","text":"short"}]},{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"ok"}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`
	r := build(t, Anthropic, inference.Responses, body)
	msgs := arrayOf(r["messages"])
	parts := arrayOf(objectOf(msgs[0])["content"])
	if len(parts) != 3 || objectOf(parts[0])["type"] != "redacted_thinking" || objectOf(parts[1])["signature"] != sig || objectOf(parts[1])["thinking"] != "hidden original" || objectOf(parts[2])["type"] != "tool_use" {
		t.Fatal(jsonString(r))
	}
	chat := build(t, XAI, inference.Anthropic, `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"signed","signature":"`+sig+`"},{"type":"text","text":"answer"}]}]}`)
	if !strings.Contains(jsonString(chat), sig) || !strings.Contains(jsonString(chat), "answer") {
		t.Fatal(jsonString(chat))
	}
}

func TestCustomToolLowering(t *testing.T) {
	body := `{"input":[{"type":"custom_tool_call","call_id":"custom_1","name":"patch","input":"raw patch"},{"type":"custom_tool_call_output","call_id":"custom_1","output":"done"}],"tools":[{"type":"custom","name":"patch","format":{"type":"text"}}],"tool_choice":{"type":"custom","name":"patch"}}`
	for _, p := range []Provider{OpenAI, Anthropic, Google, XAI} {
		t.Run(string(p), func(t *testing.T) {
			r := build(t, p, inference.Responses, body)
			text := jsonString(r)
			if !strings.Contains(text, "raw patch") || !strings.Contains(text, "done") {
				t.Fatal(text)
			}
			if p != OpenAI && !strings.Contains(text, `"required":["input"]`) {
				t.Fatal(text)
			}
			if p == OpenAI && !strings.Contains(text, `"type":"custom_tool_call"`) {
				t.Fatal(text)
			}
		})
	}
}

func preparedBuild(t *testing.T, p Provider, options RequestOptions, model, body string) (*Adapter, context.Context, object) {
	t.Helper()
	a, _ := New(p)
	ctx := a.PrepareContext(WithRequestOptions(context.Background(), options))
	r, err := a.Build(ctx, inference.AdapterInput{Operation: inference.Generate, Wire: inference.Responses, Body: json.RawMessage(body), Target: inference.Target{Model: model, BaseURL: "https://cloud.zed.dev"}, Credential: inference.Credential{Secret: "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := decodeObject(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return a, ctx, m
}

func TestGoogleGenerationPolicy(t *testing.T) {
	for _, tc := range []struct {
		model, effort, want string
		ladder              []string
		cap                 int64
	}{
		{"gemini-3.5-flash", "minimal", "low", nil, 65536},
		{"gemini-3.5-flash", "none", "", nil, 65536},
		{"gemini-3.1-pro-preview", "max", "high", []string{"low", "high"}, 65535},
		{"unknown-alias", "high", "", nil, 100000},
		{"gemini-3.6-flash", "high", "", []string{}, 65536},
		{"gemini-3.1-flash-image", "high", "", []string{"high"}, 65536},
	} {
		t.Run(tc.model+tc.effort, func(t *testing.T) {
			_, _, envelope := preparedBuild(t, Google, RequestOptions{ReasoningEfforts: tc.ladder, ShowThinkingSummaries: true}, tc.model, `{"input":"hello","reasoning":{"effort":"`+tc.effort+`"},"max_output_tokens":100000,"text":{"format":{"type":"json_schema","schema":{"type":"object","additionalProperties":false,"properties":{"answer":{"type":"string"}}}}}}`)
			r := objectOf(envelope["provider_request"])
			gc := objectOf(r["generationConfig"])
			if stringOf(objectOf(gc["thinkingConfig"])["thinkingLevel"]) != tc.want {
				t.Fatal(jsonString(gc))
			}
			if n, _ := validCount(gc["maxOutputTokens"]); n != tc.cap {
				t.Fatal(jsonString(gc))
			}
			if gc["responseMimeType"] != "application/json" || objectOf(gc["responseJsonSchema"])["additionalProperties"] != false || gc["responseSchema"] != nil {
				t.Fatal(jsonString(gc))
			}
			if imageModel(tc.model) && len(arrayOf(gc["responseModalities"])) != 2 {
				t.Fatal(jsonString(gc))
			}
		})
	}
	_, _, m := preparedBuild(t, Google, RequestOptions{ReasoningEfforts: []string{"low", "high"}, ReasoningEffortMap: map[string]string{"max": "__omit__"}}, "gemini-3.1-pro-preview", `{"input":"hi","reasoning":{"effort":"ultra"}}`)
	if objectOf(objectOf(m["provider_request"])["generationConfig"])["thinkingConfig"] != nil {
		t.Fatal(jsonString(m))
	}
}

func TestGoogleSignaturePerCallAndReplay(t *testing.T) {
	first, second := "signature_first_0123456789", "signature_second_0123456789"
	input := `{"event":{"candidates":[{"content":{"parts":[{"thought":true,"text":"hidden","thoughtSignature":"` + first + `"},{"functionCall":{"id":"c1","name":"one","args":{}}},{"functionCall":{"id":"c2","name":"two","args":{}},"extra_content":{"google":{"thought_signature":"` + second + `"}}}]},"finishReason":"STOP"}]}}` + "\n"
	output := streamText(t, Google, input)
	var event object
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "data: {") {
			event, _ = decodeObject([]byte(strings.TrimPrefix(line, "data: ")))
			break
		}
	}
	delta := objectOf(objectOf(arrayOf(event["choices"])[0])["delta"])
	calls := arrayOf(delta["tool_calls"])
	if len(calls) != 2 || thoughtSignature(objectOf(calls[0])) != first || thoughtSignature(objectOf(calls[1])) != second || delta["reasoning_content"] != nil {
		t.Fatal(output)
	}
	delta["role"] = "assistant"
	r, err := googleRequest(object{"messages": []any{delta}}, inference.Chat)
	if err != nil {
		t.Fatal(err)
	}
	parts := arrayOf(objectOf(arrayOf(r["contents"])[0])["parts"])
	if thoughtSignature(objectOf(parts[0])) != first || thoughtSignature(objectOf(parts[1])) != second {
		t.Fatal(jsonString(r))
	}
}

func TestContextAliasesTrustedThreadAndIsolation(t *testing.T) {
	var wg sync.WaitGroup
	for _, name := range []string{"namespace/tool-one", "namespace/tool-two"} {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, ctx, m := preparedBuild(t, Anthropic, RequestOptions{ThreadID: "trusted-" + name}, "claude-haiku-4-5", `{"input":"hi","tools":[{"type":"function","name":"`+name+`","parameters":{"type":"object"}}]}`)
			if m["thread_id"] != "trusted-"+name {
				t.Error("trusted thread missing")
			}
			wireName := stringOf(objectOf(arrayOf(objectOf(m["provider_request"])["tools"])[0])["name"])
			if wireName == name || !safeToolName.MatchString(wireName) {
				t.Error("alias invalid")
			}
			stream := `{"type":"content_block_start","content_block":{"type":"tool_use","name":"` + wireName + `","id":"call_1","input":{}}}` + "\n{\"status\":\"stream_ended\"}\n"
			r, err := a.Decode(ctx, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))})
			if err != nil {
				t.Error(err)
				return
			}
			defer r.Body.Close()
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			if !strings.Contains(string(data), name) || strings.Contains(string(data), wireName) {
				t.Error("alias leaked or crossed request")
			}
			_, err = a.Build(ctx, inference.AdapterInput{Operation: inference.Generate, Wire: inference.Responses, Body: json.RawMessage(`{"input":"hi"}`), Target: inference.Target{Model: "claude", BaseURL: "https://cloud.zed.dev"}, Credential: inference.Credential{Secret: "fixture"}})
			if err == nil {
				t.Error("context reused")
			}
		}()
	}
	wg.Wait()
}

func TestChatCustomAndStrictGoogleTools(t *testing.T) {
	r := build(t, OpenAI, inference.Chat, `{"messages":[{"role":"assistant","tool_calls":[{"id":"c1","type":"custom","custom":{"name":"patch","input":"raw"}}]},{"role":"tool","tool_call_id":"c1","content":"done"}],"tools":[{"type":"custom","custom":{"name":"patch","format":{"type":"text"}}}],"tool_choice":{"type":"custom","custom":{"name":"patch"}}}`)
	if !strings.Contains(jsonString(r), `"type":"custom_tool_call"`) || !strings.Contains(jsonString(r), `"type":"custom_tool_call_output"`) || objectOf(r["tool_choice"])["type"] != "custom" {
		t.Fatal(jsonString(r))
	}
	r = build(t, Google, inference.Chat, `{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","strict":true,"parameters":{"type":"object"}}}],"tool_choice":"auto"}`)
	if objectOf(objectOf(r["toolConfig"])["functionCallingConfig"])["mode"] != "VALIDATED" {
		t.Fatal(jsonString(r))
	}
}

func TestGoogleImageOutputAndSignatureLimits(t *testing.T) {
	out := streamText(t, Google, `{"candidates":[{"content":{"parts":[{"inline_data":{"mime_type":"image/png","data":"aW1hZ2U="}}]},"finishReason":"STOP"}]}`+"\n")
	if !strings.Contains(out, "data:image/png;base64,aW1hZ2U=") {
		t.Fatal(out)
	}
	out = streamText(t, Google, `{"candidates":[{"content":{"parts":[{"text":"x","thoughtSignature":"`+strings.Repeat("a", (64<<10)+1)+`"}]},"finishReason":"STOP"}]}`+"\n")
	if !strings.Contains(out, "unsupported Google event") {
		t.Fatal("unbounded signature accepted")
	}
}

func TestTrustedOptionsAndAliasAdmission(t *testing.T) {
	a, _ := New(Anthropic)
	in := inference.AdapterInput{Operation: inference.Generate, Wire: inference.Responses, Body: json.RawMessage(`{"input":"hello","tools":[{"type":"function","name":"unsafe/name","parameters":{"type":"object"}}]}`), Target: inference.Target{Model: "claude", BaseURL: "https://cloud.zed.dev"}, Credential: inference.Credential{Secret: "fixture"}}
	if _, err := a.Build(context.Background(), in); err == nil {
		t.Fatal("alias build without restoration context")
	}
	_, ctx, m := preparedBuild(t, Anthropic, RequestOptions{ThreadID: "trusted"}, "claude", string(in.Body))
	if m["thread_id"] != "trusted" {
		t.Fatal("trusted ID lost")
	}
	s := ctx.Value(stateKey{}).(*requestState)
	if len(s.aliases) != 1 {
		t.Fatal("missing alias")
	}
	r, err := a.Build(a.PrepareContext(context.Background()), in)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := decodeObject(r.Body)
	if b["thread_id"] == "trusted" {
		t.Fatal("thread crossed request")
	}
}

func TestAnthropicEffortHeadroom(t *testing.T) {
	for _, model := range []string{"claude-haiku-4-5", "claude-opus-4-7"} {
		_, _, m := preparedBuild(t, Anthropic, RequestOptions{}, model, `{"input":"hi","reasoning":{"effort":"high"},"max_output_tokens":2000,"temperature":0.2}`)
		r := objectOf(m["provider_request"])
		thinking := objectOf(r["thinking"])
		if model == "claude-opus-4-7" {
			if thinking["type"] != "adaptive" || objectOf(r["output_config"])["effort"] != "high" {
				t.Fatal(jsonString(r))
			}
		} else {
			max, _ := validCount(r["max_tokens"])
			budget, _ := validCount(thinking["budget_tokens"])
			if max <= budget {
				t.Fatal(jsonString(r))
			}
		}
		if r["temperature"] != nil {
			t.Fatal("thinking retained rejected sampling")
		}
	}
}

func TestNamespacedCustomDeclarationsAndHistory(t *testing.T) {
	body := `{"input":[{"type":"custom_tool_call","call_id":"c1","name":"patch","namespace":"editor","input":"raw"},{"type":"custom_tool_call_output","call_id":"c1","output":"done"}],"tools":[{"type":"namespace","name":"editor","tools":[{"type":"custom","name":"patch","format":{"type":"text"}}]}],"tool_choice":{"type":"custom","name":"patch","namespace":"editor"}}`
	for _, p := range []Provider{Anthropic, Google, XAI} {
		r := build(t, p, inference.Responses, body)
		data := jsonString(r)
		if !strings.Contains(data, "editor__patch") || !strings.Contains(data, "raw") || strings.Contains(data, `"type":"namespace"`) {
			t.Fatal(data)
		}
	}
}
