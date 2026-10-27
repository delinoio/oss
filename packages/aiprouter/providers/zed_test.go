// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"net/http"
	"testing"
	"time"
)

type executorFunc func(context.Context, core.Scope, *http.Request) (*http.Response, error)

func (f executorFunc) Do(ctx context.Context, s core.Scope, r *http.Request) (*http.Response, error) {
	return f(ctx, s, r)
}
func preparedZed(a *Auth) Credential {
	return Credential{Scope: testScope, Provider: "zed", AccountID: "user", AccessToken: "old-llm", ExpiresAt: a.now().Add(time.Hour), Zed: &ZedAccount{NativeToken: "native", OrganizationID: "org", LLMToken: "old-llm", SystemID: "system"}}
}
func TestZedCatalogRecovery(t *testing.T) {
	for _, header := range []string{"x-zed-expired-token", "x-zed-outdated-token", "401"} {
		t.Run(header, func(t *testing.T) {
			a, m := newMock(t,
				responseStep{"GET", "https://cloud.zed.dev/models", 200, `{}`, func(t *testing.T, r *http.Request, b []byte) {
					if r.Header.Get("Authorization") != "Bearer old-llm" {
						t.Fatal("raw account token sent to models")
					}
				}},
				responseStep{"GET", "https://cloud.zed.dev/client/users/me", 200, `{"organizations":[{"id":"team"},{"id":"personal","is_personal":true}]}`, func(t *testing.T, r *http.Request, b []byte) {
					if r.Header.Get("Authorization") != "user native" || r.Header.Get("x-zed-system-id") != "system" {
						t.Fatal("wrong native lookup auth")
					}
				}},
				responseStep{"POST", "https://cloud.zed.dev/client/llm_tokens", 200, `{"token":{"value":"new-llm"}}`, jsonValue("organization_id", "personal")},
				responseStep{"GET", "https://cloud.zed.dev/models", 200, `{"models":[{"id":["model","variant"],"provider":"anthropic","display_name":"Model","max_token_count":200000,"supports_tools":true,"supports_images":true,"supported_effort_levels":["low","high"]},{"id":"disabled","is_disabled":true}],"default_model":"model","recommended_models":["model"]}`, func(t *testing.T, r *http.Request, b []byte) {
					if r.Header.Get("Authorization") != "Bearer new-llm" || r.Header.Get("x-zed-client-supports-x-ai") != "true" {
						t.Fatal("bad catalog bearer/capability")
					}
				}})
			a.executor = executorFunc(func(ctx context.Context, s core.Scope, r *http.Request) (*http.Response, error) {
				res, e := m.Do(ctx, s, r)
				if m.calls == 1 {
					if header == "401" {
						res.StatusCode = 401
					} else {
						res.Header.Set(header, "")
					}
				}
				return res, e
			})
			original := preparedZed(a)
			catalog, c, e := a.ZedModels(context.Background(), testScope, original)
			if e != nil {
				t.Fatal(e)
			}
			if c.AccessToken != "new-llm" || c.Zed.OrganizationID != "personal" || original.Zed.LLMToken != "old-llm" {
				t.Fatal("refresh changed input or lost account ownership")
			}
			if len(catalog.Models) != 1 || catalog.Models[0].ID != "model" || !catalog.Models[0].SupportsTools || catalog.Models[0].ContextLength != 200000 || catalog.DefaultModel != "model" {
				t.Fatal("catalog projection lost metadata")
			}
		})
	}
}
func TestZedRawTokenRejected(t *testing.T) {
	a, _ := newMock(t)
	c := Credential{Scope: testScope, Provider: "zed", AccountID: "user", AccessToken: "native"}
	if _, _, e := a.ZedModels(context.Background(), testScope, c); !errors.Is(e, ErrReauthenticate) {
		t.Fatal(e)
	}
	if _, e := Resolve(Connection{Provider: "zed", Credential: &c}, "model", InboundResponses); !errors.Is(e, ErrConnection) {
		t.Fatal("raw native token accepted for inference")
	}
	c = preparedZed(a)
	if _, e := Resolve(Connection{Provider: "zed", Credential: &c}, "model", InboundResponses); e != nil {
		t.Fatal(e)
	}
}
func TestZedNoOrganizationStopsExchange(t *testing.T) {
	a, _ := newMock(t, responseStep{"GET", "https://cloud.zed.dev/client/users/me", 200, `{"organizations":[]}`, nil})
	if _, e := a.PrepareZed(context.Background(), testScope, preparedZed(a)); !errors.Is(e, ErrResponse) {
		t.Fatal(e)
	}
}
func TestZedRecoveryStopsAfterOneAttempt(t *testing.T) {
	a, m := newMock(t, responseStep{"GET", "https://cloud.zed.dev/models", 401, `{}`, nil}, responseStep{"GET", "https://cloud.zed.dev/client/users/me", 200, `{"defaultOrganizationId":"org"}`, nil}, responseStep{"POST", "https://cloud.zed.dev/client/llm_tokens", 200, `{"llm_token":"new"}`, nil}, responseStep{"GET", "https://cloud.zed.dev/models", 401, `{}`, nil})
	if _, _, e := a.ZedModels(context.Background(), testScope, preparedZed(a)); !errors.Is(e, ErrReauthenticate) || m.calls != 4 {
		t.Fatalf("unbounded recovery: %v", e)
	}
}
func TestGoogleProjectRequiredForRoute(t *testing.T) {
	c := Credential{Scope: testScope, Provider: "google-antigravity", AccessToken: "token"}
	if _, e := Resolve(Connection{Provider: c.Provider, Credential: &c}, "gemini", InboundChat); !errors.Is(e, ErrConnection) {
		t.Fatal("missing project allowed")
	}
	c.ProjectID = "owned-project"
	if _, e := Resolve(Connection{Provider: c.Provider, Credential: &c}, "gemini", InboundChat); e != nil {
		t.Fatal(e)
	}
}
