// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/google/uuid"
)

func TestSubagentClaudeHistoryPublishesEachOriginalLeafOnceAfterAcknowledgment(t *testing.T) {
	c, rpc, _ := claudeToolFixture(t, "", "Agent")
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := claude.APIStreamConfig{Home: filepath.Join(root, "claude"), Workspace: filepath.Join(root, "workspace")}
	// Unix mode bits cannot establish an owner-only Windows ACL. Create the
	// native home with the shared private-directory policy so descendants
	// inherit that ACL; keep the production transcript reader unchanged.
	if err := security.PrivateDir(cfg.Home); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	description, agentType, depth := "Original child", "general-purpose", uint32(1)
	files := map[string][]map[string]any{}
	write := func(id string) {
		t.Helper()
		base := filepath.Join(cfg.Home, "projects", "delidev", string(c.binding.journal.SessionID), "subagents", "agent-"+id)
		if err := os.MkdirAll(filepath.Dir(base), 0o700); err != nil {
			t.Fatal(err)
		}
		var body []byte
		for _, record := range files[id] {
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			body = append(append(body, raw...), '\n')
		}
		meta, _ := json.Marshal(map[string]any{"toolUseId": c.children[id].ParentToolID, "agentType": agentType, "description": description, "spawnDepth": depth})
		if err := os.WriteFile(base+".jsonl", body, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(base+".meta.json", meta, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{base + ".jsonl", base + ".meta.json"} {
			if err := security.RegularPrivate(path); err != nil {
				t.Fatal("history fixture is not owner-only", domain.SafeError(err).Code)
			}
		}
	}
	for i, id := range []string{"first_child", "second_child"} {
		tool, kind := []string{"tool_original_one", "tool_original_two"}[i], claude.LocalAgentTask
		start := claude.LifecycleObservation{Kind: claude.TaskObserved, SessionID: c.binding.journal.SessionID, InputID: c.binding.journal.InputID, TurnID: c.binding.turn, NativeID: string(domain.NewID()), Accepted: true, Task: &claude.NativeTaskObservation{Kind: claude.TaskStarted, ID: id, ToolID: &tool, Type: &kind, Description: &description, SubagentType: &agentType, SpawnDepth: &depth}}
		if _, err := c.PublishTaskObservation(ctx, start); err != nil {
			t.Fatal(err)
		}
		user, leaf := uuid.NewString(), uuid.NewString()
		files[id] = []map[string]any{
			{"type": "user", "uuid": user, "parentUuid": nil, "sessionId": c.binding.journal.SessionID, "cwd": cfg.Workspace, "version": claude.SupportedVersion, "isSidechain": true, "agentId": id, "message": map[string]any{"role": "user", "content": "Original child input"}},
			{"type": "assistant", "uuid": leaf, "parentUuid": user, "sessionId": c.binding.journal.SessionID, "cwd": cfg.Workspace, "version": claude.SupportedVersion, "isSidechain": true, "agentId": id, "message": map[string]any{"role": "assistant", "id": "msg_" + id, "model": "original-child-model", "content": []any{map[string]any{"type": "text", "text": "Original child output"}}, "usage": map[string]any{"output_tokens": 3}, "stop_reason": "end_turn"}},
		}
		write(id)
	}
	complete := func(id string) {
		t.Helper()
		status := claude.TaskCompleted
		o := claude.LifecycleObservation{Kind: claude.TaskObserved, SessionID: c.binding.journal.SessionID, InputID: c.binding.journal.InputID, TurnID: c.binding.turn, NativeID: string(domain.NewID()), Accepted: true, Task: &claude.NativeTaskObservation{Kind: claude.TaskUpdated, ID: id, Patch: &claude.TaskPatch{Status: &status}}}
		if _, err := c.PublishTaskObservation(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	complete("first_child")
	child := c.children["first_child"]
	if _, err := claude.ReadChildTranscript(ctx, cfg.Home, c.binding.journal.SessionID, cfg.Workspace, claude.ChildHistoryBinding{TaskID: child.NativeID, ToolID: child.ParentToolID, AgentType: agentType, Description: description, SpawnDepth: depth}, c.childProofs[child.NativeID], nil); err != nil {
		t.Fatal("private history fixture is unavailable before receipt publication", domain.SafeError(err).Code)
	}
	rpc.lose = true
	before := len(rpc.events)
	if err := c.PublishChildHistory(ctx, cfg); err == nil || len(c.childHistorySources) != 0 {
		t.Fatal("lost acknowledgment installed history identity")
	}
	rpc.lose = false
	if err := c.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if len(c.childHistorySources) != 1 || rpc.requests[before] != rpc.requests[before+1] || !bytes.Equal(rpc.events[before], rpc.events[before+1]) {
		t.Fatal("history replay replaced its original receipt")
	}
	before = len(rpc.events)
	if err := c.PublishChildHistory(ctx, cfg); err != nil || len(rpc.events) != before {
		t.Fatal("repeated first-child inspection published a duplicate", err)
	}
	complete("second_child")
	before = len(rpc.events)
	if err := c.PublishChildHistory(ctx, cfg); err != nil || len(rpc.events) != before+1 || len(c.childHistorySources) != 2 {
		t.Fatal("newly settled sibling republished prior child history", err)
	}
	before = len(rpc.events)
	if err := c.PublishChildHistory(ctx, cfg); err != nil || len(rpc.events) != before {
		t.Fatal("settled history published twice", err)
	}
	// A new verified leaf is a distinct observation. Native finalization can
	// also refine its usage; each exact verified projection publishes once.
	prior := files["first_child"][1]
	next := map[string]any{}
	for key, value := range prior {
		next[key] = value
	}
	next["uuid"], next["parentUuid"] = uuid.NewString(), prior["uuid"]
	next["message"] = map[string]any{"role": "assistant", "id": "msg_new_leaf", "model": "original-child-model", "content": []any{map[string]any{"type": "text", "text": "New leaf output"}}, "usage": map[string]any{"output_tokens": 4}, "stop_reason": "end_turn"}
	files["first_child"] = append(files["first_child"], next)
	write("first_child")
	if err := c.PublishChildHistory(ctx, cfg); err != nil || len(rpc.events) != before+1 {
		t.Fatal("new original history leaf was suppressed", err)
	}
	before = len(rpc.events)
	next["message"].(map[string]any)["usage"] = map[string]any{"output_tokens": 5}
	write("first_child")
	if err := c.PublishChildHistory(ctx, cfg); err != nil || len(rpc.events) != before+1 {
		t.Fatal("verified native usage finalization was suppressed", err)
	}
	before = len(rpc.events)
	if err := c.PublishChildHistory(ctx, cfg); err != nil || len(rpc.events) != before {
		t.Fatal("unchanged finalized history was published twice", err)
	}
}
