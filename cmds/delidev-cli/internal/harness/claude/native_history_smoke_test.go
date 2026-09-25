package claude

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func nativeFixtureChildHistory(t *testing.T, config APIStreamConfig, tasks map[string]nativeTaskState, proofs map[string][]HistoryMessageProof) {
	t.Helper()
	verified := 0
	for id, task := range tasks {
		if task.kind != LocalAgentTask {
			continue
		}
		if !task.status.terminal() || !task.notified || filepath.Base(id) != id || strings.ContainsAny(id, "/\\") {
			t.Fatal("child fixture has no settled original native task")
		}
		observation, err := ReadChildTranscript(context.Background(), config.Home, config.SessionID, config.Workspace, ChildHistoryBinding{TaskID: id, ToolID: task.tool, AgentType: task.agentType}, proofs[task.tool], config.Process.Logger)
		if err != nil {
			t.Fatal("child native transcript lost original task or forwarded content", err)
		}
		wantMatched, wantAdditional := uint32(0), uint32(2)
		if len(proofs[task.tool]) != 0 {
			wantMatched, wantAdditional = 3, 1
		}
		if observation.Transcript.MatchedMessages != wantMatched || observation.Transcript.AdditionalMessages != wantAdditional || observation.SpawnDepth != 1 {
			t.Fatal("child native transcript fabricated forwarded messages or lost unforwarded final content")
		}
		verified++
	}
	if verified != 1 {
		t.Fatal("fixture child history count changed")
	}
}

func nativeFixtureChildFiles(t *testing.T, config APIStreamConfig, id string) ([]byte, []byte) {
	t.Helper()
	if filepath.Base(id) != id || strings.ContainsAny(id, "/\\") {
		t.Fatal("invalid original child identity")
	}
	scope, err := openHistoryFiles(context.Background(), config.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()
	base := filepath.Join("projects", "delidev", string(config.SessionID), "subagents", "agent-"+id)
	raw, err := scope.read(context.Background(), base+".jsonl", maxHistoryTranscript)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := scope.read(context.Background(), base+".meta.json", 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.check(context.Background()); err != nil {
		t.Fatal(err)
	}
	return raw, metadata
}
