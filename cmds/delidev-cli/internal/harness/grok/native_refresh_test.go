// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// This opt-in rejected-profile probe uses a synthetic loopback IdP and expired
// dummy tokens. It bypasses the managed constructor only to observe the pinned
// binary's refresh primitive. It grants no enterprise/foreign issuer support,
// actual account acceptance or production authentication admission.
func TestManualNativeGrokUncertainRefreshIsNotAnAcceptedManagedProfile(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit isolated native Grok binary required")
	}
	config, _ := fixtureManagedProfile(t)
	var issuer string
	var sends atomic.Uint32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys", "userinfo_endpoint": issuer + "/userinfo", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"ES256"}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": config.Model, "model": config.Model, "name": config.ModelName, "context_window": config.ContextTokens, "api_backend": "chat_completions", "supports_backend_search": false, "agent_type": "grok-build-plan"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/token":
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if r.ParseForm() != nil || r.Form.Get("refresh_token") != "fixture-managed-refresh-token" || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("client_id") != subscription.GrokClientID {
				t.Error("rejected-profile probe changed its synthetic token authority")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			sends.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"temporarily_unavailable"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer provider.Close()
	issuer = provider.URL
	auth, _, err := subscription.ParseGrok(config.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	auth.Issuer, auth.Created, auth.Expires = issuer, time.Now().UTC().Add(-2*time.Hour), time.Now().UTC().Add(-time.Hour)
	rejected, _ := json.Marshal(map[string]subscription.GrokAuth{issuer + "::" + subscription.GrokClientID: auth})
	defer clear(rejected)
	changed := config
	changed.Bundle = rejected
	if _, err := buildManagedProfile(changed); err == nil {
		t.Fatal("foreign/expired rejected probe acquired managed admission")
	}
	profile, err := buildManagedProfile(config)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := probeEnvironment(config.Probe)
	if err != nil {
		t.Fatal(err)
	}
	if createNativeLogGuard(config.Probe.Home) != nil || security.WriteAtomicOwned(filepath.Join(config.Probe.Home, "config.toml"), profile.profile.configuration) != nil || security.WriteAtomicOwned(filepath.Join(config.Probe.Home, "auth.json"), rejected) != nil {
		t.Fatal("isolated rejected-profile setup failed")
	}
	environment, err = managedEnvironment(environment, issuer+"/v1", config.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	// These test-only overrides never enter a managed constructor or Worker.
	environment = append(environment, "GROK_OAUTH2_ISSUER="+issuer, "GROK_OAUTH2_CLIENT_ID="+subscription.GrokClientID)
	process := config.Probe.Process
	process.Executable, process.Env, process.Args = binary, environment, []string{"--no-auto-update", "agent", "stdio"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	wire, err := nativewire.StartJSONRPC(ctx, process)
	if err != nil {
		t.Fatal(err)
	}
	defer wire.Close()
	response, err := wire.Call(ctx, domain.NewID(), "initialize", initializeParams{ProtocolVersion: 1, ClientInfo: clientInfo{Name: "delidev", Title: "DeliDev", Version: "0.1.0"}})
	var initial struct {
		Meta struct {
			Version string `json:"agentVersion"`
		} `json:"_meta"`
	}
	if err != nil || response.ErrorCode != nil || json.Unmarshal(response.Result, &initial) != nil || initial.Meta.Version != SupportedVersion {
		t.Fatal("rejected-profile probe did not bind the exact native pin")
	}
	_, err = wire.Call(ctx, domain.NewID(), "authenticate", map[string]any{"methodId": "cached_token", "_meta": map[string]bool{"headless": true}})
	if err != nil {
		t.Fatal("rejected-profile authentication observation was uncertain", err)
	}
	if wire.Close() != nil {
		t.Fatal("rejected-profile native cleanup was not joined")
	}
	if sends.Load() <= 1 {
		t.Fatal("the pinned native refresh behavior changed; revalidate its complete managed profile")
	}
	retained, err := security.ReadPrivate(filepath.Join(config.Probe.Home, "auth.json"), subscription.MaxBundle)
	defer clear(retained)
	if err != nil || !bytes.Equal(rejected, retained) || checkNativeLogGuard(filepath.Join(config.Probe.Home, "logs")) != nil {
		t.Fatal("rejected-profile probe changed authentication or retained native logs")
	}
	t.Logf("rejected native profile: version=%s, synthetic refresh sends=%d; managed admission remains unaccepted", SupportedVersion, sends.Load())
}
