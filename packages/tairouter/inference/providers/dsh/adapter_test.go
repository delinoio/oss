// SPDX-License-Identifier: Apache-2.0
package dsh

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

func input() inference.AdapterInput {
	return inference.AdapterInput{Operation: inference.Generate, Wire: inference.Chat, Target: inference.Target{BaseURL: "https://api.deepseek.com/anthropic", Model: "deepseek", Auth: inference.OAuth}, Credential: inference.Credential{Secret: " token "}, Body: json.RawMessage(`{"model":"client-model","messages":[{"role":"user","content":"hello"}],"max_tokens":25,"stream":true}`)}
}
func TestBuildHeadersAndTranslation(t *testing.T) {
	in := input()
	in.Target.Headers = http.Header{"X-Custom": {"custom"}, "Anthropic-Beta": {"unwanted"}}
	in.Credential.Headers = http.Header{"authorization": {"Bearer generic"}, "x-dsh-auth-token": {"stale"}, "x-api-key": {"generic"}, "X-Claude-Code-Session-Id": {"private"}, "X-Client-Request-Id": {"private"}}
	in.ClientHeaders = http.Header{"Authorization": {"client-secret"}}
	before := string(in.Body)
	r, err := New().Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if r.URL != MessagesURL || r.ResponseWire != inference.Anthropic || r.Method != "POST" {
		t.Fatalf("unexpected request %+v", r)
	}
	if r.Headers.Get("x-dsh-auth-token") != "token" || r.Headers.Get("anthropic-version") != "2023-06-01" || r.Headers.Get("X-Custom") != "custom" || r.Headers.Get("Accept") != "text/event-stream" {
		t.Fatal(r.Headers)
	}
	for _, k := range []string{"Authorization", "X-Api-Key", "Api-Key", "X-Goog-Api-Key", "Anthropic-Beta", "X-Claude-Code-Session-Id", "X-Client-Request-Id", "Host"} {
		if values, exists := r.Headers[k]; !exists || len(values) != 0 {
			t.Fatalf("missing deletion marker %s", k)
		}
	}
	var body map[string]any
	if err = json.Unmarshal(r.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "deepseek" || body["max_tokens"] != float64(25) || len(body["messages"].([]any)) != 1 {
		t.Fatal(body)
	}
	if string(in.Body) != before || in.Credential.Headers["authorization"][0] != "Bearer generic" {
		t.Fatal("mutated input")
	}
}
func TestDestinationAndReservedHeaders(t *testing.T) {
	for _, base := range []string{"", "https://API.DEEPSEEK.COM", "https://api.deepseek.com/anthropic/v1/", MessagesURL} {
		if u, e := ResolveMessagesURL(base); e != nil || u != MessagesURL {
			t.Fatalf("%q %q %v", base, u, e)
		}
	}
	for _, base := range []string{"https://evil.test/anthropic", "http://api.deepseek.com", "https://api.deepseek.com:444", "https://user@api.deepseek.com", "https://api.deepseek.com/?token=x", "https://api.deepseek.com/#fragment", "https://api.deepseek.com/v1/messages"} {
		if _, e := ResolveMessagesURL(base); e == nil {
			t.Fatalf("accepted %s", base)
		}
	}
	for _, key := range []string{"aUtHoRiZaTiOn", "X-API-KEY", "X-DSH-AUTH-TOKEN", "anthropic-version", "host"} {
		in := input()
		in.Target.Headers = http.Header{key: {"override"}}
		if _, e := New().Build(context.Background(), in); e == nil {
			t.Fatalf("accepted %s", key)
		}
	}
}
func TestReimportAndDecode(t *testing.T) {
	a := New()
	for _, status := range []int{401, 403, 429, 500} {
		r, e := a.RecoverHTTP(context.Background(), input(), inference.HTTPFailure{Status: status})
		if e != nil {
			t.Fatal(e)
		}
		if status == 401 {
			if r.Action != inference.RecoveryStop || r.Error.Code != "dsh_reimport_required" {
				t.Fatal(r)
			}
		} else if r.Action != inference.RecoveryDefault {
			t.Fatal(r)
		}
	}
	for _, payload := range []string{`{"type":"message","content":[{"type":"text","text":"ok"}]}`, "event: message_stop\ndata: {}\n\n"} {
		response := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload))}
		out, e := a.Decode(context.Background(), response)
		if e != nil || out != response {
			t.Fatal(e)
		}
		raw, _ := io.ReadAll(out.Body)
		out.Body.Close()
		if string(raw) != payload {
			t.Fatal(string(raw))
		}
	}
}
func TestBoundsAndIsolation(t *testing.T) {
	for _, body := range []string{"null", `{} {}`, strings.Repeat("x", MaxRequestBytes+1)} {
		in := input()
		in.Body = []byte(body)
		if _, e := New().Build(context.Background(), in); e == nil {
			t.Fatal("accepted invalid body")
		}
	}
	in := input()
	in.Credential.Secret = " "
	if _, e := New().Build(context.Background(), in); e == nil {
		t.Fatal("accepted empty token")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := New().Build(ctx, input()); e != context.Canceled {
		t.Fatal(e)
	}
	a := New()
	for i := 0; i < 16; i++ {
		t.Run("concurrent", func(t *testing.T) {
			t.Parallel()
			r, e := a.Build(context.Background(), input())
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(r.Headers.Values("X-Dsh-Auth-Token"), []string{"token"}) {
				t.Fatal(r.Headers)
			}
		})
	}
}
