package opencode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestHTTPReadKeepsAuthenticationOnOriginalOwnedAuthority(t *testing.T) {
	var calls, foreignCalls atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignCalls.Add(1); w.WriteHeader(200) }))
	defer foreign.Close()
	t.Setenv("HTTP_PROXY", foreign.URL)
	t.Setenv("HTTPS_PROXY", foreign.URL)
	t.Setenv("ALL_PROXY", foreign.URL)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		name, password, ok := r.BasicAuth()
		if !ok || name != "delidev" || password != "private-password-sentinel" || r.URL.RawQuery != "" || r.Method != "GET" {
			t.Error("changed native read/authentication")
		}
		w.Header().Set("Location", foreign.URL+"/credential-trap")
		w.WriteHeader(307)
	}))
	defer server.Close()
	client, transport := probeHTTPClient(strings.TrimPrefix(server.URL, "http://"))
	defer transport.CloseIdleConnections()
	_, err := readHTTP(context.Background(), client, server.URL, "/global/health", "private-password-sentinel", 200)
	if err == nil || calls.Load() != 1 || foreignCalls.Load() != 0 {
		t.Fatalf("redirect/proxy/retry escaped: %v %d %d", err, calls.Load(), foreignCalls.Load())
	}
	if strings.Contains(err.Error(), "sentinel") || strings.Contains(err.Error(), server.URL) {
		t.Fatal("native error exposed credentials or URL")
	}
	_, err = readHTTP(context.Background(), client, server.URL, "/session", "private-password-sentinel", 200)
	if err == nil || calls.Load() != 1 {
		t.Fatal("discovery invoked a project endpoint")
	}
	_, err = readHTTP(context.Background(), client, foreign.URL, "/doc", "private-password-sentinel", 200)
	if err == nil || foreignCalls.Load() != 0 {
		t.Fatal("HTTP client sent credentials to another authority")
	}
}

func TestHTTPReadRejectsMalformedBoundedResponses(t *testing.T) {
	for _, mode := range []string{"body", "length", "content-type", "compression", "challenge", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "body":
					w.(http.Flusher).Flush()
					_, _ = io.WriteString(w, strings.Repeat("x", maxHTTPBody+1))
				case "length":
					w.Header().Set("Content-Length", "1048577")
				case "content-type":
					w.Header().Set("Content-Type", "text/html")
					_, _ = io.WriteString(w, "private-sentinel")
				case "compression":
					w.Header().Set("Content-Encoding", "gzip")
					_, _ = io.WriteString(w, "private-sentinel")
				case "challenge":
					w.Header().Set("Www-Authenticate", `Bearer realm="foreign"`)
					w.WriteHeader(401)
				case "timeout":
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			client, transport := probeHTTPClient(strings.TrimPrefix(server.URL, "http://"))
			defer transport.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			status := 200
			if mode == "challenge" {
				status = 401
			}
			_, err := readHTTP(ctx, client, server.URL, "/global/health", "private-password-sentinel", status)
			if err == nil || strings.Contains(err.Error(), "sentinel") {
				t.Fatal("invalid response accepted or exposed")
			}
			if (mode == "body" || mode == "length") && domain.SafeError(err).Code != domain.ResourceExhausted {
				t.Fatal("body bound lost", err)
			}
		})
	}
}

func TestHealthAndConfigurationRequireExactFacts(t *testing.T) {
	for _, raw := range []string{`null`, `{"healthy":true,"version":"invalid/version"}`, `{"healthy":false,"version":"1.18.32"}`, `{"healthy":null,"version":"1.18.32"}`, `{"healthy":true,"Version":"1.18.32"}`, `{"healthy":true,"version":"1.18.32","version":"1.18.32"}`, `{"healthy":true,"version":"1.18.32","future":true}`} {
		if validateHealth([]byte(raw)) == nil {
			t.Fatal("invalid health accepted", raw)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"plugin":[]}`, `{} {}`} {
		if validateEmptyConfig([]byte(raw)) == nil {
			t.Fatal("inherited/ambiguous config accepted")
		}
	}
}

func TestSchemaAdvertisementsCannotLoseNativeOperationIdentity(t *testing.T) {
	if err := validateSchema(schemaFixture); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"version", "paths", "method", "identity", "status", "media", "body", "null-schema", "alias", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			var doc map[string]any
			_ = json.Unmarshal(schemaFixture, &doc)
			paths := doc["paths"].(map[string]any)
			op := paths["/session/{sessionID}/prompt_async"].(map[string]any)["post"].(map[string]any)
			switch mode {
			case "version":
				doc["openapi"] = "3.0.0"
			case "paths":
				delete(paths, "/session/{sessionID}/fork")
			case "method":
				delete(paths["/question/{requestID}/reply"].(map[string]any), "post")
			case "identity":
				op["operationId"] = "session.prompt"
			case "status":
				op["responses"] = map[string]any{"200": map[string]any{}}
			case "body":
				op["responses"].(map[string]any)["204"].(map[string]any)["content"] = map[string]any{}
			case "alias":
				op["OperationID"] = op["operationId"]
				delete(op, "operationId")
			case "media", "null-schema":
				response := paths["/global/health"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)
				if mode == "media" {
					response["content"] = map[string]any{"text/html": map[string]any{"schema": map[string]any{}}}
				} else {
					response["content"].(map[string]any)["application/json"].(map[string]any)["schema"] = nil
				}
			}
			raw, _ := json.Marshal(doc)
			if mode == "duplicate" {
				raw = []byte(strings.Replace(string(raw), `"openapi":"3.1.0"`, `"openapi":"3.1.0","openapi":"3.1.0"`, 1))
			}
			if validateSchema(raw) == nil {
				t.Fatal("changed native advertisement accepted")
			}
		})
	}
}
