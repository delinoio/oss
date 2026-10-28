// SPDX-License-Identifier: Apache-2.0
package google

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

func TestVertexPinnedEndpointSelection(t *testing.T) {
	for _, tc := range []struct {
		name, project, location, host, path string
		auth                                inference.AuthMode
	}{
		{"service account regional", "project-123", "us-central1", "us-central1-aiplatform.googleapis.com", "/v1/projects/project-123/locations/us-central1/publishers/google/models/gemini-3.7-flash:streamGenerateContent", inference.OAuth},
		{"service account global", "project-123", "global", "aiplatform.googleapis.com", "/v1/projects/project-123/locations/global/publishers/google/models/gemini-3.7-flash:streamGenerateContent", inference.OAuth},
		{"key express", "", "ignored.invalid", "aiplatform.googleapis.com", "/v1/publishers/google/models/gemini-3.7-flash:streamGenerateContent", inference.APIKey},
		{"key project defaultglobal", "project-123", "", "aiplatform.googleapis.com", "/v1/projects/project-123/locations/global/publishers/google/models/gemini-3.7-flash:streamGenerateContent", inference.APIKey},
		{"encoded project", "project/name", "global", "aiplatform.googleapis.com", "/v1/projects/project%2Fname/locations/global/publishers/google/models/gemini-3.7-flash:streamGenerateContent", inference.APIKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input(`{"messages":[{"role":"user","content":"hi"}],"stream":true}`)
			in.Target.Model = "gemini-3.7-flash"
			in.Target.Wire = WireVertex
			in.Target.Auth = tc.auth
			in.Credential.Headers = http.Header{ProjectHeader: {tc.project}, LocationHeader: {tc.location}, "Authorization": {"wrong-bearer"}, "X-Goog-Api-Key": {"wrong-key"}}
			a := NewVertex(Options{})
			r, e := a.Build(a.PrepareContext(context.Background()), in)
			if e != nil {
				t.Fatal(e)
			}
			if r.URL != "https://"+tc.host+tc.path+"?alt=sse" {
				t.Fatal(r.URL)
			}
			if r.Headers.Get(ProjectHeader) != "" || r.Headers.Get(LocationHeader) != "" {
				t.Fatal("metadata leaked")
			}
			if tc.auth == inference.OAuth {
				if r.Headers.Get("Authorization") != "Bearer selected-secret" || r.Headers.Get("X-Goog-Api-Key") != "" {
					t.Fatal("wrong bearer auth")
				}
			} else {
				if r.Headers.Get("X-Goog-Api-Key") != "selected-secret" || r.Headers.Get("Authorization") != "" {
					t.Fatal("wrong key auth")
				}
			}
			body, _ := decodeJSON(r.Body)
			if body["request"] != nil || body["contents"] == nil {
				t.Fatal("Vertex used CCA envelope")
			}
			if obj(body["generationConfig"])["thinkingConfig"] != nil {
				t.Fatal("Vertex inferred unsupported reasoning ladder")
			}
		})
	}
}
func TestVertexRejectsUnresolvedOrUnsafeSelection(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "ambient-project")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "global")
	for _, tc := range []struct {
		project, location, model string
		auth                     inference.AuthMode
	}{
		{"", "global", "gemini", inference.OAuth}, {"project", "", "gemini", inference.OAuth},
		{"auto", "global", "gemini", inference.OAuth}, {"auto", "global", "gemini", inference.APIKey},
		{"project", "global.attacker.test", "gemini", inference.OAuth}, {"project", "US-CENTRAL1", "gemini", inference.OAuth},
		{"project", "global", "gemini..bad", inference.OAuth}, {"project", "global", "../model", inference.APIKey},
	} {
		in := input(`{"messages":[{"role":"user","content":"hi"}]}`)
		in.Target.Auth = tc.auth
		in.Target.Model = tc.model
		in.Credential.Headers = http.Header{ProjectHeader: {tc.project}, LocationHeader: {tc.location}}
		if _, e := NewVertex(Options{}).Build(context.Background(), in); e == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}
func TestVertexSelectedOriginAndMetadataPrecedence(t *testing.T) {
	a := NewVertex(Options{})
	in := input(`{"messages":[{"role":"user","content":"hi"}]}`)
	in.Target.Auth = inference.OAuth
	in.Target.BaseURL = "https://verified-fixture.example:8443"
	in.Target.Headers = http.Header{ProjectHeader: {"wrong-project"}, LocationHeader: {"wrong-location"}}
	in.Credential.Headers = http.Header{ProjectHeader: {"selected-project"}, LocationHeader: {"global"}}
	req, e := a.Build(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(req.URL, in.Target.BaseURL+"/v1/projects/selected-project/locations/global/") {
		t.Fatal(req.URL)
	}
	in.Target.BaseURL += "/unrelated-prefix"
	if _, e = a.Build(context.Background(), in); e == nil {
		t.Fatal("non-Vertex prefix accepted")
	}
}
