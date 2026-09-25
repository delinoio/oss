package claude

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

var nativeAPIFixtureToken = apiproxy.TokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{17}, 32))

const nativeAPIUpstreamKey = "server-only-private-fixture"

type nativeAPIAuthority struct {
	scope apiproxy.Scope
	ctx   context.Context
}

func (a nativeAPIAuthority) Acquire(ctx context.Context, token string) (*apiproxy.Lease, error) {
	if token != nativeAPIFixtureToken || ctx.Err() != nil {
		return nil, domain.Fail(domain.Unauthenticated, "Invalid fixture authority.", "")
	}
	return &apiproxy.Lease{Scope: a.scope, Context: a.ctx, Release: func() {}, Key: func(context.Context) ([]byte, error) { return []byte(nativeAPIUpstreamKey), nil }}, nil
}

func TestManualNativeAPIChildEnvironment(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	const command = `if test -z "${ANTHROPIC_API_KEY-}" && test -z "${ANTHROPIC_AUTH_TOKEN-}"; then printf 'isolated'; else printf 'inherited'; fi`
	var requests atomic.Int64
	var toolResult atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.URL.RawQuery != "beta=true" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || r.Header.Get("Authorization") != "" {
			t.Error("unexpected native authority or provider operation")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		n := requests.Add(1)
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxStreamFrame+1))
		if err != nil || len(raw) > maxStreamFrame {
			t.Error("invalid native provider body")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var settings struct {
			Model  string          `json:"model"`
			System json.RawMessage `json:"system"`
			Output struct {
				Effort string `json:"effort"`
			} `json:"output_config"`
		}
		if json.Unmarshal(raw, &settings) != nil || settings.Model != "fixture-model" || settings.Output.Effort != "high" || !bytes.Contains(settings.System, []byte("Private fixture instructions.")) || !bytes.Contains(settings.System, []byte("Claude Code")) {
			t.Error("native effective model, effort or additive base instructions changed")
		}
		if n == 1 {
			input, _ := json.Marshal(map[string]any{"command": command, "description": "Check fixture environment isolation"})
			w.Header().Set("Content-Type", "text/event-stream")
			for _, event := range []map[string]any{
				{"type": "message_start", "message": map[string]any{"id": "msg_fixture_tool", "type": "message", "role": "assistant", "content": []any{}, "model": "fixture-model", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
				{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "toolu_fixture_isolation", "name": "Bash", "input": map[string]any{}}},
				{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(input)}},
				{"type": "content_block_stop", "index": 0},
				{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 2}},
				{"type": "message_stop"},
			} {
				data, _ := json.Marshal(event)
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
			}
			return
		}
		if n != 2 {
			t.Error("unexpected provider retry")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		results := nativeFixtureToolResults(t, raw)
		observed, ok := results["toolu_fixture_isolation"]
		if !ok || len(results) != 1 || observed.Error || nativeFixtureText(observed.Content) != "isolated" {
			t.Error("child token isolation was not confirmed")
		} else {
			toolResult.Store(true)
		}

		nativeFixtureTextResponse(w, n)
	}))
	defer provider.Close()
	cfg, logs := fixtureConfig(t, "native-api")
	workspace := filepath.Join(cfg.Process.Cwd, "workspace")
	if err := security.PrivateDir(workspace); err != nil {
		t.Fatal(err)
	}
	session, inputID := domain.NewID(), domain.NewID()
	cfg.Process.Env = append(cfg.Process.Env, "ANTHROPIC_AUTH_TOKEN=foreign-fixture-only", "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1", "ANTHROPIC_MODEL=foreign-model")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// The lease is a fixture; production Worker registration/dispatch is a
	// separate boundary. Forwarding itself uses the real scoped server relay.
	authority := nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: session, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: "fixture-model", Provider: domain.Provider{Name: "Private fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}
	relay := httptest.NewServer(apiproxy.New(authority, slog.New(slog.NewJSONHandler(io.Discard, nil))))
	defer relay.Close()
	config := APIStreamConfig{Process: cfg.Process, Version: SupportedVersion, Home: cfg.Home, Workspace: workspace, SessionID: session, Model: "fixture-model", Effort: "high", Permission: DefaultPermission, Instructions: "Private fixture instructions.", API: APIConfig{ServerOrigin: relay.URL, Token: nativeAPIFixtureToken}}
	config.Process.Executable = binary
	s, err := OpenAPIStream(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		if err := process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
			t.Error(err)
		}
		if strings.Contains(logs.String(), nativeAPIFixtureToken) || strings.Contains(logs.String(), nativeAPIUpstreamKey) || strings.Contains(logs.String(), command) {
			t.Error("private native data entered logs")
		}
		err := filepath.WalkDir(cfg.Process.Cwd, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			raw, err := os.ReadFile(path)
			if bytes.Contains(raw, []byte(nativeAPIFixtureToken)) || bytes.Contains(raw, []byte(nativeAPIUpstreamKey)) {
				t.Error("execution token persisted in private native state")
			}
			return err
		})
		if err != nil {
			t.Error(err)
		}
	}()
	if err := s.SendInput(ctx, inputID, session, "Run the private environment check."); err != nil {
		t.Fatal(err)
	}
	echoed := false
	var original StreamEvent
	callbacks := 0
	for {
		event, err := s.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == NativeRequest {
			var callback struct {
				Subtype string          `json:"subtype"`
				Tool    string          `json:"tool_name"`
				ID      string          `json:"tool_use_id"`
				Input   json.RawMessage `json:"input"`
			}
			var input struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(event.Body, &callback) != nil || callback.Subtype != "can_use_tool" || callback.Tool != "Bash" || callback.ID != "toolu_fixture_isolation" || json.Unmarshal(callback.Input, &input) != nil || input.Command != command {
				t.Fatal("foreign native tool request")
			}
			original = event
			callbacks++
			if callbacks != 1 {
				t.Fatal("duplicate native permission request")
			}
			if err := s.Reply(ctx, event, map[string]any{"behavior": "allow", "updatedInput": callback.Input}); err != nil {
				t.Fatal(err)
			}
		}
		if event.Kind == NativeReplyEcho {
			if echoed || event.ArrivalID != original.ArrivalID || event.RequestID != original.RequestID {
				t.Fatal("foreign or repeated native reply echo")
			}
			echoed = true
		}
		if event.Kind == NativeMessage && event.Type == "result" {
			var result struct {
				Input    domain.ID `json:"user_message_uuid"`
				Session  domain.ID `json:"session_id"`
				Error    bool      `json:"is_error"`
				Terminal string    `json:"terminal_reason"`
				Result   string    `json:"result"`
			}
			if json.Unmarshal(event.Body, &result) != nil || result.Input != inputID || result.Session != session || result.Error || result.Terminal != "completed" || result.Result != "Fixture response 2." {
				t.Fatal("native completion changed original scope")
			}
			if !echoed || !toolResult.Load() || callbacks != 1 || requests.Load() != 2 {
				t.Fatal("missing exact native tool acceptance evidence")
			}
			return
		}
	}
}

func TestManualNativeAPIPreservesPermissionModes(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary required")
	}
	for _, permission := range []NativePermission{DefaultPermission, PlanPermission, AcceptEditsPermission, DontAskPermission, BypassPermission} {
		t.Run(string(permission), func(t *testing.T) {
			cfg, _ := fixtureConfig(t, "native-permission")
			workspace := filepath.Join(cfg.Process.Cwd, "workspace")
			if err := security.PrivateDir(workspace); err != nil {
				t.Fatal(err)
			}
			cfg.Process.Executable = binary
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s, err := OpenAPIStream(ctx, APIStreamConfig{Process: cfg.Process, Version: SupportedVersion, Home: cfg.Home, Workspace: workspace, SessionID: domain.NewID(), Model: "fixture-model", Permission: permission, API: APIConfig{ServerOrigin: "http://127.0.0.1:1", Token: nativeAPIFixtureToken}})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if err := process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
