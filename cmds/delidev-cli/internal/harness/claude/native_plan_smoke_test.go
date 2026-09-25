package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

type nativeFixtureToolResult struct {
	Type    string          `json:"type"`
	Tool    string          `json:"tool_use_id"`
	Error   bool            `json:"is_error"`
	Content json.RawMessage `json:"content"`
}

func nativeFixtureToolResults(t *testing.T, raw []byte) map[string]nativeFixtureToolResult {
	t.Helper()
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	results := map[string]nativeFixtureToolResult{}
	if json.Unmarshal(raw, &body) != nil {
		t.Error("invalid native message history")
		return results
	}
	for _, message := range body.Messages {
		if message.Role != "user" {
			continue
		}
		var blocks []nativeFixtureToolResult
		if json.Unmarshal(message.Content, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type != "tool_result" {
				continue
			}
			if _, exists := results[block.Tool]; exists || block.Tool == "" {
				t.Error("ambiguous native tool result")
			}
			results[block.Tool] = block
		}
	}
	return results
}

func nativeFixtureToolResponse(w http.ResponseWriter, sequence int64, name, id string, input any) {
	params, _ := json.Marshal(input)
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range []map[string]any{
		{"type": "message_start", "message": map[string]any{"id": fmt.Sprintf("msg_fixture_%d", sequence), "type": "message", "role": "assistant", "content": []any{}, "model": "fixture-model", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
		{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}},
		{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(params)}},
		{"type": "content_block_stop", "index": 0},
		{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 2}},
		{"type": "message_stop"},
	} {
		raw, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
	}
}

func TestManualNativePlanQuestionArtifactAndApproval(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	cfg, logs := apiFixtureConfig(t, "native-plan")
	cfg.Process.Executable = binary
	cfg.Model = "fixture-model"
	const question = "Which fixture approach should the plan use?"
	const plan = "# Context\nImplement the private fixture with the selected first approach.\n\n# Steps\n1. Apply the selected fixture change.\n2. Verify the fixture.\n"
	var requests atomic.Int64
	var artifact atomic.Value
	artifact.Store("")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || r.Header.Get("Authorization") != "" {
			t.Error("native plan authority changed")
			w.WriteHeader(400)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxStreamFrame+1))
		if err != nil || len(raw) > maxStreamFrame {
			t.Error("invalid native plan request")
			w.WriteHeader(400)
			return
		}
		n := requests.Add(1)
		results := nativeFixtureToolResults(t, raw)
		switch n {
		case 1:
			var body struct {
				Model    string `json:"model"`
				Messages []struct {
					Content json.RawMessage `json:"content"`
				} `json:"messages"`
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			}
			if json.Unmarshal(raw, &body) != nil || body.Model != cfg.Model {
				t.Error("invalid native plan prompt")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			tools := map[string]bool{}
			for _, tool := range body.Tools {
				tools[tool.Name] = true
			}
			for _, name := range []string{"AskUserQuestion", "Write", "ExitPlanMode", "EnterPlanMode", "Agent"} {
				if !tools[name] {
					t.Error("full native tool is unavailable", name)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
			}
			var text strings.Builder
			for _, message := range body.Messages {
				text.WriteString(nativeFixtureText(message.Content))
			}
			matches := regexp.MustCompile(`You should create your plan at (.+?) using the Write tool\.`).FindAllStringSubmatch(text.String(), -1)
			if len(matches) != 1 {
				t.Error("native Plan context did not identify one plan file")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			path := matches[0][1]
			if filepath.Dir(path) != filepath.Join(cfg.Home, "plans") || filepath.Ext(path) != ".md" {
				t.Error("native plan file left the private runtime")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			artifact.Store(path)
			nativeFixtureToolResponse(w, n, "AskUserQuestion", "toolu_plan_question", map[string]any{"questions": []any{map[string]any{"question": question, "header": "Approach", "multiSelect": false, "options": []any{map[string]any{"label": "First", "description": "Use the first fixture approach."}, map[string]any{"label": "Second", "description": "Use the second fixture approach."}}}}})
		case 2:
			answer, exists := results["toolu_plan_question"]
			if !exists || answer.Error || !strings.Contains(nativeFixtureText(answer.Content), "First") {
				t.Error("native question answer was not accepted")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			nativeFixtureToolResponse(w, n, "Write", "toolu_plan_write", map[string]any{"file_path": artifact.Load().(string), "content": plan})
		case 3:
			write, exists := results["toolu_plan_write"]
			if !exists || write.Error {
				t.Error("native plan write failed")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			contents, err := os.ReadFile(artifact.Load().(string))
			if err != nil || string(contents) != plan {
				t.Error("native plan artifact changed")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			nativeFixtureToolResponse(w, n, "ExitPlanMode", "toolu_plan_exit", map[string]any{})
		case 4:
			exit, exists := results["toolu_plan_exit"]
			if !exists || exit.Error || !strings.Contains(nativeFixtureText(exit.Content), "approved") {
				t.Error("native plan approval was not accepted")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			nativeFixtureTextResponse(w, n)
		default:
			t.Error("native plan repeated a provider request")
			w.WriteHeader(400)
		}
	}))
	defer provider.Close()
	authority := nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Plan fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}
	relay := httptest.NewServer(apiproxy.New(authority, slog.New(slog.NewJSONHandler(io.Discard, nil))))
	defer relay.Close()
	cfg.API.ServerOrigin = relay.URL
	s, err := OpenAPIStream(ctx, cfg)
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
		if strings.Contains(logs.String(), nativeAPIFixtureToken) || strings.Contains(logs.String(), question) || strings.Contains(logs.String(), plan) {
			t.Error("private native plan data entered logs")
		}
	}()
	input := domain.NewID()
	if err := s.SendInput(ctx, input, cfg.SessionID, "Plan the private fixture change."); err != nil {
		t.Fatal(err)
	}
	answered, approved := false, false
	for {
		event, err := s.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == NativeRequest {
			var request struct {
				Subtype string                     `json:"subtype"`
				Tool    string                     `json:"tool_name"`
				ID      string                     `json:"tool_use_id"`
				Input   map[string]json.RawMessage `json:"input"`
			}
			if json.Unmarshal(event.Body, &request) != nil || request.Subtype != "can_use_tool" {
				t.Fatal("unknown native plan interaction")
			}
			switch request.Tool {
			case "AskUserQuestion":
				if answered || request.ID != "toolu_plan_question" || !strings.Contains(string(request.Input["questions"]), question) {
					t.Fatal("native question changed")
				}
				request.Input["answers"], _ = json.Marshal(map[string]string{question: "First"})
				answered = true
			case "ExitPlanMode":
				if !answered || approved || request.ID != "toolu_plan_exit" {
					t.Fatal("native plan approval changed")
				}
				var content, path string
				if json.Unmarshal(request.Input["plan"], &content) != nil || content != plan || json.Unmarshal(request.Input["planFilePath"], &path) != nil || path != artifact.Load().(string) {
					t.Fatal("native approval did not retain the exact written plan")
				}
				approved = true
			default:
				t.Fatal("unexpected native permission callback", request.Tool)
			}
			if err := s.Reply(ctx, event, map[string]any{"behavior": "allow", "updatedInput": request.Input}); err != nil {
				t.Fatal(err)
			}
		}
		if event.Kind == NativeMessage && event.Type == "result" {
			var result struct {
				Session  domain.ID `json:"session_id"`
				Input    domain.ID `json:"user_message_uuid"`
				Error    bool      `json:"is_error"`
				Terminal string    `json:"terminal_reason"`
			}
			if json.Unmarshal(event.Body, &result) != nil || result.Session != cfg.SessionID || result.Input != input || result.Error || result.Terminal != "completed" || !answered || !approved || requests.Load() != 4 {
				t.Fatal("native plan did not complete its original input")
			}
			return
		}
	}
}
