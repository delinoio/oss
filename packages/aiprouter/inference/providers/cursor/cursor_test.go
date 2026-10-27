// SPDX-License-Identifier: Apache-2.0
// Mock scenarios adapted from opencodex at 88aaa754566d119b6b8fc898f8fb47602387d82e:
// cursor-framing, cursor-protobuf-events, cursor-eof-terminal, cursor-errors,
// cursor-tool-continuation, cursor-blob-integrity and cursor-http1-transport tests.
// See LICENSE.upstream. These fixtures do not establish live provider acceptance.
package cursor

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

func input(body string) inference.AdapterInput {
	return inference.AdapterInput{Principal: inference.Principal{OrganizationID: "org-a", UserID: "user-a", MembershipGeneration: 1}, Target: inference.Target{Provider: "cursor", BaseURL: "https://api2.cursor.sh", Model: "gpt-5.6", Wire: Wire, Auth: inference.OAuth}, Credential: inference.Credential{AccountID: "account-a", Secret: "fixture-only-token"}, Operation: inference.Generate, Wire: inference.Chat, Body: json.RawMessage(body)}
}

const simpleRequest = `{"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]}`

func prepared(t *testing.T) (inference.AdapterRequest, *Session) {
	t.Helper()
	r, s, e := Prepare(context.Background(), input(simpleRequest), nil)
	if e != nil {
		t.Fatal(e)
	}
	return r, s
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *inference.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error=%v, want %s", err, code)
	}
}
func interaction(kind int, body []byte) Frame { return Frame{Payload: pb(1, pb(kind, body))} }
func handle(t *testing.T, s *Session, f Frame) ([]Event, [][]byte) {
	t.Helper()
	e, r, err := s.Handle(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	return e, r
}
func toolUpdate(kind int, id, name, cumulative string, values map[string]any) Frame {
	args := join(ps(1, name), ps(3, id), ps(4, toolProvider), ps(5, name))
	for k, v := range values {
		b, _ := encodeValue(v, 0)
		args = append(args, pb(2, join(ps(1, k), pb(2, b)))...)
	}
	u := ps(1, id)
	if name != "" {
		u = append(u, pb(2, pb(15, pb(1, args)))...)
	}
	if kind == 7 {
		u = append(u, ps(3, cumulative)...)
	}
	return interaction(kind, u)
}
func serialized(t *testing.T, frames ...Frame) []byte {
	t.Helper()
	var b []byte
	for _, f := range frames {
		v, e := EncodeFrame(f)
		if e != nil {
			t.Fatal(e)
		}
		b = append(b, v...)
	}
	return b
}

type fragmented struct{ io.Reader }

func (f fragmented) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return f.Reader.Read(p)
}

func TestGoldenFramingAndFragmentation(t *testing.T) {
	// Independent literal: AgentServerMessage.interaction_update(1),
	// InteractionUpdate.text_delta(1), TextDeltaUpdate.text(1), "hi".
	golden, _ := hex.DecodeString("00000000080a060a040a026869")
	f, e := ReadFrame(fragmented{bytes.NewReader(golden)})
	if e != nil {
		t.Fatal(e)
	}
	s := newSession("fixture", nil)
	events, _ := handle(t, s, f)
	if len(events) != 1 || events[0].Text != "hi" {
		t.Fatalf("events=%+v", events)
	}
	b, e := EncodeFrame(f)
	if e != nil || !bytes.Equal(b, golden) {
		t.Fatal("frame golden differs")
	}
	for i := 1; i < len(golden); i++ {
		_, err := ReadFrame(bytes.NewReader(golden[:i]))
		if err == nil {
			t.Fatalf("accepted truncation at %d", i)
		}
	}
	_, err := ReadFrame(bytes.NewReader([]byte{0, 1, 0, 0, 1}))
	requireCode(t, err, "cursor_frame_too_large")
	_, err = ReadFrame(bytes.NewReader([]byte{8, 0, 0, 0, 0}))
	requireCode(t, err, "cursor_invalid_frame_flags")
}

func TestGzipAndExpansionLimit(t *testing.T) {
	compress := func(p []byte) []byte {
		var buf bytes.Buffer
		z := gzip.NewWriter(&buf)
		z.Write(p)
		z.Close()
		header := []byte{1, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(header[1:], uint32(buf.Len()))
		return append(header, buf.Bytes()...)
	}
	f, err := ReadFrame(bytes.NewReader(compress([]byte("payload"))))
	if err != nil || string(f.Payload) != "payload" {
		t.Fatalf("gzip: %v", err)
	}
	_, err = ReadFrame(bytes.NewReader(compress(bytes.Repeat([]byte{'x'}, MaxFrameBytes+1))))
	requireCode(t, err, "cursor_frame_too_large")
}

func TestProtobufRejectsMalformedAndSkipsUnknown(t *testing.T) {
	for _, b := range [][]byte{{0}, {0x80}, {0x0a, 0xff}, {0x0b}, {0x09, 0x01}, {0x08, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}} {
		if _, e := parse(b); e == nil {
			t.Fatalf("accepted %x", b)
		}
	}
	m, e := parse(join(ps(100, "future"), pv(1, 7)))
	if e != nil || m.get(1).value != 7 {
		t.Fatal("unknown field skip")
	}
}

func TestBuildWireAndSchema(t *testing.T) {
	r, s := prepared(t)
	if r.URL != "https://api2.cursor.sh"+RunPath || r.ResponseWire != inference.Chat || r.Headers.Get("Content-Type") != "application/connect+proto" || r.Headers.Get("X-Cursor-Client-Version") != ClientVersion {
		t.Fatalf("request metadata=%+v", r.Headers)
	}
	f, e := ReadFrame(bytes.NewReader(r.Body))
	if e != nil {
		t.Fatal(e)
	}
	m, _ := parse(f.Payload)
	run, _ := m.sub(1)
	details, _ := run.sub(3)
	if details.str(1) != "gpt-5.6" || run.get(4).number == 0 || run.str(5) == "" {
		t.Fatal("missing run fields")
	}
	cat, _ := run.sub(4)
	def, _ := cat.sub(1)
	schema, e := decodeValue(def.get(3).data, 0)
	if e != nil || object(schema)["type"] != "object" || def.str(1) != "ocx_client_weather" || def.str(4) != toolProvider {
		t.Fatalf("tool definition=%v %v", schema, e)
	}
	if s.tools["ocx_client_weather"].Name != "weather" {
		t.Fatal("tool mapping")
	}
	if bytes.Contains(r.Body, []byte("fixture-only-token")) {
		t.Fatal("credential in protobuf")
	}
}

func TestToolChoiceAndRequestedModel(t *testing.T) {
	in := input(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"weather"}}],"tool_choice":"none","cursor_max_mode":true,"cursor_model_parameters":{"effort":"high"}}`)
	r, _, e := Prepare(context.Background(), in, nil)
	if e != nil {
		t.Fatal(e)
	}
	f, _ := ReadFrame(bytes.NewReader(r.Body))
	m, _ := parse(f.Payload)
	run, _ := m.sub(1)
	requested, _ := run.sub(9)
	if run.get(3).number != 0 || requested.get(2).value != 1 || run.get(4).number != 4 || len(run.get(4).data) != 0 {
		t.Fatal("parameterized request must omit legacy model details and explicitly suppress tools")
	}
}

func TestHistoryAndKV(t *testing.T) {
	in := input(`{"messages":[{"role":"system","content":"system fixture"},{"role":"user","content":"first"},{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Seoul\"}"}}]},{"role":"tool","tool_call_id":"c1","content":"sunny"}],"tools":[{"type":"function","function":{"name":"weather"}}]}`)
	r, s, e := Prepare(context.Background(), in, nil)
	if e != nil {
		t.Fatal(e)
	}
	f, _ := ReadFrame(bytes.NewReader(r.Body))
	m, _ := parse(f.Payload)
	run, _ := m.sub(1)
	state, _ := run.sub(1)
	if len(state) != 3 {
		t.Fatalf("expected system,user,result roots: %d", len(state))
	}
	for _, root := range state {
		_, replies := handle(t, s, Frame{Payload: pb(4, join(pv(1, 9), pb(2, pb(1, root.data))))})
		if len(replies) != 1 {
			t.Fatal("missing KV response")
		}
		reply, _ := parse(replies[0])
		kv, _ := reply.sub(3)
		result, _ := kv.sub(2)
		if kv.get(1).value != 9 || !json.Valid(result.get(1).data) {
			t.Fatal("bad KV correlation/root JSON")
		}
	}
	action, _ := run.sub(2)
	userAction, _ := action.sub(1)
	user, _ := userAction.sub(1)
	if !strings.Contains(user.str(1), continuationText) {
		t.Fatal("tool results did not use external continuation action")
	}
	all := ""
	for _, b := range s.blobs {
		all += string(b)
	}
	if !strings.Contains(all, "[Tool Result]") || !strings.Contains(all, "sunny") || strings.Contains(all, "[Tool Call]") {
		t.Fatal("tool history projection")
	}
	_, _, e = s.Handle(context.Background(), Frame{Payload: pb(4, join(pv(1, 10), pb(2, pb(1, []byte("missing")))))})
	requireCode(t, e, "continuation_unavailable")
}

func TestBlobIntegrityAndIsolation(t *testing.T) {
	_, s := prepared(t)
	_, other := prepared(t)
	b := []byte("owned fixture")
	hash := sha256.Sum256(b)
	_, replies := handle(t, s, Frame{Payload: pb(4, join(pv(1, 2), pb(3, join(pb(1, hash[:]), pb(2, b)))))})
	if len(replies) != 1 {
		t.Fatal("no set ack")
	}
	_, _, err := other.Handle(context.Background(), Frame{Payload: pb(4, join(pv(1, 3), pb(2, pb(1, hash[:]))))})
	requireCode(t, err, "continuation_unavailable")
	_, _, err = s.Handle(context.Background(), Frame{Payload: pb(4, join(pv(1, 4), pb(3, join(pb(1, hash[:]), pb(2, []byte("changed"))))))})
	requireCode(t, err, "cursor_blob_digest_mismatch")
}

func TestParallelToolsAreAtomicAndCumulative(t *testing.T) {
	_, s := prepared(t)
	for _, id := range []string{"a", "b"} {
		e, _ := handle(t, s, toolUpdate(2, id, "ocx_client_weather", "", nil))
		if len(e) != 0 {
			t.Fatal("premature tool start")
		}
	}
	handle(t, s, toolUpdate(7, "a", "ocx_client_weather", `{"city":"Se`, nil))
	handle(t, s, toolUpdate(7, "b", "ocx_client_weather", `{"city":"Tokyo"}`, nil))
	handle(t, s, toolUpdate(7, "a", "ocx_client_weather", `{"city":"Seoul"}`, nil))
	handle(t, s, toolUpdate(7, "a", "ocx_client_weather", `{`, nil))
	e, _ := handle(t, s, toolUpdate(3, "b", "", "", nil))
	if len(e) != 1 || e[0].ID != "b" || e[0].Arguments != `{"city":"Tokyo"}` {
		t.Fatalf("b=%+v", e)
	}
	e, _ = handle(t, s, toolUpdate(3, "a", "", "", nil))
	if len(e) != 1 || e[0].Arguments != `{"city":"Seoul"}` || e[0].Name != "weather" {
		t.Fatalf("a=%+v", e)
	}
	e, _ = handle(t, s, toolUpdate(3, "a", "", "", nil))
	if len(e) != 0 {
		t.Fatal("duplicate tool emission")
	}
}

func TestNativeArgsResolveEmptyCompletionAndToolResult(t *testing.T) {
	_, s := prepared(t)
	id := "call-a\nfc-a"
	handle(t, s, toolUpdate(2, id, "ocx_client_weather", "", nil))
	e, _ := handle(t, s, toolUpdate(3, id, "ocx_client_weather", "", nil))
	if len(e) != 0 {
		t.Fatal("empty completion committed")
	}
	// The pin also accepts JSON-encoded UTF-8 argument values, not only Value.
	args := join(ps(1, "ocx_client_weather"), ps(3, id), pb(2, join(ps(1, "city"), pb(2, []byte(`"Seoul"`)))))
	e, _ = handle(t, s, Frame{Payload: pb(2, join(pv(1, 7), ps(15, "exec-a"), pb(11, args)))})
	if len(e) != 1 || strings.ContainsAny(e[0].ID, "\r\n") || e[0].Arguments != `{"city":"Seoul"}` {
		t.Fatalf("events=%+v", e)
	}
	r, err := s.ToolResult(context.Background(), e[0].ID, "sunny", false)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := parse(r)
	exec, _ := m.sub(2)
	result, _ := exec.sub(11)
	success, _ := result.sub(1)
	if exec.get(1).value != 7 || exec.str(15) != "exec-a" || len(success) == 0 {
		t.Fatal("result correlation lost")
	}
	_, err = s.ToolResult(context.Background(), e[0].ID, "again", false)
	requireCode(t, err, "cursor_unpaired_tool_result")
}

func TestNoArgsUnknownAndNativeExecution(t *testing.T) {
	_, s := prepared(t)
	e, _ := handle(t, s, toolUpdate(3, "a", "ocx_client_weather", "", nil))
	if len(e) != 1 || e[0].Arguments != "{}" {
		t.Fatal("new advertised no-arg tool dropped")
	}
	_, s = prepared(t)
	_, _, err := s.Handle(context.Background(), toolUpdate(2, "a", "undeclared", "", nil))
	requireCode(t, err, "cursor_unknown_tool")
	_, s = prepared(t)
	_, _, err = s.Handle(context.Background(), Frame{Payload: pb(2, join(pv(1, 1), pb(2, ps(1, "must never run"))))})
	requireCode(t, err, "cursor_native_execution_denied")
	_, s = prepared(t)
	_, replies := handle(t, s, Frame{Payload: pb(2, join(pv(1, 4), pb(10, nil)))})
	if len(replies) != 1 || bytes.Contains(replies[0], []byte("/Users/")) {
		t.Fatal("context reply")
	}
}

func TestUsageDoesNotInventInputBilling(t *testing.T) {
	_, s := prepared(t)
	handle(t, s, interaction(8, pv(1, 5)))
	handle(t, s, interaction(8, pv(1, 3)))
	handle(t, s, Frame{Payload: pb(3, pb(5, join(pv(1, 1000), pv(2, 2000))))})
	handle(t, s, interaction(14, nil))
	e, _ := handle(t, s, Frame{Flags: 2, Payload: []byte(`{}`)})
	if len(e) != 1 || e[0].Usage.InputTokens != nil || *e[0].Usage.OutputTokens != 8 || *e[0].ContextTokens != 1000 {
		t.Fatalf("usage=%+v", e)
	}
	_, s = prepared(t)
	handle(t, s, interaction(1, ps(1, "hi")))
	e, _ = handle(t, s, Frame{Flags: 2, Payload: []byte(`{}`)})
	if e[0].Usage.OutputTokens != nil || e[0].Usage.InputTokens != nil {
		t.Fatal("unknown reported as zero")
	}
	_, s = prepared(t)
	handle(t, s, interaction(8, nil))
	handle(t, s, interaction(14, nil))
	e, _ = handle(t, s, Frame{Flags: 2, Payload: []byte(`{}`)})
	if e[0].Usage.OutputTokens == nil || *e[0].Usage.OutputTokens != 0 {
		t.Fatal("explicit zero lost")
	}
}

func TestTerminalFailures(t *testing.T) {
	_, s := prepared(t)
	handle(t, s, toolUpdate(2, "a", "ocx_client_weather", "", nil))
	_, err := s.EOF()
	requireCode(t, err, "cursor_incomplete_tool_call")
	_, s = prepared(t)
	handle(t, s, interaction(1, ps(1, "partial")))
	_, err = s.EOF()
	requireCode(t, err, "cursor_stream_truncated")
	_, s = prepared(t)
	handle(t, s, interaction(14, nil))
	_, _, err = s.Handle(context.Background(), Frame{Flags: 2, Payload: []byte(`{"error":{"code":"unavailable","message":"private payload"}}`)})
	requireCode(t, err, "cursor_server_overloaded")
	_, s = prepared(t)
	_, _, err = s.Handle(context.Background(), Frame{Flags: 2, Payload: []byte(`{}`)})
	requireCode(t, err, "cursor_stream_truncated")
}

func TestContinuationRoundTripAndOwnerChecks(t *testing.T) {
	in := input(`{"messages":[{"role":"system","content":"fixture rule"},{"role":"user","content":"hello"}]}`)
	r, s, err := Prepare(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := ReadFrame(bytes.NewReader(r.Body))
	m, _ := parse(f.Payload)
	run, _ := m.sub(1)
	state := append([]byte(nil), run.get(1).data...)
	state = append(state, pb(5, pv(1, 100))...)
	handle(t, s, Frame{Payload: pb(3, state)})
	saved, err := s.Snapshot(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Simulate another process loading encrypted SQL state after decryption.
	b, _ := json.Marshal(saved)
	var loaded Continuation
	if err = json.Unmarshal(b, &loaded); err != nil {
		t.Fatal(err)
	}
	in.Body = json.RawMessage(`{"messages":[{"role":"system","content":"fixture rule"},{"role":"user","content":"hello"},{"role":"assistant","content":"answer"},{"role":"user","content":"next"}]}`)
	r, resumed, err := Prepare(context.Background(), in, &loaded)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.conversationID != s.conversationID {
		t.Fatal("conversation reminted")
	}
	f, _ = ReadFrame(bytes.NewReader(r.Body))
	m, _ = parse(f.Payload)
	run, _ = m.sub(1)
	cs, _ := run.sub(1)
	roots := 0
	for _, f := range cs {
		if f.number == 1 {
			roots++
		}
	}
	if roots != 2 {
		t.Fatalf("duplicated checkpoint prefix: %d", roots)
	}
	for _, change := range []func(*inference.AdapterInput){func(i *inference.AdapterInput) { i.Principal.OrganizationID = "org-b" }, func(i *inference.AdapterInput) { i.Principal.UserID = "user-b" }, func(i *inference.AdapterInput) { i.Principal.MembershipGeneration++ }, func(i *inference.AdapterInput) { i.Credential.Secret = "other" }, func(i *inference.AdapterInput) { i.Target.Model = "other" }, func(i *inference.AdapterInput) { i.Target.BaseURL = "https://other.test" }} {
		other := in
		change(&other)
		_, _, err := Prepare(context.Background(), other, &loaded)
		requireCode(t, err, "continuation_unavailable")
	}
	loaded.ExpiresAt = time.Now().Add(-time.Second)
	_, _, err = Prepare(context.Background(), in, &loaded)
	requireCode(t, err, "continuation_unavailable")
}

func TestHTTP1AppendGolden(t *testing.T) {
	// data="0102", request_id={request_id:"r"}, append_seqno=1.
	b, e := EncodeBidiAppend([]byte{1, 2}, "r", 1)
	if e != nil {
		t.Fatal(e)
	}
	if hex.EncodeToString(b) != "0a043031303212030a01721801" {
		t.Fatalf("append=%x", b)
	}
	b, e = BidiRunBody("r")
	if e != nil || hex.EncodeToString(b) != "00000000030a0172" {
		t.Fatalf("run=%x %v", b, e)
	}
}

func TestErrorClassificationAndRetryEvidence(t *testing.T) {
	for _, tc := range []struct {
		code, detail, want string
		status             int
		size               *SizeContext
	}{
		{"resource_exhausted", "Error", "cursor_context_limit_exceeded", 400, nil},
		{"resource_exhausted", "Error", "cursor_rate_limit_exceeded", 429, &SizeContext{20, 200000}},
		{"resource_exhausted", "quota exhausted", "cursor_rate_limit_exceeded", 429, nil},
		{"resource_exhausted", "tool registration too large", "cursor_resource_limit_exceeded", 400, nil},
		{"resource_exhausted", "too many tools", "cursor_resource_limit_exceeded", 400, nil},
		{"resource_exhausted", "request body exceeds maximum allowed limit", "cursor_resource_limit_exceeded", 400, nil},
		{"resource_exhausted", "request exceeds concurrent request limit", "cursor_rate_limit_exceeded", 429, nil},
		{"failed_precondition", "model unavailable for this plan", "cursor_invalid_request", 400, nil},
		{"unauthenticated", "authorization=secret /Users/private", "cursor_authentication_failed", 401, nil},
		{"cancelled", "server cancelled", "cursor_unexpected_cancel", 502, nil},
	} {
		e := ClassifyError(tc.code, tc.detail, tc.size)
		if e.Code != tc.want || e.Status != tc.status {
			t.Fatalf("%s: %+v", tc.detail, e)
		}
	}
	no := false
	yes := true
	if _, ok := RetryDelay(1, nil, false, "ECONNRESET"); ok {
		t.Fatal("unknown commitment retried")
	}
	if _, ok := RetryDelay(1, &yes, false, "ECONNRESET"); ok {
		t.Fatal("committed request retried")
	}
	if _, ok := RetryDelay(1, &no, true, "ECONNRESET"); ok {
		t.Fatal("visible request retried")
	}
	if delay, ok := RetryDelay(2, &no, false, "ECONNRESET"); !ok || delay != 500*time.Millisecond {
		t.Fatal("precommit retry missing")
	}
	if _, ok := RetryDelay(3, &no, false, "ECONNRESET"); ok {
		t.Fatal("budget exceeded")
	}
	if _, ok := RetryDelay(1, &no, false, "NGHTTP2_CANCEL"); ok {
		t.Fatal("cancel retried")
	}
}

func mockResponse(t *testing.T, body io.ReadCloser) *http.Response {
	t.Helper()
	r, _ := prepared(t)
	req, e := http.NewRequest("POST", r.URL, bytes.NewReader(r.Body))
	if e != nil {
		t.Fatal(e)
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/connect+proto"}}, Body: body, Request: req}
}

func TestDecodeSSEAndTerminalError(t *testing.T) {
	for _, success := range []bool{true, false} {
		frames := []Frame{interaction(1, ps(1, "hello")), interaction(8, pv(1, 3))}
		if success {
			frames = append(frames, interaction(14, nil), Frame{Flags: 2, Payload: []byte(`{}`)})
		}
		r, e := New().Decode(context.Background(), mockResponse(t, io.NopCloser(bytes.NewReader(serialized(t, frames...)))))
		if e != nil {
			t.Fatal(e)
		}
		b, err := io.ReadAll(r.Body)
		r.Body.Close()
		if !bytes.Contains(b, []byte(`"completion_tokens":3`)) {
			t.Fatal("confirmed usage was lost before terminal status")
		}
		if success {
			if err != nil || !bytes.Contains(b, []byte("[DONE]")) || bytes.Contains(b, []byte("prompt_tokens")) {
				t.Fatalf("success: %s %v", b, err)
			}
		} else {
			if err == nil || bytes.Contains(b, []byte("[DONE]")) || !bytes.Contains(b, []byte("cursor_stream_truncated")) {
				t.Fatalf("failure: %s %v", b, err)
			}
		}
	}
}

func TestFreeformAndMalformedArguments(t *testing.T) {
	in := input(`{"input":"apply the patch","tools":[{"type":"custom","name":"apply_patch"}]}`)
	in.Wire = inference.Responses
	_, s, err := Prepare(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := handle(t, s, toolUpdate(3, "a", "apply_patch", "", map[string]any{"input": "{}"}))
	if len(e) != 1 || e[0].Arguments != `{"input":"{}"}` {
		t.Fatalf("freeform string reparsed: %+v", e)
	}
	_, s, err = Prepare(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	handle(t, s, toolUpdate(2, "a", "apply_patch", "", nil))
	handle(t, s, toolUpdate(7, "a", "apply_patch", `{"input":`, nil))
	_, _, err = s.Handle(context.Background(), interaction(14, nil))
	requireCode(t, err, "cursor_incomplete_tool_call")
	_, s = prepared(t)
	handle(t, s, toolUpdate(2, "a", "ocx_client_weather", "", nil))
	handle(t, s, toolUpdate(7, "a", "ocx_client_weather", `{broken`, nil))
	_, _, err = s.Handle(context.Background(), toolUpdate(3, "a", "", "", nil))
	requireCode(t, err, "cursor_invalid_tool_arguments")
}

func TestJSONValueRoundTripAndDepth(t *testing.T) {
	want := map[string]any{"null": nil, "bool": true, "number": float64(7), "array": []any{"x", false, map[string]any{"key": "value"}}}
	b, err := encodeValue(want, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeValue(b, 0)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(want)
	right, _ := json.Marshal(got)
	if !bytes.Equal(left, right) {
		t.Fatalf("roundtrip %s", right)
	}
	for i := 0; i < 34; i++ {
		b = pb(6, pb(1, b))
	}
	_, err = decodeValue(b, 0)
	requireCode(t, err, "cursor_argument_depth")
}

func TestTextToolMarkersAreDeferredAndRealCallsWin(t *testing.T) {
	for split := 0; split <= len("[TOOL_CALL]"); split++ {
		_, s := prepared(t)
		marker := `[TOOL_CALL]mcp_opencodex-responses_ocx_client_weather[ARGS]{"city":"Seoul"}`
		var text string
		for _, part := range []string{"before " + marker[:split], marker[split:] + " after"} {
			events, _ := handle(t, s, interaction(1, ps(1, part)))
			for _, event := range events {
				if event.Kind != Text {
					t.Fatal("premature fallback")
				}
				text += event.Text
			}
		}
		if text != "before  after" {
			t.Fatalf("marker leaked at split %d: %q", split, text)
		}
		e, _ := handle(t, s, Frame{Flags: 2, Payload: []byte(`{}`)})
		if len(e) != 2 || e[0].Kind != ToolCall || e[0].Name != "weather" || e[1].Kind != Done {
			t.Fatalf("fallback=%+v", e)
		}
	}
	_, s := prepared(t)
	handle(t, s, interaction(1, ps(1, `[TOOL_CALL]ocx_client_weather[ARGS]{"city":"stale"}`)))
	e, _ := handle(t, s, toolUpdate(3, "real", "ocx_client_weather", "", map[string]any{"city": "real"}))
	if len(e) != 1 {
		t.Fatal("missing real call")
	}
	handle(t, s, interaction(14, nil))
	e, _ = handle(t, s, Frame{Flags: 2, Payload: []byte(`{}`)})
	if len(e) != 1 || e[0].Kind != Done {
		t.Fatal("text fallback duplicated real call")
	}
}

func TestTextMarkerBoundsAndUndeclaredNames(t *testing.T) {
	_, s := prepared(t)
	handle(t, s, interaction(1, ps(1, `safe [TOOL_CALL]undeclared[ARGS]{"secret":"fixture"} tail`)))
	e, _ := handle(t, s, Frame{Flags: 2, Payload: []byte(`{}`)})
	if len(e) != 1 || e[0].Kind != Done {
		t.Fatal("undeclared fallback promoted")
	}
	_, s = prepared(t)
	_, _, err := s.Handle(context.Background(), interaction(1, ps(1, "[TOOL_CALL]"+strings.Repeat("x", maxPendingText))))
	requireCode(t, err, "cursor_text_tool_call_limit")
}

func TestSnapshotCorruptionAndPrefixMismatch(t *testing.T) {
	_, s := prepared(t)
	handle(t, s, Frame{Payload: pb(3, pb(5, pv(1, 9)))})
	c, err := s.Snapshot(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	in := input(`{"messages":[{"role":"user","content":"different history"}]}`)
	_, _, err = Prepare(context.Background(), in, c)
	requireCode(t, err, "continuation_unavailable")
	c.Blobs["invalid-digest"] = []byte("content")
	_, _, err = Prepare(context.Background(), input(simpleRequest), c)
	requireCode(t, err, "continuation_unavailable")
}

func TestDecodeCancellationClosesUpstream(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	r, err := New().Decode(ctx, mockResponse(t, reader))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := io.ReadAll(r.Body); done <- e }()
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("cancellation succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not unblock reader")
	}
	if _, err = writer.Write([]byte{0}); err == nil {
		t.Fatal("upstream remained open")
	}
	r.Body.Close()
}

func TestDecodeNeedsDuplexAndNoNativeExecution(t *testing.T) {
	frames := []Frame{{Payload: pb(2, join(pv(1, 1), pb(10, nil)))}}
	r, err := New().Decode(context.Background(), mockResponse(t, io.NopCloser(bytes.NewReader(serialized(t, frames...)))))
	if err != nil {
		t.Fatal(err)
	}
	b, e := io.ReadAll(r.Body)
	r.Body.Close()
	if e == nil || !bytes.Contains(b, []byte("cursor_duplex_executor_required")) {
		t.Fatalf("missing seam error: %s %v", b, e)
	}
}

func TestInputWireProjectionAndLimits(t *testing.T) {
	for _, tc := range []struct {
		wire inference.Wire
		body string
	}{
		{inference.Responses, `{"input":"hello","instructions":"rule"}`},
		{inference.Anthropic, `{"system":"rule","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`},
	} {
		in := input(tc.body)
		in.Wire = tc.wire
		if _, _, e := Prepare(context.Background(), in, nil); e != nil {
			t.Fatal(e)
		}
	}
	in := input(`{"messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"unused","format":"wav"}}]}]}`)
	_, _, err := Prepare(context.Background(), in, nil)
	requireCode(t, err, "cursor_unsupported_content")
	in = input(`{"messages":[{"role":"user","content":"hi"}],"previous_response_id":"missing"}`)
	_, _, err = Prepare(context.Background(), in, nil)
	requireCode(t, err, "continuation_unavailable")
	in = input(`{"messages":[{"role":"system","content":"` + strings.Repeat("x", MaxRootBytes) + `"},{"role":"user","content":"hi"}]}`)
	_, _, err = Prepare(context.Background(), in, nil)
	requireCode(t, err, "cursor_root_envelope_limit")
}
