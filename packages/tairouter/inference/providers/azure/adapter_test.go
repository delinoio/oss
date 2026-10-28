// SPDX-License-Identifier: Apache-2.0
package azure

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"io"
	"net/http"
	"strings"
	"testing"
)

func input() inference.AdapterInput {
	return inference.AdapterInput{Operation: inference.Generate, Wire: inference.Responses, Target: inference.Target{BaseURL: "https://resource.openai.azure.com/openai/v1", Model: "deployment", Auth: inference.APIKey}, Credential: inference.Credential{Secret: "key"}, Body: json.RawMessage(`{"model":"client","input":"hello","max_output_tokens":1,"stream":true}`)}
}
func TestPinnedRequest(t *testing.T) {
	in := input()
	in.Target.Headers = http.Header{"authorization": {"Bearer stale"}, "X-Custom": {"custom"}}
	in.Credential.Headers = http.Header{"api-key": {"stale"}, OptionsHeader: {`{"min_max_output_tokens":128}`}}
	r, e := New().Build(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	if r.URL != "https://resource.openai.azure.com/openai/v1/responses" || r.ResponseWire != inference.Responses || r.Headers.Get("Api-Key") != "key" || r.Headers.Get("Authorization") != "" || r.Headers.Get("X-Custom") != "custom" || len(r.Headers.Values(OptionsHeader)) != 0 {
		t.Fatalf("bad request %+v", r)
	}
	var b map[string]any
	json.Unmarshal(r.Body, &b)
	if b["max_output_tokens"] != float64(128) || b["store"] != false || b["model"] != "deployment" {
		t.Fatal(b)
	}
	if string(in.Body) != `{"model":"client","input":"hello","max_output_tokens":1,"stream":true}` {
		t.Fatal("mutated caller")
	}
}
func TestBudgetFloor(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{{"1", "128"}, {"1.0", "128"}, {"1e0", "128"}, {"128", "128"}, {"256", "256"}, {"0", "0"}, {"-1", "-1"}, {"1.5", "1.5"}, {"null", "null"}, {`"1"`, `"1"`}} {
		if got := string(EffectiveOutputBudget([]byte(tc.raw), 128)); got != tc.want {
			t.Fatalf("%s: %s", tc.raw, got)
		}
	}
	for _, floor := range []int64{0, -1} {
		if string(EffectiveOutputBudget([]byte("1"), floor)) != "1" {
			t.Fatal("invalid floor applied")
		}
	}
	in := input()
	in.Body = []byte(`{"input":"hello"}`)
	r, e := New().Build(WithOptions(context.Background(), Options{128}), in)
	if e != nil || strings.Contains(string(r.Body), "max_output_tokens") {
		t.Fatalf("invented budget %s %v", r.Body, e)
	}
}
func TestRefusals(t *testing.T) {
	for _, base := range []string{"https://{resource}.openai.azure.com/openai/v1", "https://resource.openai.azure.com/{deployment}", "https://resource.openai.azure.com/%7Bdeployment%7D", "https://resource.openai.azure.com/openai/v1?api-version=2024-01-01", "https://user@resource.openai.azure.com", "not-a-url"} {
		in := input()
		in.Target.BaseURL = base
		if _, e := New().Build(context.Background(), in); e == nil {
			t.Fatalf("accepted %s", base)
		}
	}
	for _, auth := range []inference.AuthMode{inference.OAuth, inference.Anonymous, "forward"} {
		in := input()
		in.Target.Auth = auth
		if _, e := New().Build(context.Background(), in); e == nil {
			t.Fatal("accepted auth", auth)
		}
	}
	for _, body := range []string{"null", `{} {}`, strings.Repeat("x", MaxRequestBytes+1)} {
		in := input()
		in.Body = []byte(body)
		if _, e := New().Build(context.Background(), in); e == nil {
			t.Fatal("accepted body")
		}
	}
	in := input()
	in.Credential.Secret = " "
	if _, e := New().Build(context.Background(), in); e == nil {
		t.Fatal("accepted blank key")
	}
}
func TestDecodeAndConcurrency(t *testing.T) {
	a := New()
	for _, body := range []string{`{"object":"response","output":[]}`, "event: response.completed\ndata: {}\n\n"} {
		r := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
		out, e := a.Decode(context.Background(), r)
		if e != nil || out != r {
			t.Fatal(e)
		}
		raw, _ := io.ReadAll(out.Body)
		out.Body.Close()
		if string(raw) != body {
			t.Fatal(string(raw))
		}
	}
	for i := 0; i < 16; i++ {
		t.Run("concurrent", func(t *testing.T) {
			t.Parallel()
			if _, e := a.Build(context.Background(), input()); e != nil {
				t.Fatal(e)
			}
		})
	}
}
