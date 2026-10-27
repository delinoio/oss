// SPDX-License-Identifier: Apache-2.0
package devin

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

func input(body string) inference.AdapterInput {
	return inference.AdapterInput{Target: inference.Target{Model: "swe-2-high", Wire: Wire}, Credential: inference.Credential{Secret: "fixture-key"}, Operation: inference.Generate, Wire: inference.Chat, Body: json.RawMessage(body)}
}
func fields(t *testing.T, b []byte) map[int][]field {
	t.Helper()
	out := map[int][]field{}
	if err := walk(b, func(f field) error { out[f.num] = append(out[f.num], f); return nil }); err != nil {
		t.Fatal(err)
	}
	return out
}
func build(t *testing.T, in inference.AdapterInput) map[int][]field {
	t.Helper()
	req, err := New().Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	flags, b, err := readFrame(bytes.NewReader(req.Body))
	if err != nil || flags != 0 {
		t.Fatalf("frame: %d %v", flags, err)
	}
	return fields(t, b)
}

// These expectations port the pinned request-shape, output-budget, metadata,
// prompt-cache and chat-wire-fixes fixtures. They do not require a live account.
func TestRequestShape(t *testing.T) {
	in := input(`{"temperature":0,"max_tokens":51,"messages":[{"role":"system","content":"S1"},{"role":"system","content":"S2"},{"role":"user","content":"U1"},{"role":"system","content":"LATE"},{"role":"user","content":"U2"}]}`)
	in.Target.BaseURL = "https://eu.windsurf.com/_route/api_server/"
	req, err := New().Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL != "https://eu.windsurf.com/_route/api_server"+chatPath || req.ResponseWire != inference.Chat {
		t.Fatalf("route: %+v", req)
	}
	if req.Headers.Get("Authorization") != "Basic fixture-key-fixture-key" || req.Headers.Get("Content-Type") != "application/connect+proto" {
		t.Fatal(req.Headers)
	}
	f := build(t, in)
	if string(f[2][0].b) != "S1\n\nS2" || len(f[3]) != 2 {
		t.Fatal("system channel")
	}
	if string(fields(t, f[3][1].b)[3][0].b) != "<system>\nLATE\n</system>\nU2" {
		t.Fatal("late system")
	}
	m := fields(t, f[1][0].b)
	if string(m[1][0].b) != "chisel" || string(m[2][0].b) != "2026.8.18" || string(m[5][0].b) != "windows" || len(m[31][0].b) != 732 || len(m[9]) != 0 {
		t.Fatal("metadata shape")
	}
	c := fields(t, f[8][0].b)
	if c[2][0].n != 51 || c[3][0].n != 128000 || math.Float64frombits(binary.LittleEndian.Uint64(c[5][0].b)) != 0.0001 {
		t.Fatal("completion config")
	}
	if fields(t, f[13][0].b)[1][0].n != 1 || len(f[17]) != 0 || string(f[21][0].b) != "swe-2-high" {
		t.Fatal("cache/model fields")
	}
	if string(fields(t, build(t, input(`{"messages":[{"role":"system","content":"ONLY"}]}`))[3][0].b)[3][0].b) != "<system>\nONLY\n</system>" {
		t.Fatal("system-only prompt")
	}
}

func TestCredentialsAndTenantBoundary(t *testing.T) {
	if token("a.b.c") != "devin-session-token$a.b.c" || token("cog_example") != "cog_example" || token("devin-session-token$a.b.c") != "devin-session-token$a.b.c" {
		t.Fatal("normalization")
	}
	for _, host := range []string{"http://server.codeium.com", "https://attacker.example", "https://server.codeium.com.evil.example", "https://user:secret@server.codeium.com", "https://server.codeium.com:8443", "https://server.codeium.com/?q=1", "https://eu.windsurf.com/%2e%2e/steal", "https://server.codeium.com/../x"} {
		in := input(`{"messages":[{"role":"user","content":"hi"}]}`)
		in.Target.BaseURL = host
		if _, err := New().Build(context.Background(), in); !errors.Is(err, inference.ErrDestination) {
			t.Fatalf("accepted %q: %v", host, err)
		}
	}
}

func TestToolsImagesAndAdjacentResults(t *testing.T) {
	f := build(t, input(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}}]},{"role":"assistant","content":null,"reasoning_content":"thought","reasoning_signature":"devin-sig1:anthropic:sig","tool_calls":[{"id":"call1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call1","content":"a"},{"role":"tool","tool_call_id":"call1","content":"b","is_error":true},{"role":"assistant","content":""},{"role":"tool","tool_call_id":"call1","content":"c"}],"tools":[{"type":"function","function":{"name":"read","description":"Takes a task_id parameter identifying the task","parameters":{"type":"object"}}}]}`))
	if len(f[3]) != 5 {
		t.Fatalf("adjacency: %d", len(f[3]))
	}
	if len(fields(t, f[3][0].b)[10]) != 1 {
		t.Fatal("image lost")
	}
	a := fields(t, f[3][1].b)
	if string(a[11][0].b) != "thought" || string(a[12][0].b) != "sig" || string(a[18][0].b) != "anthropic" || len(a[6]) != 1 {
		t.Fatal("assistant replay")
	}
	r := fields(t, f[3][2].b)
	if string(r[3][0].b) != "a\n\nERROR: b" || r[9][0].n != 1 || string(r[7][0].b) != "call1" {
		t.Fatal("tool results")
	}
	if !strings.HasPrefix(string(fields(t, f[10][0].b)[2][0].b), "Accepts") {
		t.Fatal("description rewrite")
	}
}

func TestSchemaAndModelVariants(t *testing.T) {
	for _, tc := range []struct{ model, effort, want string }{{"swe-2-high", "low", "swe-2-medium"}, {"swe-2", "ultra", "swe-2-max"}, {"claude-opus-4.7-max", "low", "claude-opus-4-7-max"}, {"gpt-5-6-sol-medium-priority", "", "gpt-5-6-sol-medium-priority"}, {"swe-1.7", "", "swe-1-7-medium"}} {
		if got := resolveModel(tc.model, tc.effort); got != tc.want {
			t.Fatalf("%s => %s", tc.model, got)
		}
	}
	in := input(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"enum":{"type":["string","null"],"enum":["x"]}},"default":{"type":["string","null"]}}}}]}`)
	in.Target.Model = "gemini-3-8-flash-medium"
	f := build(t, in)
	params := fields(t, f[10][0].b)[3][0].b
	var schema object
	if err := json.Unmarshal(params, &schema); err != nil {
		t.Fatal(err)
	}
	if obj(obj(schema["properties"])["enum"])["type"] != "string" {
		t.Fatal(string(params))
	}
	if len(arr(obj(schema["default"])["type"])) != 2 {
		t.Fatal("instance data rewritten")
	}
}

func eos(body string) []byte { b := frame([]byte(body)); b[0] = 2; return b }
func decodeStream(t *testing.T, b []byte) (string, error) {
	t.Helper()
	resp, err := New().Decode(context.Background(), &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(b))})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	return string(out), err
}
func events(t *testing.T, stream string) []object {
	t.Helper()
	var out []object
	for _, line := range strings.Split(stream, "\n") {
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var m object
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func TestStreamingToolsUsageAndSignatures(t *testing.T) {
	var wire []byte
	wire = append(wire, frame(textField(nil, 9, "thought"))...)
	wire = append(wire, frame(textField(nil, 3, "answer"))...)
	wire = append(wire, frame(blob(nil, 6, toolCall("a", "read", "{")))...)
	wire = append(wire, frame(blob(nil, 6, textField(nil, 3, "}")))...)
	u := number(number(number(number(nil, 2, 1000), 3, 10), 4, 0), 5, 57000)
	terminal := blob(number(textField(textField(nil, 10, "sig"), 21, "anthropic"), 5, 10), 7, u)
	wire = append(wire, frame(terminal)...)
	wire = append(wire, eos(`{}`)...)
	out, err := decodeStream(t, wire)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{`"reasoning_content":"thought"`, `"content":"answer"`, `"reasoning_signature":"devin-sig1:anthropic:sig"`, `"prompt_tokens":58000`, `"cached_tokens":57000`, `"finish_reason":"tool_calls"`, `data: [DONE]`} {
		if !strings.Contains(out, needle) {
			t.Fatalf("missing %s in %s", needle, out)
		}
	}
	if strings.Count(out, `"name":"read"`) != 1 {
		t.Fatal("tool start repeated")
	}
	_ = events(t, out)
}

func TestUsageUnknownZeroAndAuthoritativePrecedence(t *testing.T) {
	u, err := modelUsage(number(nil, 3, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := u["prompt_tokens"]; ok {
		t.Fatal("invented input")
	}
	if _, ok := u["total_tokens"]; ok {
		t.Fatal("invented total")
	}
	if u["completion_tokens"] != int64(0) {
		t.Fatal("lost zero")
	}
	fallback := blob(nil, 2, textField(blob(nil, 4, append([]byte{21}, 0, 0, 200, 66)), 5, "input_tokens"))
	b := blob(blob(nil, 28, fallback), 7, number(nil, 2, 7))
	out, err := decodeStream(t, append(frame(b), eos(`{}`)...))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"prompt_tokens":100`) || !strings.Contains(out, `"prompt_tokens":7`) {
		t.Fatal(out)
	}
}

func TestStreamFailuresCannotBecomeSuccess(t *testing.T) {
	for name, b := range map[string][]byte{
		"missing trailer": frame(number(nil, 5, 2)),
		"truncated field": append(frame([]byte{26, 4, 'x'}), eos(`{}`)...),
		"truncated frame": {0, 0, 0, 0, 4, 'x'},
		"oversized frame": {0, 1, 0, 0, 1},
		"orphan args":     append(frame(blob(nil, 6, textField(nil, 3, "{}"))), eos(`{}`)...),
		"late error":      append(frame(number(textField(nil, 3, "hi"), 5, 2)), eos(`{"error":{"code":"permission_denied","message":"secret-canary"}}`)...),
		"bad trailer":     eos(`null`),
	} {
		t.Run(name, func(t *testing.T) {
			out, err := decodeStream(t, b)
			if err == nil || strings.Contains(out, "[DONE]") {
				t.Fatalf("false success: %s %v", out, err)
			}
			if strings.Contains(err.Error(), "secret-canary") {
				t.Fatal("secret leak")
			}
		})
	}
}

func TestGzipAndCancellation(t *testing.T) {
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	_, _ = z.Write(textField(nil, 3, "compressed"))
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	b := frame(compressed.Bytes())
	b[0] = 1
	out, err := decodeStream(t, append(b, eos(`{}`)...))
	if err != nil || !strings.Contains(out, "compressed") {
		t.Fatalf("gzip %s %v", out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r, w := io.Pipe()
	defer w.Close()
	resp, err := New().Decode(ctx, &http.Response{StatusCode: 200, Body: r})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(resp.Body); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not unblock read")
	}
}

func TestHostedSearch(t *testing.T) {
	in := input(`{"commands":{"search_query":[{"q":" query "}]}}`)
	in.Operation = inference.Search
	req, err := New().Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(req.URL, searchPath) || req.Headers.Get("Content-Type") != "application/proto" || req.Headers.Get("Authorization") != "" {
		t.Fatal("search transport")
	}
	f := fields(t, req.Body)
	if string(f[2][0].b) != "query" || f[3][0].n != 5 {
		t.Fatal("search body")
	}
	result := func(link, title, snippet string) []byte {
		return textField(textField(textField(nil, 3, link), 4, title), 7, snippet)
	}
	b := blob(blob(blob(nil, 1, result("https://docs.example/search", "Search docs", "Excerpt.")), 1, result("javascript:alert(1)", "Unsafe", "drop")), 1, result("https://docs.example/search", "Later title", "Latest excerpt."))
	b = textField(b, 3, "Provider summary.")
	u, _ := url.Parse(req.URL)
	resp, err := New().Decode(context.Background(), &http.Response{StatusCode: 200, Request: &http.Request{URL: u}, Body: io.NopCloser(bytes.NewReader(b))})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var answer object
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatal(err)
	}
	if answer["output"] != "Provider summary.\n\nSearch results:\n- Search docs: Latest excerpt. [https://docs.example/search]" || len(arr(answer["results"])) != 1 {
		t.Fatal(string(body))
	}
	if _, err := searchResponse([]byte{10, 128}); err == nil {
		t.Fatal("malformed search accepted")
	}
	in.Body = json.RawMessage(`{"commands":{"search_query":[{"q":"a"},{"q":"b"}]}}`)
	if _, err := New().Build(context.Background(), in); err == nil {
		t.Fatal("batch silently dropped")
	}
}

func TestPathFailuresAndInboundWires(t *testing.T) {
	for _, op := range []inference.Operation{inference.Compact, inference.CountTokens, inference.ImageGeneration, inference.ImageEdit} {
		in := input(`{}`)
		in.Operation = op
		if _, err := New().Build(context.Background(), in); !errors.Is(err, inference.ErrUnsupported) {
			t.Fatal(op, err)
		}
	}
	for _, tc := range []struct {
		wire inference.Wire
		body string
	}{{inference.Responses, `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},{"type":"function_call","call_id":"c","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"c","output":"ok"}]}`}, {inference.Anthropic, `{"system":"S","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"f","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":"ok"}]}]}`}} {
		in := input(tc.body)
		in.Wire = tc.wire
		f := build(t, in)
		last := fields(t, f[3][len(f[3])-1].b)
		if string(last[7][0].b) != "c" {
			t.Fatal("tool result lost")
		}
	}
}

func TestFinishReasonsAndEmptyUsage(t *testing.T) {
	for code, want := range map[uint64]string{0: "stop", 1: "length", 2: "stop", 3: "length", 10: "tool_calls", 11: "content_filter", 13: "stop"} {
		out, err := decodeStream(t, append(frame(number(nil, 5, code)), eos(`{}`)...))
		if err != nil || !strings.Contains(out, `"finish_reason":"`+want+`"`) {
			t.Fatalf("%d: %s %v", code, out, err)
		}
		if strings.Contains(out, `"usage"`) {
			t.Fatal("absent usage synthesized")
		}
	}
}

func TestFragmentationLargeDeltasAndGzipBound(t *testing.T) {
	var output bytes.Buffer
	value := strings.Repeat("한", 40000)
	wire := append(frame(textField(nil, 3, value)), eos(`{}`)...)
	if err := transcode(context.Background(), &oneByteReader{b: wire}, &output); err != nil {
		t.Fatal(err)
	}
	var reconstructed strings.Builder
	for _, event := range events(t, output.String()) {
		for _, choice := range arr(event["choices"]) {
			reconstructed.WriteString(str(obj(obj(choice)["delta"])["content"]))
		}
	}
	if reconstructed.String() != value {
		t.Fatal("large UTF-8 delta corrupted")
	}
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	_, _ = io.Copy(z, io.LimitReader(zeroReader{}, maxFrame+1))
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	b := frame(compressed.Bytes())
	b[0] = 1
	if _, _, err := readFrame(bytes.NewReader(b)); !errors.Is(err, errLimit) {
		t.Fatalf("gzip expansion: %v", err)
	}
}

type oneByteReader struct{ b []byte }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = r.b[0]
	r.b = r.b[1:]
	return 1, nil
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestPartialUsageAndDimensionFallback(t *testing.T) {
	u, err := modelUsage(number(nil, 5, 17))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := u["prompt_tokens"]; ok {
		t.Fatal("cache subset invented a total")
	}
	if obj(u["prompt_tokens_details"])["cached_tokens"] != int64(17) {
		t.Fatal(u)
	}
	entry := func(metric string, value float32) []byte {
		v := binary.LittleEndian.AppendUint32([]byte{21}, math.Float32bits(value))
		return blob(nil, 2, textField(blob(nil, 4, v), 5, metric))
	}
	dim := append(entry("input_tokens", 5), entry("output_tokens", 3)...)
	dim = append(dim, entry("cached_input_tokens", 2)...)
	u, err = dimensionUsage(dim)
	if err != nil || u["total_tokens"] != int64(8) {
		t.Fatalf("dimension usage: %v %v", u, err)
	}
	if _, err = modelUsage(number(nil, 2, math.MaxUint64)); err == nil {
		t.Fatal("counter overflow accepted")
	}
}

func TestAnthropicResultAdjacencyAndSchemaDepth(t *testing.T) {
	in := input(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"same","content":"a"},{"type":"text","text":"between"},{"type":"tool_result","tool_use_id":"same","content":"b"}]}]}`)
	in.Wire = inference.Anthropic
	f := build(t, in)
	if len(f[3]) != 3 || string(fields(t, f[3][1].b)[3][0].b) != "between" {
		t.Fatal("intervening message lost")
	}
	var schema any = object{"type": "string"}
	for i := 0; i < 70; i++ {
		schema = object{"items": schema}
	}
	if _, err := rewriteSchema(schema, 0); err == nil {
		t.Fatal("unbounded recursion")
	}
	if got := reasoningSignature([]object{{"type": "thinking", "thinking": "t"}, {"type": "text", "text": "answer"}, {"type": "thinking", "signature": "unrelated"}}); got != "" {
		t.Fatal("signature attached across intervening content")
	}
	if got := reasoningSignature([]object{{"type": "thinking", "thinking": "t"}, {"type": "thinking", "signature": "matching"}}); got != "matching" {
		t.Fatal("late adjacent signature lost")
	}
}

func TestSearchSizeBound(t *testing.T) {
	u, _ := url.Parse("https://server.codeium.com" + searchPath)
	_, err := New().Decode(context.Background(), &http.Response{StatusCode: 200, Request: &http.Request{URL: u}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", (64<<10)+1)))})
	if !errors.Is(err, errLimit) {
		t.Fatalf("search bound: %v", err)
	}
}

func TestCloseUnblocksProducerAndSanitizesTrailer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, w := io.Pipe()
	defer w.Close()
	resp, err := New().Decode(ctx, &http.Response{StatusCode: 200, Body: r})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := w.Write(frame(textField(nil, 3, strings.Repeat("x", 100000)))); done <- err }()
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("producer did not close")
	}
	for code, status := range map[string]int{"unauthenticated": 401, "permission_denied": 403, "resource_exhausted": 429, "invalid_argument": 400, "deadline_exceeded": 504, "unavailable": 503, "invented-secret-code": 502} {
		err := trailer([]byte(`{"error":{"code":"` + code + `","message":"secret-canary"}}`))
		var e *inference.Error
		if !errors.As(err, &e) || e.Status != status || strings.Contains(err.Error(), "secret") {
			t.Fatalf("%s: %v", code, err)
		}
	}
}

func FuzzWire(f *testing.F) {
	f.Add([]byte{26, 2, 'h', 'i'})
	f.Add([]byte{10, 128})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<20 {
			t.Skip()
		}
		_ = walk(b, func(field) error { return nil })
		_, _ = modelUsage(b)
		_, _ = searchResponse(b)
		_, _ = ParseCatalog(b)
	})
}
