package opencode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func protectedFixtureForm(secret, form string) any {
	switch form {
	case "escaped":
		raw, _ := json.Marshal(secret)
		const d = "0123456789abcdef"
		b := secret[0]
		return json.RawMessage(`"\u00` + string([]byte{d[b>>4], d[b&15]}) + string(raw[2:]))
	case "base64":
		return base64.StdEncoding.EncodeToString([]byte(secret))
	case "raw-base64":
		return base64.RawStdEncoding.EncodeToString([]byte(secret))
	case "url-base64":
		return base64.URLEncoding.EncodeToString([]byte(secret))
	case "raw-url-base64":
		return base64.RawURLEncoding.EncodeToString([]byte(secret))
	default:
		return secret
	}
}
func TestProtectedNativeEventsAndSnapshotsRetainOriginalAuthority(t *testing.T) {
	for _, secretKind := range []string{"token", "password", "caller"} {
		for _, form := range []string{"literal", "escaped", "base64", "raw-base64", "url-base64", "raw-url-base64"} {
			t.Run(secretKind+"-"+form, func(t *testing.T) {
				profile := fixtureAPIProfile()
				password := "private-original-http-password"
				caller := "additional-protected-fixture"
				secret := profile.Token
				if secretKind == "password" {
					secret = password
				}
				if secretKind == "caller" {
					secret = caller
				}
				var logs bytes.Buffer
				api := &sessionAPI{password: password, cwd: fixtureWorkspacePath(), apiProfile: profile, protectedValues: []string{caller}, gate: make(chan struct{}, 1), owner: domain.NewID(), alive: func() error { return nil }, logger: slog.New(slog.NewJSONHandler(&logs, nil))}
				requests := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					user, actual, ok := r.BasicAuth()
					if !ok || user != "delidev" || actual != password || r.Header.Get("x-opencode-directory") != api.cwd {
						t.Error("original native authority changed")
						w.WriteHeader(400)
						return
					}
					if r.URL.Path == "/event" {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, eventFrame(1, ServerConnectedEvent, map[string]any{}))
						w.(http.Flusher).Flush()
						_, _ = io.WriteString(w, eventFrame(2, MessagePartDeltaEvent, map[string]any{"sessionID": fixtureSessionID, "messageID": fixtureMessageID, "partID": fixturePartID, "field": "text", "delta": protectedFixtureForm(secret, form)}))
						w.(http.Flusher).Flush()
						<-r.Context().Done()
						return
					}
					requests++
					w.Header().Set("Content-Type", "application/json")
					value := fixtureSession(api.cwd, domain.NewID(), fixtureSettings())
					value["title"] = protectedFixtureForm(secret, form)
					_ = json.NewEncoder(w).Encode(value)
				}))
				defer server.Close()
				api.origin = server.URL
				client, transport := probeHTTPClient(strings.TrimPrefix(server.URL, "http://"))
				defer transport.CloseIdleConnections()
				api.client = client
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				events, err := api.connectEvents(ctx, ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				event, err := events.Next(ctx)
				if err == nil || domain.SafeError(err).Code != domain.Unsupported || len(event.Properties) != 0 {
					t.Fatal("protected event retained", err)
				}
				events.Close()
				if len(events.seen) != 1 || events.pending != 0 {
					t.Fatal("refused event retained identity or bytes")
				}
				api.events = nil
				api.runtimeRead = true
				for _, path := range []string{"/config", "/provider"} {
					raw, _, err := api.request(ctx, http.MethodGet, path, nil, http.StatusOK)
					if err != nil || len(raw) == 0 {
						t.Fatal("private configuration verification lost exact credential", err)
					}
				}
				api.runtimeRead = false
				api.creation = &sessionCreation{request: domain.NewID(), settings: fixtureSettings(), identity: sessionIdentity{id: fixtureSessionID}}
				raw, _, err := api.request(ctx, http.MethodGet, "/session/"+fixtureSessionID, nil, http.StatusOK)
				if err == nil || domain.SafeError(err).Code != domain.Unsupported || len(raw) != 0 || requests != 3 {
					t.Fatal("protected snapshot exposed or retried", err, requests)
				}
				if strings.Contains(logs.String(), secret) {
					t.Fatal("protected data entered diagnostics")
				}
			})
		}
	}
}
