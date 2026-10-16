// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func openRouterToolPNG(t *testing.T) string {
	t.Helper()
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(output.Bytes())
}

func openRouterFixture(t *testing.T, operation Operation, upstream http.HandlerFunc) *proxyFixture {
	t.Helper()
	f := newProxyFixture(t, domain.OpenAIResponses, []Operation{operation}, upstream)
	f.authority.scope.OpenRouterTools = true
	f.authority.scope.Harness = domain.Codex
	f.authority.scope.Provider.Endpoint = "https://openrouter.ai/api/v1"
	handler := f.server.Config.Handler.(*Handler)
	target, _ := url.Parse(f.upstream.URL)
	transport := handler.client.Transport
	handler.client.Transport = oauthProxyTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "openrouter.ai" {
			t.Fatal("adapter changed fixed upstream authority")
		}
		copy := request.Clone(request.Context())
		copy.URL.Scheme, copy.URL.Host = target.Scheme, target.Host
		return transport.RoundTrip(copy)
	})
	return f
}

func TestOpenRouterNativeImageAndEditUseFixedToolModelAndOriginalPNG(t *testing.T) {
	for _, operation := range []Operation{ImageCreate, ImageEdit} {
		t.Run(string(operation), func(t *testing.T) {
			encoded := openRouterToolPNG(t)
			f := openRouterFixture(t, operation, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/images" || r.Header.Get("Authorization") != "Bearer "+fixtureKey {
					t.Error("image operation changed path/account")
				}
				raw, _ := io.ReadAll(r.Body)
				var body map[string]json.RawMessage
				if json.Unmarshal(raw, &body) != nil || string(body["model"]) != `"openai/gpt-image-2"` || string(body["output_format"]) != `"png"` || string(body["n"]) != "1" || string(body["stream"]) != "false" {
					t.Error("image model/format changed")
				}
				if (body["input_references"] != nil) != (operation == ImageEdit) || body["images"] != nil {
					t.Error("native edit references were not translated")
				}
				if operation == ImageEdit && !bytes.Contains(raw, []byte(encoded)) {
					t.Error("original inline image bytes changed")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"created": uint64(123), "data": []any{map[string]string{"b64_json": encoded, "media_type": "image/png"}}})
			})
			body := map[string]any{"model": "gpt-image-2", "prompt": "private-test-prompt", "background": "transparent", "quality": "auto", "size": "auto"}
			path := "/images/generations"
			if operation == ImageEdit {
				path = "/images/edits"
				body["images"] = []any{map[string]string{"image_url": "data:image/png;base64," + encoded}}
			}
			raw, _ := json.Marshal(body)
			response, result, err := f.request(t, path, string(raw), nil)
			if err != nil || response.StatusCode != 200 || !bytes.Contains(result, []byte(encoded)) || bytes.Contains(result, []byte("background")) {
				t.Fatal("native image response failed or invented output metadata", err, string(result))
			}
			if f.calls.Load() != 1 || f.authority.keys.Load() != 1 || strings.Contains(f.logs.String(), encoded) || strings.Contains(f.logs.String(), "private-test-prompt") || strings.Contains(f.logs.String(), fixtureKey) {
				t.Fatal("duplicate request or private data logged")
			}
		})
	}
}

func TestOpenRouterNativeToolValidationPrecedesCredentialAccess(t *testing.T) {
	for _, body := range []string{
		`{"model":"other","prompt":"test"}`,
		`{"model":"gpt-image-2","prompt":"test","fallbacks":[]}`,
		`{"model":"gpt-image-2","prompt":"test","model":"other"}`,
		`{"model":"gpt-image-2","prompt":"test","n":2}`,
		`{"model":"gpt-image-2","prompt":"test","images":[{"image_url":"https://foreign.invalid/private.png"}]}`,
	} {
		f := openRouterFixture(t, ImageCreate, func(http.ResponseWriter, *http.Request) { t.Error("invalid tool request reached upstream") })
		response, _, err := f.request(t, "/images/generations", body, nil)
		if err != nil || response.StatusCode < 400 || f.authority.keys.Load() != 0 || f.calls.Load() != 0 {
			t.Fatal("invalid tool request acquired provider authority", err)
		}
	}
}

func TestOpenRouterNativeSearchKeepsConversationModelAndRequiresServerTools(t *testing.T) {
	f := openRouterFixture(t, StandaloneSearch, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/api/v1/responses" || !bytes.Contains(raw, []byte(`"model":"fixed-model"`)) || !bytes.Contains(raw, []byte(`"tool_choice":"required"`)) || !bytes.Contains(raw, []byte("openrouter:web_search")) || !bytes.Contains(raw, []byte("openrouter:web_fetch")) {
			t.Error("search changed model or lost selected tools")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Original search output https://example.test/result"}]}]}`)
	})
	response, raw, err := f.request(t, "/alpha/search", `{"id":"native-thread","model":"fixed-model","commands":{"search_query":[{"q":"fixture"}]}}`, nil)
	if err != nil || response.StatusCode != 200 || !bytes.Contains(raw, []byte("Original search output")) || !bytes.Contains(raw, []byte(`"encrypted_output":null`)) {
		t.Fatal("native search output failed", err, string(raw))
	}
}

func TestOpenRouterToolResponseNeverAdoptsInvalidBytesOrPartialText(t *testing.T) {
	for _, raw := range []string{`{"created":1,"data":[{"b64_json":"not-png"}]}`, `{"created":1,"data":[]}`, `{"created":1,"data":[{"b64_json":"","media_type":"image/jpeg"}]}`} {
		if _, err := projectOpenRouterToolResponse([]byte(raw), ImageCreate); err == nil {
			t.Fatal("invalid generated image accepted")
		}
	}
	if _, err := projectOpenRouterToolResponse([]byte(`{"status":"failed","output":[]}`), StandaloneSearch); err == nil {
		t.Fatal("failed search became success")
	}
}

func TestOpenRouterIncompleteToolResultDoesNotInviteRetry(t *testing.T) {
	f := openRouterFixture(t, StandaloneSearch, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"private incomplete output"}]}]}`)
	})
	response, _, err := f.request(t, "/alpha/search", `{"id":"fixture","model":"fixed-model","commands":{"search_query":[{"q":"fixture"}]}}`, nil)
	if err != nil || response.StatusCode != http.StatusUnprocessableEntity || f.calls.Load() != 1 {
		t.Fatal("incomplete tool response invited retry", err, response.StatusCode)
	}
	if !strings.Contains(f.logs.String(), "max_output_tokens") || strings.Contains(f.logs.String(), "private incomplete output") {
		t.Fatal("projection diagnostic lost closed failure or exposed content")
	}
}
