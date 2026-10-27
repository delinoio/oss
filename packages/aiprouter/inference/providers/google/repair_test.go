// SPDX-License-Identifier: Apache-2.0
package google

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

func TestCustomToolReplayUnwrap(t *testing.T) {
	for _, tc := range []struct {
		input string
		exact bool
		want  string
	}{
		{` {"x":1} `, false, signatureA}, {`{"x":1}`, true, signatureB},
		{`{"x":1} garbage`, false, ""}, {`null`, false, ""}, {`1`, false, ""}, {strings.Repeat("a", 64<<10+1), false, ""},
	} {
		s := &requestState{}
		s.remember(replayKey("tool", object{"x": json.Number("1")}), signatureA)
		args := object{"input": tc.input}
		if tc.exact {
			s.remember(replayKey("tool", args), signatureB)
		}
		p := object{"functionCall": object{"name": "tool", "args": args}}
		s.restoreCalls(object{"contents": []any{object{"role": "model", "parts": []any{p}}}}, nil)
		if got := signature(p); got != tc.want {
			t.Fatalf("exact=%v got %q want %q", tc.exact, got, tc.want)
		}
	}
}

// Pinned google-wire-compiler.ts targets flattened declaration indices and permits
// exactly one combined schema/thinking compatibility repair in google-http.ts.
func TestRepairRequestPinnedRules(t *testing.T) {
	for _, strict := range []bool{false, true} {
		options := Options{}
		if strict {
			options.SchemaPolicy = SchemaRejectLossy
		}
		a := NewAntigravity(options)
		in := input(`{"messages":[{"role":"user","content":"hi"}]}`)
		in.Target.Auth = inference.OAuth
		in.Credential.Headers = map[string][]string{ProjectHeader: {"project"}}
		ctx := a.PrepareContext(context.Background())
		req, err := a.Build(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		req.Body = []byte(`{"model":"gemini","project":"project","request":{"generationConfig":{"thinkingConfig":{"includeThoughts":true},"temperature":0.5},"tools":[{"functionDeclarations":[{"name":"one","parameters":{"type":"object","required":["x"]}},{"name":"two","parameters":{"type":"object","required":["y"]}}]}]}}`)
		f := inference.HTTPFailure{Status: 400, Body: []byte(`functionDeclarations[1] json schema and thinking_config rejected`)}
		repaired, changed, err := a.RepairRequest(ctx, in, req, f)
		if err != nil {
			t.Fatal(err)
		}
		if changed == strict {
			t.Fatal("strict repair policy not respected")
		}
		if strict {
			if string(repaired.Body) != string(req.Body) {
				t.Fatal("strict mutated input")
			}
			continue
		}
		body, _ := decodeJSON(repaired.Body)
		root := obj(body["request"])
		if obj(root["generationConfig"])["thinkingConfig"] != nil {
			t.Fatal("thinking not removed")
		}
		decls := array(obj(array(root["tools"])[0])["functionDeclarations"])
		if obj(obj(decls[0])["parameters"])["required"] == nil || obj(obj(decls[1])["parameters"])["required"] != nil {
			t.Fatal("wrong declaration repaired")
		}
		if _, changed, err = a.RepairRequest(ctx, in, repaired, f); changed || err != nil {
			t.Fatal("duplicate repair allowed", err)
		}
		report, ok := RequestSchemaReport(ctx)
		if !ok || report.Categories["repair-opened-schema"] != 1 {
			t.Fatal("missing loss report")
		}
	}
	a := NewAPI(Options{})
	req := inference.AdapterRequest{Body: []byte(`{"generationConfig":{"thinkingConfig":{}}}`)}
	if _, changed, err := a.RepairRequest(context.Background(), input(`{}`), req, inference.HTTPFailure{Status: 400, Body: []byte("thinkingConfig")}); changed || err != nil {
		t.Fatal("AI Studio repair should be disabled", err)
	}
}

func TestRepairLogicalLimitSurvivesFreshAttempt(t *testing.T) {
	a := NewAntigravity(Options{})
	in := sessionInput()
	in.ClientHeaders = nil
	ctx := a.PrepareContext(context.Background())
	req, err := a.Build(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, err := a.RepairRequest(ctx, in, req, inference.HTTPFailure{Status: 400, Repairs: 1, Body: []byte("thinkingConfig rejected")}); changed || err != nil {
		t.Fatal("fresh context bypassed logical repair cap", err)
	}
}
