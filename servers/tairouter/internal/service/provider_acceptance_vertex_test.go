// SPDX-License-Identifier: Apache-2.0
package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/providers"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

const providerAcceptanceVertexModel = "gemini-3.1-pro-preview"
const providerAcceptanceVertexResponse = `{"responseId":"vertex_fixture","modelVersion":"gemini-3.1-pro-preview","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"fixture ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`

func (f *providerAcceptanceFixture) vertexOAuth(project, location string) {
	f.t.Helper()
	f.data.Metadata.AuthKind = v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH
	f.data.Credential = &providers.Credential{Scope: f.scope, Provider: "google-vertex", AccessToken: f.secret, AccountID: "vertex-fixture-account", ProjectID: project, ExpiresAt: time.Now().Add(time.Hour), Metadata: map[string]string{"location": location}, Google: &providers.GoogleCredential{Type: providers.GoogleAuthorizedUser, ClientID: "fixture-client", ClientSecret: "fixture-client-secret", RefreshToken: "fixture-refresh-token", ProjectID: project}}
	f.save()
}
func TestProviderAcceptanceVertex(t *testing.T) {
	for _, oauth := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(map[bool]string{false: "key-express", true: "oauth-regional"}[oauth]+"/"+map[bool]string{false: "buffered", true: "streamed"}[stream], func(t *testing.T) {
				var sends atomic.Int32
				var f *providerAcceptanceFixture
				f = newProviderAcceptance(t, "google-vertex", providerAcceptanceVertexModel, func(w http.ResponseWriter, r *http.Request) {
					sends.Add(1)
					host, path := "aiplatform.googleapis.com", "/v1/publishers/google/models/"+providerAcceptanceVertexModel
					if oauth {
						host = "us-central1-aiplatform.googleapis.com"
						path = "/v1/projects/fixture-project/locations/us-central1/publishers/google/models/" + providerAcceptanceVertexModel
					}
					method, query := "generateContent", ""
					if stream {
						method, query = "streamGenerateContent", "alt=sse"
					}
					if r.Host != host || r.URL.Path != path+":"+method || r.URL.RawQuery != query || r.Method != "POST" {
						t.Error("Vertex canonical host/project/location/model/method mismatch")
					}
					if oauth {
						if r.Header.Get("Authorization") != "Bearer "+f.secret || r.Header.Get("X-Goog-Api-Key") != "" {
							t.Error("Vertex OAuth authentication mismatch")
						}
					} else {
						if r.Header.Get("X-Goog-Api-Key") != f.secret || r.Header.Get("Authorization") != "" {
							t.Error("Vertex key authentication mismatch")
						}
					}
					for _, header := range []string{"X-Tairouter-Google-Project", "X-Tairouter-Google-Location", "X-Api-Key", "Api-Key"} {
						if r.Header.Get(header) != "" {
							t.Errorf("Vertex leaked internal or unrelated header %s", header)
						}
					}
					b := providerAcceptanceBody(t, r)
					contents, _ := b["contents"].([]any)
					if len(contents) != 1 || b["request"] != nil || b["project"] != nil {
						t.Error("Vertex native content payload or envelope mismatch")
					} else {
						content, _ := contents[0].(map[string]any)
						parts, _ := content["parts"].([]any)
						if len(parts) != 1 || parts[0].(map[string]any)["text"] != "hello" {
							t.Error("Vertex user text not translated")
						}
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: %s\n\n", providerAcceptanceVertexResponse)
					} else {
						providerAcceptanceResponse(w, providerAcceptanceVertexResponse)
					}
				})
				if oauth {
					f.vertexOAuth("fixture-project", "us-central1")
				}
				request, _ := json.Marshal(map[string]any{"model": "fixture", "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "hello"}}})
				w := f.call("/v1/chat/completions", string(request), nil)
				providerAcceptanceStatus(t, w, 200)
				if sends.Load() != 1 || !strings.Contains(w.Body.String(), "fixture ok") {
					t.Fatal("Vertex response missing or repeated")
				}
				if stream && !strings.Contains(w.Body.String(), "[DONE]") {
					t.Fatal("Vertex translated stream lacks terminal")
				}
			})
		}
	}
}
func TestProviderAcceptanceVertexRequiresPersonalContext(t *testing.T) {
	for _, missing := range []string{"project", "location"} {
		t.Run(missing, func(t *testing.T) {
			var sends atomic.Int32
			f := newProviderAcceptance(t, "google-vertex", providerAcceptanceVertexModel, func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				providerAcceptanceResponse(w, providerAcceptanceVertexResponse)
			})
			project, location := "fixture-project", "us-central1"
			if missing == "project" {
				project = ""
			} else {
				location = ""
			}
			f.vertexOAuth(project, location)
			w := f.call("/v1/chat/completions", `{"model":"fixture","messages":[{"role":"user","content":"hello"}]}`, nil)
			if w.Code < 400 || sends.Load() != 0 {
				t.Fatal("Vertex inherited ambient context or dispatched incomplete OAuth context")
			}
		})
	}
}
func TestProviderAcceptanceVertexSchemaRecovery(t *testing.T) {
	var sends atomic.Int32
	f := newProviderAcceptance(t, "google-vertex", providerAcceptanceVertexModel, func(w http.ResponseWriter, r *http.Request) {
		n := sends.Add(1)
		b := providerAcceptanceBody(t, r)
		tools, _ := b["tools"].([]any)
		if len(tools) == 0 {
			t.Error("Vertex declaration missing")
		} else {
			tool, _ := tools[0].(map[string]any)
			decls, _ := tool["functionDeclarations"].([]any)
			if len(decls) != 1 {
				t.Error("Vertex declaration count changed")
			} else {
				decl, _ := decls[0].(map[string]any)
				params, _ := decl["parameters"].(map[string]any)
				if params == nil {
					params, _ = decl["parametersJsonSchema"].(map[string]any)
				}
				properties, _ := params["properties"].(map[string]any)
				if decl["name"] != "lookup" || (n == 1 && len(properties) != 1) || (n == 2 && len(properties) != 0) {
					t.Error("Vertex repair did not retain name and open rejected schema")
				}
			}
		}
		if n == 1 {
			w.WriteHeader(400)
			providerAcceptanceResponse(w, `{"error":{"message":"functionDeclarations[0] invalid json schema"}}`)
			return
		}
		providerAcceptanceResponse(w, providerAcceptanceVertexResponse)
	})
	w := f.call("/v1/chat/completions", `{"model":"fixture","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}}]}`, nil)
	providerAcceptanceStatus(t, w, 200)
	if sends.Load() != 2 {
		t.Fatal("Vertex schema refusal did not use one shared-budget repair")
	}
}
