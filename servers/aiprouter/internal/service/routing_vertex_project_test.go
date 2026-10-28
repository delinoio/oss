// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"google.golang.org/protobuf/proto"
)

func TestVertexProjectAutoComposedCacheAndRegion(t *testing.T) {
	var probes, models atomic.Int32
	var f *providerAcceptanceFixture
	f = newProviderAcceptance(t, "google-vertex", providerAcceptanceVertexModel, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "ocx-project-probe") {
			probes.Add(1)
			if r.Method != "POST" || r.Host != "aiplatform.googleapis.com" || r.Header.Get("X-Goog-Api-Key") != f.secret || r.Header.Get("Authorization") != "" {
				t.Error("probe destination/auth changed")
			}
			w.WriteHeader(404)
			w.Write([]byte(`{"error":{"message":"projects/123456789/locations/us-central1/publishers/google/models/ocx-project-probe"}}`))
			return
		}
		models.Add(1)
		if r.Host != "us-central1-aiplatform.googleapis.com" || !strings.HasPrefix(r.URL.Path, "/v1/projects/123456789/locations/us-central1/publishers/google/models/") {
			t.Errorf("wrong resolved destination %s%s", r.Host, r.URL.Path)
		}
		providerAcceptanceResponse(w, providerAcceptanceVertexResponse)
	})
	f.data.Metadata.Configuration = &v1.ProviderConfiguration{VertexProject: proto.String("auto"), VertexLocation: proto.String("us-central1")}
	f.save()
	for i := 0; i < 2; i++ {
		w := f.call("/v1/chat/completions", `{"model":"fixture","messages":[{"role":"user","content":"fixture"}]}`, nil)
		if w.Code != 200 {
			t.Fatalf("status%d %s", w.Code, w.Body.String())
		}
	}
	if probes.Load() != 1 || models.Load() != 2 {
		t.Fatalf("probe=%d models=%d", probes.Load(), models.Load())
	}
}

func TestVertexProjectAutoUninformativeNotCached(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"denied", 403, `projects/12/locations/global/`},
		{"missing", 404, `{"error":"missing"}`},
		{"oversized", 404, strings.Repeat("x", 4097) + ` projects/12/locations/global/`},
		{"invalid-utf8", 404, string([]byte{0xff}) + ` projects/12/locations/global/`},
		{"redirect", 302, `projects/12/locations/global/`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var probes, models atomic.Int32
			f := newProviderAcceptance(t, "google-vertex", providerAcceptanceVertexModel, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "ocx-project-probe") {
					probes.Add(1)
					w.Header().Set("Location", "https://untrusted.example/")
					w.WriteHeader(tc.status)
					w.Write([]byte(tc.body))
					return
				}
				models.Add(1)
				if strings.Contains(r.URL.Path, "/projects/") || r.Host != "aiplatform.googleapis.com" {
					t.Error("uninformative probe did not use express")
				}
				providerAcceptanceResponse(w, providerAcceptanceVertexResponse)
			})
			f.data.Metadata.Configuration = &v1.ProviderConfiguration{VertexProject: proto.String("auto")}
			f.save()
			for i := 0; i < 2; i++ {
				w := f.call("/v1/chat/completions", `{"model":"fixture","messages":[{"role":"user","content":"fixture"}]}`, nil)
				if w.Code != 200 {
					t.Fatalf("status%d %s", w.Code, w.Body.String())
				}
			}
			if probes.Load() != 2 || models.Load() != 2 {
				t.Fatalf("negative cached or redirected: probe=%d model=%d", probes.Load(), models.Load())
			}
		})
	}
}

type vertexDeniedResolver struct{}

func (vertexDeniedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
}

func TestVertexProjectAutoGenerationAndDestinationFences(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "replacement", true: "destination"}[denied], func(t *testing.T) {
			var probes, models atomic.Int32
			var f *providerAcceptanceFixture
			f = newProviderAcceptance(t, "google-vertex", providerAcceptanceVertexModel, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "ocx-project-probe") {
					probes.Add(1)
					f.data.Secret = "replacement-fixture"
					f.save()
					w.WriteHeader(404)
					w.Write([]byte(`projects/123/locations/global/`))
					return
				}
				models.Add(1)
				providerAcceptanceResponse(w, providerAcceptanceVertexResponse)
			})
			f.data.Metadata.Configuration = &v1.ProviderConfiguration{VertexProject: proto.String("auto")}
			f.save()
			if denied {
				f.srv.inferenceDestinations.Resolver = vertexDeniedResolver{}
			}
			w := f.call("/v1/chat/completions", `{"model":"fixture","messages":[{"role":"user","content":"fixture"}]}`, nil)
			if w.Code == 200 || models.Load() != 0 {
				t.Fatalf("security failure downgraded: status%d models%d", w.Code, models.Load())
			}
			if denied && probes.Load() != 0 {
				t.Fatal("denied destination probe sent")
			}
		})
	}
}

func TestVertexProjectAutoBudgetAndClaimWait(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(map[bool]string{false: "budget", true: "claim-wait-cancellation"}[busy], func(t *testing.T) {
			var sends atomic.Int32
			f := newProviderAcceptance(t, "google-vertex", providerAcceptanceVertexModel, func(http.ResponseWriter, *http.Request) { sends.Add(1) })
			f.data.Metadata.Configuration = &v1.ProviderConfiguration{VertexProject: proto.String("auto")}
			f.save()
			a := &inferenceAdapter{s: f.s, telemetry: f.srv.telemetry}
			ctx := context.Background()
			p, release, err := a.Admit(ctx, f.key)
			acceptanceOK(t, err)
			defer release()
			route, err := a.Resolve(ctx, p, "fixture", inference.Generate)
			acceptanceOK(t, err)
			credential, err := a.Select(ctx, p, route.Targets[0], "")
			acceptanceOK(t, err)
			if busy {
				lease, err := a.current(p)
				acceptanceOK(t, err)
				_, _, err = f.s.ClaimVertexProject(ctx, lease, credential.AccountID, credential.CredentialVersion)
				acceptanceOK(t, err)
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			}
			ctx = inference.WithPhysicalSendBudget(ctx, func(context.Context, func(context.Context) error) error { return inference.ErrUnavailable })
			_, _, err = a.negotiateVertexProject(ctx, p, route.Targets[0], credential, *f.srv.inferenceDestinations)
			if err == nil || sends.Load() != 0 {
				t.Fatalf("refused probe fell through: %v sends%d", err, sends.Load())
			}
		})
	}
}
