// SPDX-License-Identifier: Apache-2.0
package devin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

func toolInput(t *testing.T, choice any) object {
	t.Helper()
	var m object
	if err := json.Unmarshal([]byte(`{"tools":[{"type":"namespace","name":"mcp__cua","tools":[{"type":"function","name":"js","parameters":{"type":"object"}}]},{"type":"custom","name":"apply_patch","description":"Patch"},{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec"}]}]}`), &m); err != nil {
		t.Fatal(err)
	}
	m["tool_choice"] = choice
	return m
}

func TestToolChoiceAndFreeformFixtures(t *testing.T) {
	for _, tc := range []struct {
		choice   any
		count    int
		required bool
	}{{"required", 3, true}, {"none", 0, false}, {object{"type": "function", "name": "mcp__cua__js"}, 1, true}, {object{"type": "function", "function": object{"name": "js"}}, 1, true}, {object{"type": "tool", "name": "js"}, 1, true}, {object{"type": "allowed_tools", "mode": "required", "tools": []any{object{"type": "custom", "name": "exec"}, object{"name": "apply_patch"}}}, 2, true}} {
		m := toolInput(t, tc.choice)
		plan, err := planTools(m)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.tools) != tc.count || plan.RequiredTool != tc.required {
			t.Fatalf("%v: %+v", tc.choice, plan)
		}
		if _, err := buildTools(m, "swe-2-high"); err != nil {
			t.Fatal(err)
		}
	}
	m := toolInput(t, "auto")
	plan, err := planTools(m)
	if err != nil {
		t.Fatal(err)
	}
	if name, err := plan.RestoreToolName("js"); err != nil || name != "mcp__cua__js" {
		t.Fatalf("restore %s %v", name, err)
	}
	if !strings.Contains(plan.nudge(), "JavaScript evaluated in a V8 isolate") || strings.Contains(plan.nudge(), "`mcp__cua__js`") {
		t.Fatal(plan.nudge())
	}
	encoded, err := buildTools(m, "swe-2-high")
	if err != nil {
		t.Fatal(err)
	}
	var schema object
	if err := json.Unmarshal(fields(t, encoded[1])[3][0].b, &schema); err != nil {
		t.Fatal(err)
	}
	if str(obj(obj(schema["properties"])["input"])["type"]) != "string" || !strings.Contains(str(obj(obj(schema["properties"])["input"])["description"]), "*** Begin Patch") {
		t.Fatal(schema)
	}
	if _, err := plan.ProjectToolCall("exec", `{"input":"const x = 1;"}`); err != nil {
		t.Fatal(err)
	}
	projected, err := plan.ProjectToolCall("apply_patch", `{"input":"*** Begin Patch\n*** End Patch"}`)
	if err != nil || projected.Kind != CustomTool || projected.Input != "*** Begin Patch\n*** End Patch" {
		t.Fatalf("projection %+v %v", projected, err)
	}
	if _, err := plan.ProjectToolCall("exec", `{"input":42}`); err == nil {
		t.Fatal("invalid freeform input accepted")
	}
	if _, err := plan.ProjectToolCall("undeclared", `{}`); err == nil {
		t.Fatal("undeclared accepted")
	}
}

func TestAliasCollisionAndRequestIsolation(t *testing.T) {
	var m object
	_ = json.Unmarshal([]byte(`{"tools":[{"type":"namespace","name":"a","tools":[{"type":"function","name":"x"}]},{"type":"namespace","name":"b","tools":[{"type":"function","name":"a__x"}]}]}`), &m)
	plan, err := planTools(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.RestoreToolName("a__x"); err == nil {
		t.Fatal("canonical/local alias collision accepted")
	}
	if got, err := plan.RestoreToolName("x"); err != nil || got != "a__x" {
		t.Fatal(got, err)
	}
	m["tool_choice"] = object{"type": "function", "name": "a__x"}
	if _, err := planTools(m); err == nil {
		t.Fatal("ambiguous selector accepted")
	}
	first, _ := planTools(toolInput(t, "auto"))
	second, _ := planTools(object{"tools": []any{object{"type": "namespace", "name": "another", "tools": []any{object{"type": "function", "name": "js"}}}}})
	a, _ := first.RestoreToolName("js")
	b, _ := second.RestoreToolName("js")
	if a == b {
		t.Fatal("request state shared")
	}
}

func TestPreparedNamesHistoryAndMessagesOrder(t *testing.T) {
	m := toolInput(t, "required")
	m["input"] = []any{object{"type": "message", "role": "user", "content": "hi"}, object{"type": "custom_tool_call", "call_id": "custom", "name": "exec", "input": "let a = 1;"}, object{"type": "custom_tool_call_output", "call_id": "custom", "output": "ok"}, object{"type": "function_call", "call_id": "js", "name": "mcp__cua__js", "arguments": "{}"}}
	body, _ := json.Marshal(m)
	in := input(string(body))
	in.Wire = inference.Responses
	req, state, err := New().BuildPrepared(context.Background(), in, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, proto, err := readFrame(bytes.NewReader(req.Body))
	if err != nil {
		t.Fatal(err)
	}
	prompts := fields(t, proto)[3]
	custom := fields(t, fields(t, prompts[1].b)[6][0].b)
	if string(custom[3][0].b) != `{"input":"let a = 1;"}` {
		t.Fatal(string(custom[3][0].b))
	}
	last := fields(t, fields(t, prompts[len(prompts)-1].b)[6][0].b)
	if string(last[2][0].b) != "js" {
		t.Fatal("history was not lowered")
	}
	wire := append(frame(textField(nil, 3, "visible")), frame(blob(nil, 6, toolCall("call", "js", "{}")))...)
	wire = append(wire, frame(textField(textField(nil, 10, "sig"), 21, "anthropic"))...)
	wire = append(wire, eos(`{}`)...)
	read := func(state *ResponseContext) string {
		t.Helper()
		resp, err := New().DecodePrepared(context.Background(), &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(wire))}, state)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	responses := read(state)
	if strings.Index(responses, "visible") > strings.Index(responses, "devin-sig1:") {
		t.Fatal("Responses reordered")
	}
	if !strings.Contains(responses, "mcp__cua__js") {
		t.Fatal("name not restored")
	}
	copyState := *state
	copyState.Inbound = inference.Anthropic
	messages := read(&copyState)
	if strings.Index(messages, "devin-sig1:") > strings.Index(messages, "visible") {
		t.Fatal("Messages signature remained last")
	}
	if DecodeSignature(EncodeSignature(Signature{Value: "abc:123", Type: "anthropic"})) != (Signature{Value: "abc:123", Type: "anthropic"}) {
		t.Fatal("typed signature roundtrip")
	}
}

// Family fixtures mirror devin-family-resolution.test.ts at the pinned revision.
func familyFixture() Catalog {
	return Catalog{
		{UID: "swe-1-7", Family: "swe-1.7", Axes: map[string]Axis{"Reasoning Effort": {Order: 5, Name: "Max"}}, MaxOutputTokens: 128000},
		{UID: "swe-1-7-medium", Family: "swe-1.7", Default: true, Axes: map[string]Axis{"Reasoning Effort": {Order: 2, Name: "Medium"}}, MaxOutputTokens: 128000},
		{UID: "swe-2-medium", Family: "swe-2", Axes: map[string]Axis{"Reasoning Effort": {Order: 0, Name: "Medium"}}},
		{UID: "swe-2-high", Family: "swe-2", Default: true, Axes: map[string]Axis{"Reasoning Effort": {Order: 1, Name: "High"}}, ContextWindow: 262000, MaxOutputTokens: 128000},
		{UID: "swe-2-max", Family: "swe-2", Axes: map[string]Axis{"Reasoning Effort": {Order: 2, Name: "Max"}}},
		{UID: "claude-medium-fast", Family: "claude", Default: true, Axes: map[string]Axis{"Effort": {Order: 1, Name: "Medium"}, "Fast Mode": {Order: 1}}},
		{UID: "claude-high", Family: "claude", Axes: map[string]Axis{"Effort": {Order: 2, Name: "High"}, "Fast Mode": {Order: 0}}},
		{UID: "glm-5-2", Family: "glm-5.2", Default: true, Axes: map[string]Axis{"Effort": {Order: 1, Name: "High"}, "1M Context": {Order: 0}}},
		{UID: "glm-5-2-max-1m", Family: "glm-5.2", Axes: map[string]Axis{"Effort": {Order: 2, Name: "Max"}, "1M Context": {Order: 1}}},
	}
}
func TestCatalogResolutionAndCaps(t *testing.T) {
	catalog := familyFixture()
	for _, tc := range []struct{ model, effort, want string }{{"swe-1.7", "", "swe-1-7-medium"}, {"swe-1-7", "max", "swe-1-7"}, {"swe-1-7-high", "", "swe-1-7"}, {"swe-2", "", "swe-2-high"}, {"swe-2-high", "low", "swe-2-medium"}, {"swe-2", "xhigh", "swe-2-max"}, {"claude", "high", "claude-high"}, {"claude-medium-fast", "high", "claude-high"}, {"glm-5.2", "max-1m", "glm-5-2-max-1m"}} {
		m, err := ResolveModel(catalog, tc.model, tc.effort)
		if err != nil || m.UID != tc.want {
			t.Fatalf("%s/%s: %+v %v", tc.model, tc.effort, m, err)
		}
	}
	catalog[4].Disabled = true
	if _, err := ResolveModel(catalog, "swe-2-max", "max"); err == nil {
		t.Fatal("disabled explicit selection changed tier")
	}
	if _, err := ResolveModel(catalog, "absent", ""); err == nil {
		t.Fatal("unknown model accepted")
	}
	m, err := ResolveModel(catalog, "swe-2", "")
	if err != nil {
		t.Fatal(err)
	}
	caps := ResolveCaps(Options{ModelMaxOutputTokens: map[string]int64{"SWE.2.HIGH": 64000}, ModelContextWindows: map[string]int64{"swe-2": 200000}, ModelMaxInputTokens: map[string]int64{"swe-2": 100000}, DefaultMaxOutputTokens: 32768}, m)
	if caps.Output != 64000 || caps.Input != 100000 {
		t.Fatal(caps)
	}
	for _, tc := range []struct {
		body    string
		options Options
		want    uint64
	}{{`{"messages":[{"role":"user","content":"hi"}]}`, Options{Catalog: catalog}, 128000}, {`{"messages":[{"role":"user","content":"hi"}],"max_tokens":51}`, Options{Catalog: catalog, DefaultMaxOutputTokens: 64000}, 51}, {`{"messages":[{"role":"user","content":"hi"}]}`, Options{Catalog: catalog, DefaultMaxOutputTokens: 64000}, 64000}} {
		req, _, err := New().BuildPrepared(context.Background(), input(tc.body), tc.options)
		if err != nil {
			t.Fatal(err)
		}
		_, proto, _ := readFrame(bytes.NewReader(req.Body))
		c := fields(t, fields(t, proto)[8][0].b)
		if c[2][0].n != tc.want || c[3][0].n != 128000 {
			t.Fatal("output cap field")
		}
	}
}

func TestCatalogProtobufAndBounds(t *testing.T) {
	axis := textField(blob(textField(nil, 1, "Reasoning Effort"), 2, textField(number(nil, 1, 2), 2, "Medium")), 3, "")
	meta := number(blob(nil, 2, axis), 3, 1)
	info := textField(number(blob(nil, 6, number(nil, 15, 1)), 13, 128000), 23, "swe-1.7")
	row := blob(blob(number(textField(textField(nil, 1, "SWE"), 22, "swe-1-7-medium"), 18, 262000), 23, info), 30, meta)
	rows, err := ParseCatalog(blob(nil, 1, row))
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	m := rows[0]
	if !m.Default || m.MaxOutputTokens != 128000 || m.ContextWindow != 262000 || m.Axes["Reasoning Effort"].Name != "Medium" || m.SupportsImages != nil || m.SupportsThinking == nil || !*m.SupportsThinking {
		t.Fatal(m)
	}
	if _, err := ParseCatalog([]byte{10, 128}); err == nil {
		t.Fatal("truncated catalog accepted")
	}
	if _, err := ParseCatalog(make([]byte, maxFrame+1)); !errors.Is(err, errLimit) {
		t.Fatal(err)
	}
}

func TestPreparedFailureBoundaries(t *testing.T) {
	s := chatStream{writer: io.Discard, state: &ResponseContext{Inbound: inference.Anthropic}, heldBytes: 32 << 20}
	if err := s.emit(object{"content": "x"}, nil, nil); err == nil {
		t.Fatal("Messages retention unbounded")
	}
	plan := &ResponseContext{aliases: map[string]string{"js": ""}}
	wire := append(frame(blob(nil, 6, toolCall("c", "js", "{}"))), eos(`{}`)...)
	resp, err := New().DecodePrepared(context.Background(), &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(wire))}, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err == nil || strings.Contains(string(body), "[DONE]") {
		t.Fatal("ambiguous stream succeeded")
	}
	wire = append(frame(textField(nil, 3, "held content")), eos(`{"error":{"code":"invalid_argument"}}`)...)
	resp, err = New().DecodePrepared(context.Background(), &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(wire))}, &ResponseContext{Inbound: inference.Anthropic})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	if err == nil || strings.Contains(string(body), "held content") {
		t.Fatal("failed held output leaked")
	}
}

func TestCatalogAnchorsAndUnavailableRungs(t *testing.T) {
	rows := familyFixture()
	rows[1].Disabled = true
	m, err := ResolveModel(rows, "swe-1-7", "low")
	if err != nil || m.UID != "swe-1-7" {
		t.Fatal(m, err)
	}
	if _, err := ResolveModel(rows, "swe-1-7-medium", ""); err == nil {
		t.Fatal("explicit disabled row substituted")
	}
	rows = familyFixture()
	m, err = ResolveModel(rows, "swe-2", "high-fast")
	if err != nil || m.UID != "swe-2-high" {
		t.Fatal("invalid compound effort changed default", m, err)
	}
	m = Model{UID: "x-medium", Family: "x", MaxOutputTokens: 128000}
	if caps := ResolveCaps(Options{ContextWindow: 1000000}, m); caps.Output != 128000 {
		t.Fatal("input window became output cap")
	}
	if n := hint(map[string]int64{"X.MEDIUM": 50, "X-MEDIUM": 40}, m); n != 40 {
		t.Fatal("normalized minimum", n)
	}
}

func TestClientToolSearchAndAdditionalTools(t *testing.T) {
	body := `{"tools":[{"type":"tool_search"}],"tool_choice":{"type":"tool_search"},"input":[{"type":"additional_tools","tools":[{"type":"function","name":"discovered","parameters":{}}]},{"type":"tool_search_call","call_id":"search","arguments":{"query":"find a tool"}},{"type":"tool_search_output","call_id":"search","tools":[],"status":"completed"}]}`
	in := input(body)
	in.Wire = inference.Responses
	req, state, err := New().BuildPrepared(context.Background(), in, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, proto, err := readFrame(bytes.NewReader(req.Body))
	if err != nil {
		t.Fatal(err)
	}
	f := fields(t, proto)
	if len(f[10]) != 1 || string(fields(t, f[10][0].b)[1][0].b) != "tool_search" {
		t.Fatal("tool-search selector")
	}
	projection, err := state.ProjectToolCall("tool_search", `{"query":"find a tool"}`)
	if err != nil || projection.Kind != SearchTool {
		t.Fatal(projection, err)
	}
	if len(f[3]) != 2 || string(fields(t, f[3][1].b)[7][0].b) != "search" {
		t.Fatal("tool-search history")
	}
}
