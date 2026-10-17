// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestOpenRouterSearchPreservesNativeSettings(t *testing.T) {
	for _, settings := range []string{`{}`, `null`, `{"allowed_callers":["direct"],"external_web_access":true}`, `{"user_location":{"type":"approximate","country":"KR","region":"Seoul","city":"Seoul","timezone":"Asia/Seoul"},"search_context_size":"high","filters":{"allowed_domains":["example.test"],"blocked_domains":["blocked.test"]},"allowed_callers":["direct"],"external_web_access":"live"}`} {
		f := openRouterFixture(t, StandaloneSearch, func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				Tools []struct {
					Type       string                     `json:"type"`
					Parameters map[string]json.RawMessage `json:"parameters"`
				} `json:"tools"`
			}
			raw, _ := io.ReadAll(r.Body)
			if json.Unmarshal(raw, &request) != nil || len(request.Tools) != 2 {
				t.Error("missing tool definitions")
				return
			}
			p := request.Tools[0].Parameters
			var original map[string]json.RawMessage
			_ = json.Unmarshal([]byte(settings), &original)
			if string(p["user_location"]) != string(original["user_location"]) || string(p["search_context_size"]) != string(original["search_context_size"]) {
				t.Error("location or context size changed")
			}
			if original["filters"] != nil && (string(p["allowed_domains"]) != `["example.test"]` || string(p["excluded_domains"]) != `["blocked.test"]`) {
				t.Error("domain restrictions disappeared")
			}
			if p["allowed_callers"] != nil || p["external_web_access"] != nil {
				t.Error("native settings leaked as unsupported parameters")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"openrouter:web_search","status":"completed"},{"type":"message","content":[{"type":"output_text","text":"original result"}]}]}`)
		})
		response, _, err := f.request(t, "/alpha/search", `{"id":"fixture","model":"fixed-model","commands":{"search_query":[{"q":"fixture"}]},"settings":`+settings+`}`, nil)
		if err != nil || response.StatusCode != 200 || f.authority.keys.Load() != 1 {
			t.Fatal("supported settings rejected", err, response.StatusCode)
		}
	}
}
func TestOpenRouterSearchRejectsUnsupportedSettingsBeforeCredentials(t *testing.T) {
	for _, settings := range []string{
		`{"external_web_access":false}`, `{"external_web_access":"cached"}`, `{"external_web_access":"indexed"}`,
		`{"allowed_callers":["shell"]}`, `{"allowed_callers":[]}`, `{"image_settings":{"caption":false}}`,
		`{"image_settings":{"max_results":3}}`, `{"search_context_size":"huge"}`, `{"user_location":{"type":"exact"}}`,
		`{"user_location":{"type":"approximate","extra":true}}`, `{"filters":{"unknown":[]}}`, `{"unknown":true}`,
		`{"filters":{"allowed_domains":"example.test"}}`,
		`{"filters":{"allowed_domains":[null]}}`,
	} {
		f := openRouterFixture(t, StandaloneSearch, func(http.ResponseWriter, *http.Request) { t.Error("unsupported setting reached upstream") })
		response, _, err := f.request(t, "/alpha/search", `{"id":"fixture","model":"fixed-model","commands":{"search_query":[{"q":"fixture"}]},"settings":`+settings+`}`, nil)
		if err != nil || response.StatusCode < 400 || f.authority.keys.Load() != 0 || f.calls.Load() != 0 {
			t.Fatal("unsupported setting acquired credentials", settings, err)
		}
	}
}
