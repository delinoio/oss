// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
)

func TestManualNativeOwnedProxyNormalAndTitleProfile(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native binary and isolated scripted provider only")
	}
	for _, title := range []bool{false, true} {
		t.Run(fmt.Sprintf("title=%t", title), func(t *testing.T) {
			const upstreamSecret = "fixture-native-upstream-proxy-sentinel"
			var requests, tunnels, observations atomic.Int32
			provider, _ := outboundtest.TLSOrigin(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != apiproxy.Prefix+"/responses" || r.Header.Get("Authorization") != "Bearer "+apiFixtureToken() || r.Header.Get("Proxy-Authorization") != "" {
					t.Error("native route changed original API authority")
					http.Error(w, "denied", 403)
					return
				}
				raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				if err != nil || !strings.Contains(string(raw), "fixture-model") || strings.Contains(string(raw), upstreamSecret) || strings.Contains(string(raw), apiFixtureToken()) {
					t.Error("native body changed or reflected credentials")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, event := range []any{
					map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_owned_proxy_fixture", "status": "in_progress"}},
					map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_owned_proxy_fixture", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Owned proxy fixture complete."}}}},
					map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_owned_proxy_fixture", "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
				} {
					raw, _ := json.Marshal(event)
					_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
				}
			}))
			defer provider.Close()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tunnels.Add(1)
				origin, _ := url.Parse(provider.URL)
				if r.Method != http.MethodConnect || r.Host != origin.Host || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("fixture:"+upstreamSecret)) {
					t.Error("upstream received foreign authority")
					http.Error(w, "denied", 407)
					return
				}
				remote, err := net.DialTimeout("tcp", r.Host, time.Second)
				if err != nil {
					t.Error(err)
					http.Error(w, "unavailable", 502)
					return
				}
				defer remote.Close()
				local, buffered, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer local.Close()
				_, _ = io.WriteString(local, "HTTP/1.1 200 Connection Established\r\n\r\n")
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(remote, buffered); done <- struct{}{} }()
				go func() { _, _ = io.Copy(local, remote); done <- struct{}{} }()
				<-done
				local.Close()
				remote.Close()
				<-done
			}))
			defer upstream.Close()
			u, _ := url.Parse(upstream.URL)
			port, _ := strconv.ParseUint(u.Port(), 10, 16)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			proxy, err := nativeproxy.Open(ctx, nativeproxy.Config{Origin: provider.URL, Profile: domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Fixture", Mode: domain.ProxyHTTP, Host: u.Hostname(), Port: uint16(port)}, CredentialGeneration: domain.NewID()}, Credential: &domain.ProxyCredential{Username: "fixture", Password: upstreamSecret}, Observed: func(context.Context) { observations.Add(1) }})
			if err != nil {
				t.Fatal(err)
			}
			defer proxy.Close()
			cfg := nativeFixtureConfig(t, binary, "http://127.0.0.1:1")
			caPath := filepath.Join(filepath.Dir(cfg.Home), "config", "fixture-ca.pem")
			if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: provider.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			cfg.Process.Env = append(cfg.Process.Env, "CODEX_CA_CERTIFICATE="+caPath)
			cfg.Process.ProtectedValues = proxy.ProtectedValues()
			cfg.API = &APIConfig{ServerOrigin: provider.URL, Token: apiFixtureToken(), TitleProfile: title, LoopbackProxyURL: proxy.NativeURL()}
			client, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			settings := ThreadSettings{Model: "fixture-model", Provider: APIProvider, Effort: "high", Cwd: cfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
			if _, err := client.StartThread(ctx, domain.NewID(), settings); err != nil {
				t.Fatal(err)
			}
			if _, err := client.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Return the isolated scripted response only."}); err != nil {
				t.Fatal(err)
			}
			for {
				event, err := client.NextEvent(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if event.Kind == TurnCompletedEvent {
					if event.Turn == nil || event.Turn.Status != TurnCompleted || !event.Correlated {
						t.Fatal("native turn did not complete")
					}
					break
				}
			}
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			if err := proxy.Close(); err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 1 || tunnels.Load() != 1 || observations.Load() != 1 {
				t.Fatal("native proxy route was unused, retried or duplicated")
			}
			protected := append(proxy.ProtectedValues(), upstreamSecret, apiFixtureToken())
			if err := filepath.WalkDir(filepath.Dir(cfg.Home), func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				info, err := entry.Info()
				if err != nil || !info.Mode().IsRegular() {
					return err
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				for _, value := range protected {
					if strings.Contains(string(raw), value) {
						t.Error("native runtime persisted transient authority")
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			t.Logf("Codex %s: one native TLS request through the owned authenticated loopback tunnel and original upstream proxy; normal/title=%t; isolated scripted provider, no external account", SupportedVersion, title)
		})
	}
}
