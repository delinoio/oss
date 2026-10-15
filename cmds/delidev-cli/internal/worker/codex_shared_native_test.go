// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// runSharedCodexNativeFixture is a child-process app-server with one account
// home and multiple independent root threads. It sends no provider request.
func runSharedCodexNativeFixture(home string) {
	threads := make(map[domain.ID]map[string]any)
	countBytes, _ := os.ReadFile(filepath.Join(home, "shared-fixture"))
	mode := strings.TrimSpace(string(countBytes))
	loss := strings.HasPrefix(mode, "loss-")
	expected, _ := strconv.Atoi(strings.TrimPrefix(mode, "loss-"))
	if expected < 1 {
		os.Exit(90)
	}
	type pendingTurn struct{ thread, turn domain.ID }
	var pending []pendingTurn
	encode := json.NewEncoder(os.Stdout)
	write := func(id json.RawMessage, result any) {
		_ = encode.Encode(map[string]any{"id": id, "result": result})
	}
	notify := func(method string, params any) {
		_ = encode.Encode(map[string]any{"method": method, "params": params})
	}
	turn := func(id domain.ID, status codex.TurnStatus) map[string]any {
		return map[string]any{"id": id, "items": []any{}, "itemsView": "notLoaded", "status": status, "startedAt": nil, "completedAt": nil, "durationMs": nil, "error": nil}
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(91)
		}
		switch request.Method {
		case "initialize":
			platform, family := runtime.GOOS, "unix"
			if platform == "darwin" {
				platform = "macos"
			} else if platform == "windows" {
				family = "windows"
			}
			write(request.ID, map[string]any{"codexHome": home, "platformFamily": family, "platformOs": platform, "userAgent": "delidev/" + codex.SupportedVersion + " (shared fixture)"})
		case "initialized":
		case "thread/loaded/list":
			write(request.ID, map[string]any{"data": []string{}, "nextCursor": nil})
		case "config/read":
			write(request.ID, map[string]any{"config": map[string]any{"cli_auth_credentials_store": "file", "model_provider": "openai", "forced_login_method": "chatgpt", "model_providers": map[string]any{}, "features": map[string]any{"plugins": false}, "mcp_servers": map[string]any{}}, "origins": nil, "layers": nil})
		case "thread/start":
			id := domain.NewID()
			directory := filepath.Join(home, "sessions", "2026", "10", "14")
			if security.PrivateDir(directory) != nil {
				os.Exit(92)
			}
			path := filepath.Join(directory, fmt.Sprintf("rollout-2026-10-14T12-00-00-%s.jsonl", id))
			metadata := []byte(fmt.Sprintf(`{"timestamp":"2026-10-14T12:00:00Z","type":"session_meta","payload":{"id":"%s"}}`+"\n", id))
			if os.WriteFile(path, metadata, 0600) != nil {
				os.Exit(93)
			}
			thread := map[string]any{"id": id, "sessionId": id, "cliVersion": codex.SupportedVersion, "cwd": request.Params["cwd"], "modelProvider": "openai", "createdAt": int64(1), "updatedAt": int64(1), "ephemeral": false, "preview": "", "projectId": nil, "source": "appServer", "status": map[string]any{"type": "idle"}, "turns": []any{}, "historyMode": "legacy", "extra": nil, "canAcceptDirectInput": true, "path": path}
			threads[id] = thread
			write(request.ID, map[string]any{"thread": thread, "model": request.Params["model"], "modelProvider": "openai", "cwd": request.Params["cwd"], "approvalPolicy": "on-request", "approvalsReviewer": "user", "sandbox": map[string]any{"type": "readOnly"}, "reasoningEffort": nil, "serviceTier": nil, "instructionSources": []string{}, "runtimeWorkspaceRoots": []string{}, "activePermissionProfile": nil, "multiAgentMode": "explicitRequestOnly"})
		case "thread/read":
			id, _ := request.Params["threadId"].(string)
			thread := threads[domain.ID(id)]
			if thread == nil {
				os.Exit(94)
			}
			write(request.ID, map[string]any{"thread": thread})
		case "thread/list":
			write(request.ID, map[string]any{"data": []any{}, "nextCursor": nil, "backwardsCursor": nil})
		case "turn/start":
			threadID, _ := request.Params["threadId"].(string)
			if threads[domain.ID(threadID)] == nil {
				os.Exit(95)
			}
			id := domain.NewID()
			write(request.ID, map[string]any{"turn": turn(id, codex.TurnRunning)})
			notify("turn/started", map[string]any{"threadId": threadID, "turn": turn(id, codex.TurnRunning)})
			content := []any{map[string]any{"type": "text", "text": request.Params["input"].([]any)[0].(map[string]any)["text"], "text_elements": []any{}}}
			item := map[string]any{"type": "userMessage", "id": "fixture-user-item", "clientId": request.Params["clientUserMessageId"], "content": content}
			notify("item/started", map[string]any{"threadId": threadID, "turnId": id, "startedAtMs": 1, "item": item})
			notify("item/completed", map[string]any{"threadId": threadID, "turnId": id, "completedAtMs": 1, "item": item})
			if security.WriteAtomic(filepath.Join(home, "auth.json"), workerSubscriptionBundle("rotated")) != nil {
				os.Exit(96)
			}
			pending = append(pending, pendingTurn{domain.ID(threadID), id})
			if len(pending) == expected {
				if loss {
					os.Exit(99)
				}
				for _, completed := range pending {
					notify("turn/completed", map[string]any{"threadId": completed.thread, "turn": turn(completed.turn, codex.TurnCompleted)})
				}
			}
		case "thread/unsubscribe":
			threadID, _ := request.Params["threadId"].(string)
			if threads[domain.ID(threadID)] == nil {
				os.Exit(97)
			}
			write(request.ID, map[string]any{"status": "unsubscribed"})
			notify("thread/closed", map[string]any{"threadId": threadID})
		case "account/read":
			write(request.ID, map[string]any{"account": map[string]any{"type": "chatgpt", "email": "fixture@example.invalid", "planType": "plus"}, "requiresOpenaiAuth": true})
		default:
			os.Exit(98)
		}
	}
}
