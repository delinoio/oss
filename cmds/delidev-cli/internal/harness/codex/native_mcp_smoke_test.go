// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// This file is the installed-Codex acceptance fixture for managed MCP. It uses
// a private scripted model provider and local MCP servers only; no account,
// external provider or real MCP service is contacted. It is skipped unless an
// explicit installed executable is supplied.

const mcpStdioFixtureArgument = "delidev-mcp-stdio-fixture"

func init() {
	if len(os.Args) < 3 || os.Args[len(os.Args)-1] != mcpStdioFixtureArgument {
		return
	}
	logPath := strings.TrimPrefix(os.Args[len(os.Args)-2], "-delidev-mcp-log=")
	token := os.Getenv("FIXTURE_TOKEN")
	log := func(entry map[string]any) {
		out, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(80)
		}
		_ = json.NewEncoder(out).Encode(entry)
		_ = out.Close()
	}
	log(map[string]any{"event": "started", "pid": os.Getpid(), "token_present": token != ""})
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1<<20), 8<<20)
	send := func(message map[string]any) {
		message["jsonrpc"] = "2.0"
		_ = json.NewEncoder(os.Stdout).Encode(message)
	}
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(81)
		}
		switch request.Method {
		case "initialize":
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(request.Params, &params)
			send(map[string]any{"id": request.ID, "result": map[string]any{"protocolVersion": params.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}, "resources": map[string]any{}}, "serverInfo": map[string]any{"name": "delidev-fixture", "version": "1"}}})
		case "tools/list":
			send(map[string]any{"id": request.ID, "result": map[string]any{"tools": []any{
				map[string]any{"name": "echo", "description": "echo", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}, "annotations": map[string]any{"readOnlyHint": true}},
				map[string]any{"name": "write", "description": "write", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}, "annotations": map[string]any{"readOnlyHint": false, "destructiveHint": true}},
			}}})
		case "tools/call":
			log(map[string]any{"event": "call", "params": json.RawMessage(request.Params)})
			send(map[string]any{"id": request.ID, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "fixture-result"}}}})
		case "resources/list":
			send(map[string]any{"id": request.ID, "result": map[string]any{"resources": []any{}}})
		case "resources/templates/list":
			send(map[string]any{"id": request.ID, "result": map[string]any{"resourceTemplates": []any{}}})
		default:
			if len(request.ID) != 0 {
				send(map[string]any{"id": request.ID, "error": map[string]any{"code": -32601, "message": "unsupported"}})
			}
		}
	}
	os.Exit(0)
}

type mcpHTTPFixture struct {
	mu      sync.Mutex
	headers []http.Header
	calls   int
	server  *httptest.Server
}

func newMCPHTTPFixture(t *testing.T) *mcpHTTPFixture {
	f := &mcpHTTPFixture{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		f.mu.Lock()
		f.headers = append(f.headers, r.Header.Clone())
		f.mu.Unlock()
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "unsupported", http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(body, &request) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(request.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		respond := func(result any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
		switch request.Method {
		case "initialize":
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(request.Params, &params)
			w.Header().Set("Mcp-Session-Id", "fixture-session")
			respond(map[string]any{"protocolVersion": params.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "delidev-http-fixture", "version": "1"}})
		case "tools/list":
			respond(map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "echo", "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true}}}})
		case "tools/call":
			f.mu.Lock()
			f.calls++
			f.mu.Unlock()
			respond(map[string]any{"content": []any{map[string]any{"type": "text", "text": "http-result"}}})
		default:
			respond(map[string]any{})
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *mcpHTTPFixture) sawHeader(name, value string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, header := range f.headers {
		if header.Get(name) == value {
			return true
		}
	}
	return false
}

func requireNativeExecutable(t *testing.T) string {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed harness with private local MCP fixtures only")
	}
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// scriptedMCPModel answers each Responses request in order. A request that
// carries a function_call_output is answered with a final message.
type scriptedMCPModel struct {
	mu       sync.Mutex
	requests []string
	calls    []map[string]any // function calls to emit, one per model request
	index    int
}

func (m *scriptedMCPModel) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		m.mu.Lock()
		m.requests = append(m.requests, string(body))
		var item map[string]any
		if m.index < len(m.calls) {
			item = m.calls[m.index]
		}
		m.index++
		m.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		events := []any{map[string]any{"type": "response.created", "response": map[string]any{"id": fmt.Sprintf("resp_%d", m.index), "status": "in_progress"}}}
		if item != nil {
			events = append(events, map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		} else {
			events = append(events, map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_done", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Finished."}}}})
		}
		events = append(events, map[string]any{"type": "response.completed", "response": map[string]any{"id": fmt.Sprintf("resp_%d", m.index), "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}})
		for _, event := range events {
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
	})
}

func TestManualNativeManagedMCPInventoryAndCleanup(t *testing.T) {
	binary := requireNativeExecutable(t)
	model := &scriptedMCPModel{}
	provider := httptest.NewServer(model.handler(t))
	defer provider.Close()
	http1 := newMCPHTTPFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "stdio.log")
	stdio := mcpStdioServer("fx_stdio")
	stdio.Command, stdio.Arguments, stdio.Cwd = executable, []string{"-delidev-mcp-log=" + logPath, mcpStdioFixtureArgument}, filepath.Dir(executable)
	web := mcpHTTPServer("web_http")
	web.Endpoint = http1.server.URL + "/mcp"
	web.Headers = map[string]string{"X-Api-Key": mcpSecretHeader}
	oauth := mcpOAuthServer("oauth_http")
	oauth.Endpoint = http1.server.URL + "/oauth"
	cfg := nativeFixtureConfig(t, binary, provider.URL)
	cfg.ManagedMCP = []ManagedMCPServer{stdio, web, oauth}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = client.Close()
		}
	}()
	settings := ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Effort: "high", Cwd: cfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
	if _, err := client.StartThread(ctx, domain.NewID(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Use the fixture tool."}); err != nil {
		t.Fatalf("exact installed inventory was rejected: %v", err)
	}
	for _, outcome := range client.ManagedMCPOutcomes() {
		if outcome.State != domain.MCPStartupReady {
			t.Fatalf("selected server not ready: %+v", outcome)
		}
	}
	// Native shell tools cannot inherit any protected MCP value.
	configResponse, err := client.wire.Call(ctx, domain.NewID(), "config/read", struct {
		Cwd           string `json:"cwd"`
		IncludeLayers bool   `json:"includeLayers"`
	}{Cwd: cfg.Process.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	var nativeConfig struct {
		Config struct {
			Shell struct {
				Exclude []string `json:"exclude"`
			} `json:"shell_environment_policy"`
		} `json:"config"`
	}
	if json.Unmarshal(configResponse.Result, &nativeConfig) != nil {
		t.Fatal("native configuration is unreadable")
	}
	for _, name := range []string{"FIXTURE_TOKEN", "DELIDEV_MCP_2_H0", "DELIDEV_MCP_1_BEARER"} {
		if !slices.Contains(nativeConfig.Config.Shell.Exclude, name) {
			t.Fatalf("native shell environment may inherit %s: %v", name, nativeConfig.Config.Shell.Exclude)
		}
	}
	if !http1.sawHeader("X-Api-Key", mcpSecretHeader) || !http1.sawHeader("Authorization", "Bearer "+mcpSecretBearer) {
		t.Fatal("protected headers did not reach the HTTP servers through private environment references")
	}
	raw, _ := os.ReadFile(logPath)
	if !strings.Contains(string(raw), `"token_present":true`) {
		t.Fatalf("stdio server did not receive its inherited secret: %s", raw)
	}
	pids := startedPIDs(string(raw))
	if len(pids) != 1 {
		t.Fatalf("the stdio server must start exactly once for the thread: %s", raw)
	}
	unrelated := exec.Command("sleep", "30")
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unrelated.Process.Kill(); _ = unrelated.Wait() }()
	// Every native child is owned by the joined Codex process tree.
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	for _, pid := range pids {
		if processAlive(pid) {
			t.Fatalf("owned MCP child %d survived cleanup", pid)
		}
	}
	if !processAlive(unrelated.Process.Pid) {
		t.Fatal("cleanup touched an unrelated process")
	}
	// Protected values never reach any file in the private native runtime.
	_ = filepath.WalkDir(filepath.Dir(cfg.Home), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		contents, readErr := os.ReadFile(path)
		if readErr == nil {
			for _, secret := range []string{mcpSecretEnv, mcpSecretHeader, mcpSecretBearer} {
				if strings.Contains(string(contents), secret) {
					t.Errorf("%s persisted a protected MCP value", path)
				}
			}
		}
		return nil
	})
}

func TestManualNativeManagedMCPRejectsUnselectedProjectServers(t *testing.T) {
	binary := requireNativeExecutable(t)
	provider := httptest.NewServer((&scriptedMCPModel{}).handler(t))
	defer provider.Close()
	executable, _ := os.Executable()
	logPath := filepath.Join(t.TempDir(), "stdio.log")
	stdio := mcpStdioServer("fx_stdio")
	stdio.Command, stdio.Arguments, stdio.Cwd = executable, []string{"-delidev-mcp-log=" + logPath, mcpStdioFixtureArgument}, filepath.Dir(executable)
	cfg := nativeFixtureConfig(t, binary, provider.URL)
	cfg.ManagedMCP = []ManagedMCPServer{stdio}
	// A user-layer server in the native home must never join the selected inventory.
	configPath := filepath.Join(cfg.Home, "config.toml")
	existing, _ := os.ReadFile(configPath)
	extra := fmt.Sprintf("\n[mcp_servers.unselected_user]\ncommand = %q\nargs = [\"-delidev-mcp-log=%s\", %q]\n", executable, logPath, mcpStdioFixtureArgument)
	if err := os.WriteFile(configPath, append(existing, []byte(extra)...), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	settings := ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Effort: "high", Cwd: cfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
	if _, err := client.StartThread(ctx, domain.NewID(), settings); err != nil {
		t.Fatal(err)
	}
	_, err = client.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Use the fixture tool."})
	assertCode(t, err, domain.Unsupported)
}

func startedPIDs(log string) []int {
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		var row struct {
			Event string `json:"event"`
			PID   int    `json:"pid"`
		}
		if json.Unmarshal([]byte(line), &row) == nil && row.Event == "started" {
			pids = append(pids, row.PID)
		}
	}
	return pids
}

func serverCalls(log string) []map[string]any {
	var calls []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		var row map[string]any
		if json.Unmarshal([]byte(line), &row) == nil && row["event"] == "call" {
			calls = append(calls, row)
		}
	}
	return calls
}

func runMCPTurn(t *testing.T, tool string, decide func(*Client, Event, domain.ID)) (*Client, []Event, *scriptedMCPModel, string) {
	t.Helper()
	binary := requireNativeExecutable(t)
	model := &scriptedMCPModel{calls: []map[string]any{{"type": "function_call", "namespace": "mcp__fx_stdio", "name": tool, "call_id": "call_fixture_1", "arguments": `{"text":"hello"}`}}}
	provider := httptest.NewServer(model.handler(t))
	t.Cleanup(provider.Close)
	executable, _ := os.Executable()
	logPath := filepath.Join(t.TempDir(), "stdio.log")
	stdio := mcpStdioServer("fx_stdio")
	stdio.Command, stdio.Arguments, stdio.Cwd = executable, []string{"-delidev-mcp-log=" + logPath, mcpStdioFixtureArgument}, filepath.Dir(executable)
	cfg := nativeFixtureConfig(t, binary, provider.URL)
	cfg.ManagedMCP = []ManagedMCPServer{stdio}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	client, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	settings := ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Effort: "high", Cwd: cfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
	if _, err := client.StartThread(ctx, domain.NewID(), settings); err != nil {
		t.Fatal(err)
	}
	turn, err := client.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Use the fixture tool."})
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	for {
		event, err := client.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
		if event.Kind == InteractionRequestedEvent && decide != nil {
			decide(client, event, turn.TurnID)
		}
		if event.Kind == TurnCompletedEvent {
			break
		}
	}
	return client, events, model, logPath
}

func TestManualNativeManagedMCPReadOnlyToolCallKeepsOriginalIdentity(t *testing.T) {
	_, events, _, logPath := runMCPTurn(t, "echo", nil)
	var started, completed *Event
	for i := range events {
		switch events[i].Kind {
		case ToolStartedEvent:
			started = &events[i]
		case ToolCompletedEvent:
			completed = &events[i]
		}
	}
	if started == nil || completed == nil || started.Tool.MCP.Identity.Server != "fx_stdio" || completed.Tool.MCP.Identity != started.Tool.MCP.Identity || completed.Tool.Status != ToolCompleted || completed.Tool.MCP.Tool != "echo" || completed.Tool.MCP.ResultText == nil || *completed.Tool.MCP.ResultText != "fixture-result" || completed.Tool.MCP.ReadOnlyHint == nil || !*completed.Tool.MCP.ReadOnlyHint {
		t.Fatalf("native call lost its original identity or result: %+v", completed)
	}
	log, _ := os.ReadFile(logPath)
	if calls := serverCalls(string(log)); len(calls) != 1 {
		t.Fatalf("server received %d calls", len(calls))
	}
}

func answerElicitation(t *testing.T, action string, answered *int) func(*Client, Event, domain.ID) {
	return func(c *Client, event Event, turn domain.ID) {
		if event.Interaction == nil || !event.Interaction.Questions.Blocking {
			t.Fatalf("unexpected interaction: %+v", event.Interaction)
		}
		answers := QuestionAnswers{Answers: map[string][]string{"action": {action}}}
		for _, q := range event.Interaction.Questions.Questions[1:] {
			answers.Answers[q.ID] = []string{}
		}
		response := domain.NewID()
		if _, err := c.AnswerQuestions(context.Background(), response, event.Interaction.ID, turn, answers); err != nil {
			t.Fatal(err)
		}
		*answered++
		// A second response for the same native request is refused locally.
		if _, err := c.AnswerQuestions(context.Background(), domain.NewID(), event.Interaction.ID, turn, answers); err == nil {
			t.Fatal("the native request was answered twice")
		}
	}
}

func TestManualNativeManagedMCPDeniedWriteHasNoEffect(t *testing.T) {
	answered := 0
	_, events, _, logPath := runMCPTurn(t, "write", answerElicitation(t, "decline", &answered))
	var completed, accepted *Event
	for i := range events {
		switch events[i].Kind {
		case ToolCompletedEvent:
			completed = &events[i]
		case QuestionAcceptedEvent:
			accepted = &events[i]
		}
	}
	if answered != 1 || completed == nil || completed.Tool.Status != ToolFailed || completed.Tool.MCP.Identity.Server != "fx_stdio" || completed.Tool.MCP.Error == nil || accepted == nil || !accepted.InteractionState.Elicitation {
		t.Fatalf("denied call did not settle through exact native evidence: %+v", completed)
	}
	log, _ := os.ReadFile(logPath)
	if calls := serverCalls(string(log)); len(calls) != 0 {
		t.Fatalf("a denied write reached the MCP server: %v", calls)
	}
}

func TestManualNativeManagedMCPAcceptedWriteRunsExactlyOnce(t *testing.T) {
	answered := 0
	_, events, _, logPath := runMCPTurn(t, "write", answerElicitation(t, "accept", &answered))
	var completed *Event
	for i := range events {
		if events[i].Kind == ToolCompletedEvent {
			completed = &events[i]
		}
	}
	if answered != 1 || completed == nil || completed.Tool.Status != ToolCompleted {
		t.Fatalf("accepted write did not complete: %+v", completed)
	}
	log, _ := os.ReadFile(logPath)
	if calls := serverCalls(string(log)); len(calls) != 1 {
		t.Fatalf("server received %d writes", len(calls))
	}
}

func TestManualNativeManagedMCPReconnectsOnlyTheOriginalSelectedServer(t *testing.T) {
	binary := requireNativeExecutable(t)
	provider := httptest.NewServer((&scriptedMCPModel{}).handler(t))
	defer provider.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	web := mcpHTTPServer("web_late")
	web.Endpoint = "http://" + address + "/mcp"
	web.Headers = map[string]string{"X-Api-Key": mcpSecretHeader}
	executable, _ := os.Executable()
	logPath := filepath.Join(t.TempDir(), "stdio.log")
	stdio := mcpStdioServer("fx_stdio")
	stdio.Command, stdio.Arguments, stdio.Cwd = executable, []string{"-delidev-mcp-log=" + logPath, mcpStdioFixtureArgument}, filepath.Dir(executable)
	cfg := nativeFixtureConfig(t, binary, provider.URL)
	cfg.ManagedMCP = []ManagedMCPServer{stdio, web}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	settings := ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Effort: "high", Cwd: cfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
	if _, err := client.StartThread(ctx, domain.NewID(), settings); err != nil {
		t.Fatal(err)
	}
	_, err = client.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Use the fixture tool."})
	assertCode(t, err, domain.Unavailable)
	states := map[string]domain.MCPStartupOutcome{}
	for _, outcome := range client.ManagedMCPOutcomes() {
		states[outcome.Identity.Server] = outcome.State
	}
	if states["web_late"] != domain.MCPStartupFailed || states["fx_stdio"] != domain.MCPStartupReady {
		t.Fatalf("typed startup outcomes changed: %v", states)
	}
	// The unavailable HTTP endpoint becomes reachable; only the original
	// definition can be reconnected and the healthy stdio server is untouched.
	late := httptest.NewUnstartedServer(newMCPHTTPFixture(t).server.Config.Handler)
	late.Listener.Close()
	late.Listener, err = net.Listen("tcp", address)
	if err != nil {
		t.Skipf("fixture port was reused: %v", err)
	}
	late.Start()
	defer late.Close()
	if _, err := client.ReconnectManagedMCP(ctx, domain.NewID()); err == nil {
		t.Fatal("a foreign definition was reconnected")
	}
	if _, err := client.ReconnectManagedMCP(ctx, web.Identity.DefinitionID); err != nil {
		t.Fatalf("original selected definition did not reconnect: %v", err)
	}
	raw, _ := os.ReadFile(logPath)
	if len(startedPIDs(string(raw))) > 2 {
		t.Fatalf("healthy stdio server was restarted by a targeted reconnect: %s", raw)
	}
	if _, err := client.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Use the fixture tool."}); err != nil {
		t.Fatalf("input remained blocked after reconnect: %v", err)
	}
}
