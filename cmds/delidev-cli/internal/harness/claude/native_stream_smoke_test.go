package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// This opt-in test runs only the explicitly selected native binary. Its account,
// home, workspace and scripted provider are disposable; no user login is read.
// It validates transport observations, not public execution/account readiness.
func TestManualNativeStreamRetainsInputIdentityAndResumesHistory(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("set DELIDEV_NATIVE_CLAUDE_EXECUTABLE for isolated native acceptance")
	}
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bare Claude also checks its native provider health path. This fixture
		// deliberately does not implement or forward that optional operation.
		if r.Method == http.MethodHead && r.URL.Path == "/api/hello" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != "private-fixture-only" {
			t.Error("unexpected native provider operation or authority")
			http.Error(w, "unsupported", http.StatusBadRequest)
			return
		}
		n := requests.Add(1)
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxStreamFrame+1))
		if err != nil || len(raw) > maxStreamFrame {
			t.Error("native provider body exceeded its bound")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body struct {
			Model        string          `json:"model"`
			Stream       bool            `json:"stream"`
			System       json.RawMessage `json:"system"`
			OutputConfig struct {
				Effort string `json:"effort"`
			} `json:"output_config"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if json.Unmarshal(raw, &body) != nil || body.Model != "fixture-model" || !body.Stream || body.OutputConfig.Effort != "high" || !bytes.Contains(body.System, []byte("Private fixture instructions.")) || !bytes.Contains(body.System, []byte("Claude Code")) {
			t.Error("native model, effort or additive base instructions changed")
		}
		expected := []string{"Fixture request 1."}
		if n == 2 {
			expected = append(expected, "Fixture response 1.", "Fixture request 2.")
		} else if n != 1 {
			t.Error("native provider operation was repeated")
		}
		if len(body.Messages) != len(expected) {
			t.Error("native resume lost or duplicated conversation history")
		} else {
			for i, text := range expected {
				role := "user"
				if i%2 != 0 {
					role = "assistant"
				}
				if body.Messages[i].Role != role || nativeFixtureProviderText(body.Messages[i].Content, i == 0) != text {
					t.Error("native resume changed conversation identity or content")
				}
			}
		}
		nativeFixtureTextResponse(w, n)
	}))
	defer provider.Close()
	cfg, logs := fixtureConfig(t, "native-stream")
	env, err := probeEnvironment(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i, value := range env {
		if strings.HasPrefix(value, "ANTHROPIC_BASE_URL=") {
			env[i] = "ANTHROPIC_BASE_URL=" + provider.URL
		}
	}
	env = append(env, "ANTHROPIC_API_KEY=private-fixture-only", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1", "CLAUDE_CODE_RESUME_INTERRUPTED_TURN=0", "CLAUDE_CODE_PROJECT_DIR_NAME=fixture")
	workspace := filepath.Join(cfg.Process.Cwd, "workspace")
	if err := security.PrivateDir(workspace); err != nil {
		t.Fatal(err)
	}
	instructions := filepath.Join(cfg.Process.Cwd, "instructions.txt")
	if err := os.WriteFile(instructions, []byte("Private fixture instructions."), 0600); err != nil {
		t.Fatal(err)
	}
	session := domain.NewID()
	for turn := 1; turn <= 2; turn++ {
		t.Run(fmt.Sprintf("turn-%d", turn), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			args := []string{"--bare", "--print", "--input-format=stream-json", "--output-format=stream-json", "--verbose", "--setting-sources=", "--strict-mcp-config", `--mcp-config={"mcpServers":{}}`, "--permission-mode=default", "--permission-prompt-tool=stdio", "--no-chrome", "--disable-slash-commands", "--replay-user-messages", "--include-partial-messages", "--model=fixture-model", "--effort=high", "--append-system-prompt-file=" + instructions}
			if turn == 1 {
				args = append(args, "--session-id="+string(session))
			} else {
				args = append(args, "--resume="+string(session))
			}
			owner := domain.NewID()
			s, err := StartStream(ctx, process.Config{Directory: cfg.Process.Directory, OwnerID: owner, Executable: binary, Env: env, Cwd: workspace, Args: args, Logger: cfg.Process.Logger})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
				if err := process.ReconcileOwner(cfg.Process.Directory, owner); err != nil {
					t.Error(err)
				}
			}()
			r, err := s.Call(ctx, domain.NewID(), map[string]any{"subtype": "initialize", "hooks": nil})
			if err != nil || r.Failed {
				t.Fatal("native initialization failed", err)
			}
			input := domain.NewID()
			prompt := fmt.Sprintf("Fixture request %d.", turn)
			if err := s.SendInput(ctx, input, session, prompt); err != nil {
				t.Fatal(err)
			}
			queued, started, initialized, replayed, answered := false, false, false, false, false
			for {
				event, err := s.Next(ctx)
				if err != nil || event.Kind != NativeMessage {
					t.Fatal("unexpected native stream outcome", err)
				}
				var message struct {
					Type       string    `json:"type"`
					Subtype    string    `json:"subtype"`
					Session    domain.ID `json:"session_id"`
					UUID       string    `json:"uuid"`
					Command    domain.ID `json:"command_uuid"`
					Input      domain.ID `json:"user_message_uuid"`
					State      string    `json:"state"`
					Version    string    `json:"claude_code_version"`
					Model      string    `json:"model"`
					Cwd        string    `json:"cwd"`
					Permission string    `json:"permissionMode"`
					Replay     bool      `json:"isReplay"`
					Error      bool      `json:"is_error"`
					Result     string    `json:"result"`
					Terminal   string    `json:"terminal_reason"`
					Stop       string    `json:"stop_reason"`
					Message    struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"message"`
				}
				if json.Unmarshal(event.Body, &message) != nil || message.Session != session {
					t.Fatal("native session identity changed")
				}
				switch event.Type {
				case "command_lifecycle":
					if message.Command != input {
						t.Fatal("native lifecycle changed input identity")
					}
					if message.State == "queued" && !queued {
						queued = true
					} else if message.State == "started" && queued && !started {
						started = true
					} else {
						t.Fatal("native input lifecycle was reordered or repeated")
					}
				case "system":
					if message.Subtype == "init" {
						if initialized || message.Version != SupportedVersion || message.Model != "fixture-model" || message.Cwd != workspace || message.Permission != "default" {
							t.Fatal("effective native settings changed")
						}
						initialized = true
					}
				case "user":
					if replayed || message.UUID != string(input) || !message.Replay || message.Message.Role != "user" || nativeFixtureText(message.Message.Content) != prompt {
						t.Fatal("native user acceptance changed identity or content")
					}
					replayed = true
				case "assistant":
					if answered || message.Message.Role != "assistant" || nativeFixtureText(message.Message.Content) != fmt.Sprintf("Fixture response %d.", turn) {
						t.Fatal("native assistant output changed")
					}
					answered = true
				case "result":
					if !queued || !started || !initialized || !replayed || !answered || message.Subtype != "success" || message.Error || message.Input != input || message.Terminal != "completed" || message.Stop != "end_turn" || message.Result != fmt.Sprintf("Fixture response %d.", turn) {
						t.Fatal("native result lacks exact completed input evidence")
					}
					return
				case "stream_event":
				default:
					t.Fatal("unvalidated native event family")
				}
			}
		})
		if t.Failed() {
			break
		}
	}
	if requests.Load() != 2 {
		t.Error("native continuation did not issue exactly two provider requests")
	}
	for _, private := range []string{"private-fixture-only", "Fixture request", "Fixture response", "Private fixture instructions"} {
		if strings.Contains(logs.String(), private) {
			t.Error("private native content entered process logs")
		}
	}
}

func nativeFixtureText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil || len(blocks) != 1 || blocks[0].Type != "text" {
		return ""
	}
	return blocks[0].Text
}

func nativeFixtureProviderText(raw json.RawMessage, initial bool) string {
	if text := nativeFixtureText(raw); text != "" {
		return text
	}
	// Native Claude prepends its date context to the initial provider message.
	// Preserve that native context rather than confusing it with a second input.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if !initial || json.Unmarshal(raw, &blocks) != nil || len(blocks) != 2 || blocks[0].Type != "text" || blocks[1].Type != "text" || !strings.HasPrefix(blocks[0].Text, "<system-reminder>\n") || !strings.HasSuffix(blocks[0].Text, "</system-reminder>\n\n") {
		return ""
	}
	return blocks[1].Text
}

func nativeFixtureTextResponse(w http.ResponseWriter, n int64) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range []map[string]any{
		{"type": "message_start", "message": map[string]any{"id": fmt.Sprintf("msg_fixture_%d", n), "type": "message", "role": "assistant", "content": []any{}, "model": "fixture-model", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
		{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
		{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": fmt.Sprintf("Fixture response %d.", n)}},
		{"type": "content_block_stop", "index": 0},
		{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 2}},
		{"type": "message_stop"},
	} {
		raw, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
	}
}
