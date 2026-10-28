// SPDX-License-Identifier: Apache-2.0
package commandcode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

func profilePage(model string, efforts ...string) string {
	table := []any{"id", "reasoningEfforts", model, nil, object{"_0": 2, "_1": 3}}
	refs := []any{}
	for _, e := range efforts {
		refs = append(refs, len(table))
		table = append(table, e)
	}
	table[3] = refs
	b, _ := json.Marshal(table)
	quoted, _ := json.Marshal(string(b))
	return "window.__reactRouterContext.streamController.enqueue(" + string(quoted) + ");"
}
func TestProfileParsing(t *testing.T) {
	page := profilePage("zai-org/GLM-5.2", "high", "max")
	efforts, ok := ParseProfileEfforts([]byte(page), "zai-org/glm-5.2")
	if !ok || strings.Join(efforts, ",") != "high,max" {
		t.Fatal(efforts, ok)
	}
	for _, p := range []string{page + page, profilePage("other", "high"), profilePage("zai-org/GLM-5.2", "high", "high"), profilePage("zai-org/GLM-5.2", "ultra"), strings.Repeat("x", (512<<10)+1)} {
		if _, ok := ParseProfileEfforts([]byte(p), "zai-org/GLM-5.2"); ok {
			t.Fatal("invalid profile accepted")
		}
	}
	efforts, ok = ParseProfileEfforts([]byte("Reasoning efforts high, xhigh are supported; xhigh maps to max."), "m")
	if !ok || strings.Join(efforts, ",") != "high,max" {
		t.Fatal(efforts, ok)
	}
}
func TestProfileRepairHooks(t *testing.T) {
	in := fixtureInput(`{"messages":[],"reasoning_effort":"high"}`)
	r, e := New().Build(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	sends, learned := 0, 0
	a := NewWithOptions(Options{ProfileTransport: func(ctx context.Context, got inference.AdapterInput, req *http.Request) (*http.Response, error) {
		sends++
		if req.URL.String() != "https://commandcode.ai/models/glm-5-2" || req.Header.Get("Authorization") != "" || req.Header.Get("Accept") != "text/html" || got.Credential.AccountID != in.Credential.AccountID {
			t.Fatal("profile request escaped ownership")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(profilePage("zai-org/GLM-5.2", "high", "max")))}, nil
	}, LearnEfforts: func(_ context.Context, got inference.AdapterInput, model string, accepted, rejected []string) error {
		learned++
		if model != "zai-org/GLM-5.2" || strings.Join(accepted, ",") != "max" || strings.Join(rejected, ",") != "high" || got.Credential.AccountID != in.Credential.AccountID {
			t.Fatalf("learning mismatch %s %v %v", model, accepted, rejected)
		}
		return nil
	}})
	next, changed, e := a.RepairRequest(context.Background(), in, r, inference.HTTPFailure{Status: 422, Body: []byte("invalid reasoning_effort")})
	if e != nil || !changed || sends != 1 || learned != 1 || strings.Contains(string(next.Body), "reasoning_effort") {
		t.Fatalf("repair changed=%v sends=%d learned=%d err=%v", changed, sends, learned, e)
	}
	ctx := WithPolicy(context.Background(), Policy{Authoritative: true, ReasoningEfforts: map[string][]string{"zai-org/GLM-5.2": {"high"}}})
	_, changed, e = a.RepairRequest(ctx, in, r, inference.HTTPFailure{Status: 400, Body: []byte("invalid reasoning_effort")})
	if e != nil || changed || sends != 1 {
		t.Fatal("authoritative rung repaired")
	}
}

func TestFailedProfileRetainsRejectedRung(t *testing.T) {
	in := fixtureInput(`{"messages":[],"reasoning_effort":"high"}`)
	r, err := New().Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	learned := false
	a := NewWithOptions(Options{ProfileTransport: func(context.Context, inference.AdapterInput, *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("malformed profile"))}, nil
	}, LearnEfforts: func(_ context.Context, _ inference.AdapterInput, _ string, accepted, rejected []string) error {
		learned = true
		if strings.Join(rejected, ",") != "high" || strings.Join(accepted, ",") != "max" {
			t.Fatal(accepted, rejected)
		}
		return nil
	}})
	_, changed, err := a.RepairRequest(context.Background(), in, r, inference.HTTPFailure{Status: 400, Body: []byte("unsupported effort")})
	if err != nil || changed || !learned {
		t.Fatalf("changed=%v learned=%v err=%v", changed, learned, err)
	}
}
