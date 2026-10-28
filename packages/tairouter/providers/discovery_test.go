// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"errors"
	"google.golang.org/protobuf/encoding/protowire"
	"net/http"
	"strings"
	"testing"
)

func TestSpecializedDiscovery(t *testing.T) {
	t.Run("cursor protobuf and no aliases", func(t *testing.T) {
		row := append(protoBytes(1, []byte("model-wire")), protoNumber(7, 1)...)
		row = append(row, protoBytes(6, []byte("alias"))...)
		response := protoBytes(1, row)
		a, _ := newMock(t, responseStep{"POST", "https://api2.cursor.sh/agent.v1.AgentService/GetUsableModels", 200, string(response), func(t *testing.T, r *http.Request, b []byte) {
			if len(b) != 0 || r.Header.Get("Content-Type") != "application/proto" || r.Header.Get("Connect-Protocol-Version") != "1" {
				t.Fatal("invalid protobuf request")
			}
		}})
		c := Credential{Scope: testScope, Provider: "cursor", AccessToken: "token"}
		v, _, err := a.Discover(context.Background(), testScope, Connection{Provider: c.Provider, Credential: &c})
		if err != nil || len(v.Models) != 1 || v.Models[0].ID != "model-wire" || !v.Models[0].MaxMode {
			t.Fatalf("%+v %v", v, err)
		}
	})
	t.Run("kiro ARN region and aws json", func(t *testing.T) {
		c := Credential{Scope: testScope, Provider: "kiro", AccessToken: "token", Metadata: map[string]string{"profileArn": "arn:aws:codewhisperer:eu-west-1:123:profile/test", "apiRegion": "us-east-1"}}
		endpoint, _, _, _ := kiroManagement(c, false)
		a, _ := newMock(t, responseStep{"POST", endpoint, 200, `{"models":[{"modelId":"model","tokenLimits":{"maxInputTokens":1000000}}]}`, func(t *testing.T, r *http.Request, b []byte) {
			if r.URL.Host != "management.eu-west-1.kiro.dev" || r.Header.Get("Content-Type") != "application/x-amz-json-1.0" || r.Header.Get("X-Amz-Target") != "KiroControlPlaneBearerService.ListAvailableModels" {
				t.Fatal("wrong management request")
			}
		}})
		models, err := a.specialModels(context.Background(), testScope, c)
		if err != nil || len(models) != 1 || models[0].ContextWindow != 1000000 {
			t.Fatalf("%v %v", models, err)
		}
	})
	t.Run("workbuddy CLI roster", func(t *testing.T) {
		c := Credential{Scope: testScope, Provider: "workbuddy-ai", AccessToken: "token", Metadata: map[string]string{"uid": "user", "domain": "www.workbuddy.ai", "enterpriseId": "team"}}
		a, _ := newMock(t, responseStep{"GET", "https://www.workbuddy.ai/v3/config", 200, `{"code":0,"data":{"agents":[{"name":"cli","models":["glm-test","vision"]}],"models":[{"id":"glm-test","supportsImages":true,"supportsReasoning":true},{"id":"vision","supportsImages":true},{"id":"other"}]}}`, func(t *testing.T, r *http.Request, b []byte) {
			if r.Header.Get("X-Tenant-Id") != "team" {
				t.Fatal("lost team")
			}
		}})
		models, err := a.specialModels(context.Background(), testScope, c)
		if err != nil || len(models) != 2 || len(models[0].InputModalities) != 1 || len(models[1].InputModalities) != 2 || models[0].ReasoningEfforts[0] != "none" {
			t.Fatalf("%+v %v", models, err)
		}
	})
	t.Run("google account project", func(t *testing.T) {
		c := Credential{Scope: testScope, Provider: "google-antigravity", AccessToken: "token", ProjectID: "personal-project"}
		a, _ := newMock(t, responseStep{"POST", "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels", 200, `{"models":{"gemini":{"maxTokens":1000000}}}`, func(t *testing.T, r *http.Request, b []byte) {
			if !strings.Contains(string(b), `"project":"personal-project"`) {
				t.Fatal("missing project")
			}
		}})
		models, err := a.specialModels(context.Background(), testScope, c)
		if err != nil || len(models) != 1 {
			t.Fatal(models, err)
		}
	})
	t.Run("devin account JWT and enabled UIDs", func(t *testing.T) {
		c := Credential{Scope: testScope, Provider: "devin", AccessToken: "cog_private_token", APIBaseURL: "https://server.codeium.com"}
		row := append(protoBytes(22, []byte("enabled")), protoNumber(18, 1000000)...)
		disabled := append(protoBytes(22, []byte("disabled")), protoNumber(4, 1)...)
		response := append(protoBytes(1, row), protoBytes(1, disabled)...)
		a, _ := newMock(t, responseStep{"POST", c.APIBaseURL + "/exa.auth_pb.AuthService/GetUserJwt", 200, string(protoBytes(1, []byte("jwt"))), nil}, responseStep{"POST", c.APIBaseURL + "/exa.api_server_pb.ApiServerService/GetCascadeModelConfigs", 200, string(response), func(t *testing.T, r *http.Request, b []byte) {
			f, e := protoFields(b)
			if e != nil || len(f) != 1 {
				t.Fatal(e)
			}
			nested, e := protoFields(f[0].b)
			found := false
			for _, v := range nested {
				if v.n == 21 && string(v.b) == "jwt" {
					found = true
				}
			}
			if e != nil || !found {
				t.Fatal("missing JWT")
			}
		}})
		models, err := a.specialModels(context.Background(), testScope, c)
		if err != nil || len(models) != 1 || models[0].ID != "enabled" {
			t.Fatal(models, err)
		}
	})
}
func TestDiscoveryMalformedAndBoundaries(t *testing.T) {
	for _, b := range [][]byte{{0xff}, {0x0a, 0xff}, protoBytes(1, []byte{0xff}), protoBytes(1, protoBytes(1, []byte{0xff}))} {
		if _, e := cursorModels(b); e == nil {
			t.Fatalf("accepted malformed %x", b)
		}
	}
	c := Credential{Scope: testScope, Provider: "cursor", AccessToken: "token"}
	other := testScope
	other.Generation++
	a, _ := newMock(t)
	if _, _, err := a.Discover(context.Background(), other, Connection{Provider: "cursor", Credential: &c}); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	a, _ = newMock(t, responseStep{"POST", "https://api2.cursor.sh/agent.v1.AgentService/GetUsableModels", 401, "private response must not escape", nil})
	_, err := a.specialModels(context.Background(), testScope, c)
	var ae *AuthError
	if !errors.As(err, &ae) || ae.Status != 401 || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
	// Invalid unknown protobuf fields must not mask a truncated known model row.
	if _, err := protoFields(protowire.AppendTag(nil, 3, protowire.BytesType)); err == nil {
		t.Fatal("truncation accepted")
	}
}
