package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func bashTaskContinuationFixture(t *testing.T, startChanges ...func(map[string]any)) *APISession {
	t.Helper()
	s := inlineContinuationFixture(t, inlineBashTool)
	b := s.current
	tool := b.content.tools["toolu_original_bash"]
	tool.finished = false
	b.content.tools["toolu_original_bash"] = tool
	b.finished = false
	started := string(domain.NewID())
	start := map[string]any{"type": "system", "subtype": TaskStarted, "session_id": b.session, "uuid": started, "task_id": "original-task", "tool_use_id": "toolu_original_bash", "task_type": LocalBashTask, "description": "Private original task description"}
	for _, change := range startChanges {
		change(start)
	}
	raw, _ := json.Marshal(start)
	if _, err := b.observeTask(TaskStarted, raw); err != nil {
		t.Fatal(err)
	}
	b.seen[started] = true
	tool.finished = true
	b.content.tools["toolu_original_bash"] = tool
	b.finished = true
	completed := string(domain.NewID())
	raw, _ = json.Marshal(map[string]any{"type": "system", "subtype": TaskNotification, "session_id": b.session, "uuid": completed, "task_id": "original-task", "tool_use_id": "toolu_original_bash", "status": TaskCompleted, "output_file": "/private/original-task.output", "summary": "Private original task summary"})
	if _, err := b.observeTask(TaskNotification, raw); err != nil {
		t.Fatal(err)
	}
	b.seen[completed] = true
	return s
}
func TestInlineBashTaskCheckpointPreservesOriginalProofAndExcludesContent(t *testing.T) {
	s := bashTaskContinuationFixture(t)
	ctx := context.Background()
	original := s.current.tasks["original-task"]
	closed, err := s.CloseForContinuation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, ref, err := closed.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"Private original task description", "Private original task summary", "/private/original-task.output", "private-original-bash", s.config.Workspace} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("task checkpoint exposed private metadata")
		}
	}
	cfg := s.config
	cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
	if err := InspectCheckpoint(ctx, cfg, checkpointDigest([]byte(cfg.Instructions)), raw, ref); err == nil {
		t.Fatal("inspection accepted instruction body")
	}
	cfg.Instructions = ""
	// Fixtures can use empty instructions; the comparison-only API still cannot
	// acquire a handoff or modify the original checkpoint on repeated reads.
	originalCfg := s.config
	originalCfg.API = APIConfig{ServerOrigin: s.serverOrigin}
	for i := 0; i < 2; i++ {
		if err := InspectCheckpoint(ctx, cfg, checkpointDigest([]byte(originalCfg.Instructions)), raw, ref); err != nil {
			t.Fatal(err)
		}
	}
	restored, err := RestoreCheckpoint(ctx, originalCfg, raw, ref)
	if err != nil {
		t.Fatal(err)
	}
	if actual := restored.previous.current.tasks["original-task"]; !reflect.DeepEqual(actual, original) {
		t.Fatal("original task ownership changed")
	}
	again, againRef, err := restored.RetainCheckpoint(ctx)
	if err != nil || !bytes.Equal(raw, again) || ref != againRef {
		t.Fatal("task checkpoint re-encoding changed original evidence", err)
	}
}
func TestInlineBashTaskCheckpointRejectsChangedOriginalEnvelope(t *testing.T) {
	for _, change := range []string{"omitted", "task", "tool", "start", "finish", "start-digest", "finish-digest", "duplicate"} {
		t.Run(change, func(t *testing.T) {
			s := bashTaskContinuationFixture(t)
			closed, err := s.CloseForContinuation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			raw, ref, err := closed.RetainCheckpoint(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var cp sessionCheckpoint
			if json.Unmarshal(raw, &cp) != nil {
				t.Fatal("invalid fixture checkpoint")
			}
			proof := &cp.BashTasks[0]
			switch change {
			case "omitted":
				cp.BashTasks = nil
			case "task":
				proof.ID = "changed-task"
			case "tool":
				proof.Tool = "tool_foreign"
			case "start":
				proof.Started = string(domain.NewID())
			case "finish":
				proof.Completed = proof.Started
			case "start-digest":
				proof.StartDigest = checkpointDigest([]byte("changed"))
			case "finish-digest":
				proof.CompletionDigest = checkpointDigest([]byte("changed"))
			case "duplicate":
				cp.BashTasks = append(cp.BashTasks, *proof)
			}
			changed, _ := json.Marshal(cp)
			cfg := s.config
			cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
			if _, err := RestoreCheckpoint(context.Background(), cfg, changed, ref); err == nil {
				t.Fatal("changed task evidence acquired handoff")
			}
		})
	}
}
func TestInlineBashTaskClosureRejectsMissingAndUnsupportedNativeEvidence(t *testing.T) {
	for _, change := range []string{"missing-proof", "missing-notification", "running", "killed", "backgrounded", "background-list", "foreign-tool", "unstreamed", "no-inline-result", "tool-error", "child", "unknown-event", "start-background", "start-ambient", "start-skip", "start-subagent", "later-background"} {
		t.Run(change, func(t *testing.T) {
			var changes []func(map[string]any)
			if change == "start-background" {
				changes = append(changes, func(v map[string]any) { v["is_backgrounded"] = true })
			}
			if change == "start-ambient" {
				changes = append(changes, func(v map[string]any) { v["ambient"] = true })
			}
			if change == "start-skip" {
				changes = append(changes, func(v map[string]any) { v["skip_transcript"] = true })
			}
			if change == "start-subagent" {
				changes = append(changes, func(v map[string]any) { v["subagent_type"] = "child" })
			}
			s := bashTaskContinuationFixture(t, changes...)
			b := s.current
			task := b.tasks["original-task"]
			tool := b.content.tools[task.tool]
			switch change {
			case "missing-proof":
				task.inlineBash = nil
			case "missing-notification":
				task.notified = false
			case "running":
				task.status = TaskRunning
			case "killed":
				task.status = TaskKilled
			case "backgrounded":
				task.backgrounded = true
			case "background-list":
				b.backgroundTasks = map[string]bool{"original-task": true}
			case "foreign-tool":
				task.tool = "foreign"
			case "unstreamed":
				tool.streamed = false
			case "no-inline-result":
				tool.inline = nil
			case "tool-error":
				tool.inline.Error = true
			case "child":
				tool.parent = "foreign"
			case "unknown-event":
				delete(b.seen, task.inlineBash.Completed)
			case "later-background":
				value := true
				retainBashTaskHistory(&task, &NativeTaskObservation{Kind: TaskUpdated, Patch: &TaskPatch{Backgrounded: &value}}, string(domain.NewID()), []byte("fixture"))
				value = false
				retainBashTaskHistory(&task, &NativeTaskObservation{Kind: TaskUpdated, Patch: &TaskPatch{Backgrounded: &value}}, string(domain.NewID()), []byte("fixture"))
			}
			b.tasks["original-task"] = task
			b.content.tools["toolu_original_bash"] = tool
			if closed, err := s.CloseForContinuation(context.Background()); err == nil || closed != nil {
				t.Fatal("unproved task history granted replacement")
			}
		})
	}
}
