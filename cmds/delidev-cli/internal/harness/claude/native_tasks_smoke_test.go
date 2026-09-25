package claude

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// This opt-in test invokes only the explicitly selected binary and a scripted
// loopback provider. It cannot use an installed account or external inference.
func TestManualNativeTaskLifecycle(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	cfg, logs := apiFixtureConfig(t, "native-tasks")
	cfg.Process.Executable = binary
	cfg.Model = "fixture-model"
	cfg.Permission = DefaultPermission
	fixturePath := filepath.Join(cfg.Workspace, "child-fixture.txt")
	const fixtureText = "Private native child fixture contents."
	if err := os.WriteFile(fixturePath, []byte(fixtureText), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || r.Header.Get("Authorization") != "" {
			t.Error("native task provider authority changed")
			w.WriteHeader(400)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxStreamFrame+1))
		var body struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err != nil || len(raw) > maxStreamFrame || json.Unmarshal(raw, &body) != nil || body.Model != cfg.Model || !body.Stream {
			t.Error("native task provider request changed")
			w.WriteHeader(400)
			return
		}
		n := calls.Add(1)
		results := nativeFixtureToolResults(t, raw)
		_, hasParent := results["toolu_owned_child"]
		_, hasRead := results["toolu_child_read"]
		t.Log("native request scope", n, "parent-result", hasParent, "read-result", hasRead)
		switch n {
		case 1:
			var tools struct {
				Tools []struct {
					Name   string `json:"name"`
					Schema struct {
						Properties map[string]json.RawMessage `json:"properties"`
					} `json:"input_schema"`
				} `json:"tools"`
			}
			_ = json.Unmarshal(raw, &tools)
			for _, tool := range tools.Tools {
				if tool.Name == "Agent" {
					keys := []string{}
					for key := range tool.Schema.Properties {
						keys = append(keys, key)
					}
					sort.Strings(keys)
					t.Log("native Agent input fields", keys)
				}
			}
			nativeFixtureToolResponse(w, n, "Agent", "toolu_owned_child", map[string]any{"description": "Inspect the private fixture", "prompt": "Return the private child fixture response.", "subagent_type": "general-purpose", "run_in_background": false})
		case 2:
			if len(nativeFixtureToolResults(t, raw)) != 0 {
				t.Error("child request unexpectedly included a completed parent")
			}
			nativeFixtureToolResponse(w, n, "Read", "toolu_child_read", map[string]any{"file_path": fixturePath})
		case 3:
			result, ok := nativeFixtureToolResults(t, raw)["toolu_child_read"]
			if !ok || result.Error || !strings.Contains(nativeFixtureText(result.Content), fixtureText) {
				t.Error("native child did not receive its original file result")
			}
			nativeFixtureTextResponse(w, n)
		case 4:
			result, ok := nativeFixtureToolResults(t, raw)["toolu_owned_child"]
			if !ok || result.Error || !nativeTaskTextContains(result.Content, "Fixture response 3.") {
				t.Error("native parent did not receive its child result")
			}
			nativeFixtureTextResponse(w, n)
		default:
			t.Error("native task requested an additional provider turn", n, "notification", strings.Contains(string(raw), "task-notification"))
			w.WriteHeader(400)
		}
	}))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	authority := nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Task fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}
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
		for _, private := range []string{nativeAPIFixtureToken, nativeAPIUpstreamKey, "Return the private child fixture response.", fixtureText} {
			if strings.Contains(logs.String(), private) {
				t.Error("private task content entered logs")
			}
		}
	}()
	input := domain.NewID()
	const prompt = "Run the owned child fixture."
	binding, err := BindExecution(cfg, input, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SendInput(ctx, input, cfg.SessionID, prompt); err != nil {
		t.Fatal(err)
	}
	historyProofs := map[string][]HistoryMessageProof{}
	var result *NativeResult
	var commandClosed bool
	var childInput, childTool, childReturned, childResult, taskStarted, taskUpdated, taskNotified bool
	for result == nil || !commandClosed {
		event, err := s.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(event.Body, &fields)
		var subtype string
		_ = json.Unmarshal(fields["subtype"], &subtype)
		if event.Type == "system" && subtype != "init" || event.Type == "tool_progress" {
			keys := make([]string, 0, len(fields))
			for key := range fields {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			t.Log("native task envelope", event.Type, subtype, keys)
			if patch := fields["patch"]; patch != nil {
				var p map[string]json.RawMessage
				_ = json.Unmarshal(patch, &p)
				keys = nil
				for key := range p {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				t.Log("native task patch fields", keys)
			}
		}
		observation, err := binding.Observe(event)
		if err != nil {
			keys := make([]string, 0, len(fields))
			for key := range fields {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			var message struct {
				ID      string `json:"id"`
				Content []struct {
					Type string `json:"type"`
				} `json:"content"`
			}
			_ = json.Unmarshal(fields["message"], &message)
			var parent *string
			_ = json.Unmarshal(fields["parent_tool_use_id"], &parent)
			t.Log("failed envelope keys", keys, "has parent", parent != nil, "block types", message.Content, "active messages", len(binding.content.active))
			t.Fatalf("native task observation failed: %s/%s: %v; %s", event.Type, subtype, err, logs.String())
		}
		if event.Kind == NativeMessage && (event.Type == "user" || event.Type == "assistant") {
			var parent *string
			_ = json.Unmarshal(fields["parent_tool_use_id"], &parent)
			if parent != nil {
				proof, err := ObserveChildHistoryMessage(event, cfg.SessionID, *parent)
				if err != nil {
					t.Fatal(err)
				}
				historyProofs[*parent] = append(historyProofs[*parent], proof)
			}
		}
		if observation.Kind == InputFinished {
			result = observation.Result
		}
		if task := observation.Task; task != nil {
			switch task.Kind {
			case TaskStarted:
				if task.ToolID == nil || *task.ToolID != "toolu_owned_child" || task.Type == nil || *task.Type != LocalAgentTask || task.Prompt == nil || *task.Prompt != "Return the private child fixture response." {
					t.Fatal("native task lost its original child ownership")
				}
				taskStarted = true
			case TaskUpdated:
				if task.Patch != nil && task.Patch.Status != nil && *task.Patch.Status == TaskCompleted {
					taskUpdated = true
				}
			case TaskNotification:
				if task.Status == nil || *task.Status != TaskCompleted || task.Usage == nil {
					t.Fatal("native task terminal evidence changed")
				}
				taskNotified = true
			}
		}
		for _, content := range observation.Content {
			t.Log("content observation", content.Kind, "child", content.ParentToolID != "", "blocks", len(content.Blocks))
			if content.Kind == ToolResultObserved && content.ToolResult.ID == "toolu_owned_child" {
				for _, block := range content.ToolResult.Blocks {
					if block.Text != nil && strings.Contains(*block.Text, "Fixture response 3.") {
						childReturned = true
					}
				}
			}
			if content.ParentToolID != "toolu_owned_child" {
				continue
			}
			switch content.Kind {
			case ChildInputObserved:
				childInput = true
			case ProviderMessageSnapshot:
				for _, block := range content.Blocks {
					if block.Tool != nil && block.Tool.ID == "toolu_child_read" && block.Tool.Name == "Read" {
						childTool = true
					}
				}
			case ToolResultObserved:
				if content.ToolResult.ID == "toolu_child_read" {
					childResult = true
					if content.Index != nil {
						t.Fatal("unstreamed child result fabricated a stream index")
					}
				}
			}
		}
		if observation.Kind == CommandObserved && observation.Command == CommandCompleted {
			commandClosed = true
		}
	}
	if !result.Successful() || calls.Load() != 4 || !childInput || !childTool || !childReturned || !childResult || !taskStarted || !taskUpdated || !taskNotified || len(binding.tasks) != 1 || len(binding.backgroundTasks) != 0 {
		t.Log("native evidence", result.Successful(), calls.Load(), childInput, childTool, childReturned, childResult, taskStarted, taskUpdated, taskNotified, len(binding.tasks), len(binding.backgroundTasks))
		t.Fatal("native child execution did not finish exactly once")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	nativeFixtureChildHistory(t, cfg, binding.tasks, historyProofs)
}

func nativeTaskTextContains(raw json.RawMessage, want string) bool {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.Contains(text, want)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return false
	}
	for _, block := range blocks {
		if block.Type == "text" && strings.Contains(block.Text, want) {
			return true
		}
	}
	return false
}

func TestManualNativeBackgroundTaskStop(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	cfg, logs := apiFixtureConfig(t, "native-background-task")
	cfg.Process.Executable = binary
	cfg.Model = "fixture-model"
	cfg.Permission = DefaultPermission
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	started := make(chan string, 1)
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || r.Header.Get("Authorization") != "" {
			t.Error("background fixture authority changed")
			w.WriteHeader(400)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxStreamFrame+1))
		var body struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err != nil || len(raw) > maxStreamFrame || json.Unmarshal(raw, &body) != nil || body.Model != cfg.Model || !body.Stream {
			t.Error("background fixture request changed")
			w.WriteHeader(400)
			return
		}
		n := calls.Add(1)
		switch n {
		case 1:
			nativeFixtureToolResponse(w, n, "Bash", "toolu_background", map[string]any{"command": "sleep 60", "description": "Wait in the owned fixture scope", "run_in_background": true})
		case 2:
			var taskID string
			select {
			case taskID = <-started:
			case <-ctx.Done():
				w.WriteHeader(400)
				return
			}
			result, ok := nativeFixtureToolResults(t, raw)["toolu_background"]
			if !ok || result.Error || !nativeTaskTextContains(result.Content, taskID) {
				t.Error("native background result lost task ownership")
			}
			nativeFixtureToolResponse(w, n, "TaskStop", "toolu_stop_background", map[string]any{"task_id": taskID})
		case 3:
			result, ok := nativeFixtureToolResults(t, raw)["toolu_stop_background"]
			if !ok || result.Error {
				t.Error("native task stop did not succeed")
			}
			nativeFixtureTextResponse(w, n)
		default:
			t.Error("background fixture repeated provider request")
			w.WriteHeader(400)
		}
	}))
	defer provider.Close()
	authority := nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Background task fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}
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
		for _, secret := range []string{nativeAPIFixtureToken, nativeAPIUpstreamKey} {
			if strings.Contains(logs.String(), secret) {
				t.Error("background credential entered logs")
			}
		}
	}()
	input := domain.NewID()
	const prompt = "Start and stop the owned background fixture."
	b, err := BindExecution(cfg, input, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SendInput(ctx, input, cfg.SessionID, prompt); err != nil {
		t.Fatal(err)
	}
	var result *NativeResult
	var taskID string
	var closed, sawBackground, sawStopped, returnedBeforeStop bool
	for result == nil || !closed {
		event, err := s.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		observation, err := b.Observe(event)
		if err != nil {
			t.Fatalf("background observation failed: %s/%s: %v; %s", event.Kind, event.Type, err, logs.String())
		}
		if interaction := observation.Interaction; interaction != nil && interaction.Kind == InteractionRequested {
			if interaction.Request.Kind != ToolPermission || (interaction.Request.ToolName != "Bash" && interaction.Request.ToolName != "TaskStop") {
				t.Fatal("unexpected background permission")
			}
			original, reply, err := b.PreparePermissionReply(interaction.ArrivalID, PermissionReply{Behavior: PermissionAllow})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Reply(ctx, original, reply); err != nil {
				t.Fatal(err)
			}
		}
		if task := observation.Task; task != nil {
			switch task.Kind {
			case TaskStarted:
				if taskID != "" || task.ToolID == nil || *task.ToolID != "toolu_background" || task.Type == nil || *task.Type != LocalBashTask {
					t.Fatal("background start lost its original tool")
				}
				taskID = task.ID
				started <- task.ID
			case BackgroundTasksChanged:
				if len(task.Background) > 0 {
					sawBackground = true
				}
			case TaskUpdated:
				if task.Patch != nil && task.Patch.Status != nil && *task.Patch.Status == TaskKilled {
					sawStopped = true
				}
			case TaskNotification:
				if task.Status != nil && *task.Status == TaskStopped {
					sawStopped = true
				}
			}
		}
		for _, content := range observation.Content {
			if content.Kind == ToolResultObserved && content.ToolResult.ID == "toolu_background" {
				returnedBeforeStop = !b.tasks[taskID].status.terminal()
			}
		}
		if observation.Kind == InputFinished {
			result = observation.Result
		}
		if observation.Kind == CommandObserved && observation.Command == CommandCompleted {
			closed = true
		}
	}
	if !result.Successful() || calls.Load() != 3 || !sawBackground || !sawStopped || !returnedBeforeStop || taskID == "" || !b.tasks[taskID].status.terminal() || result.KnownWork.PendingTasks != 0 || result.KnownWork.BackgroundTasks != 0 {
		t.Fatal("background task lifetime was conflated with its returned tool or input")
	}
}
