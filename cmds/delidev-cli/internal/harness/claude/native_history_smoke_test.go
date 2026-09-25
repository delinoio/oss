package claude

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
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
		raw, metadata := nativeFixtureChildFiles(t, config, id)
		observation, err := VerifyChildTranscript(context.Background(), raw, metadata, config.SessionID, config.Workspace, ChildHistoryBinding{TaskID: id, ToolID: task.tool, AgentType: task.agentType}, proofs[task.tool])
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
	if err := security.CheckPrivateDir(config.Home); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(config.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	path := filepath.Join(config.Home, "projects", "delidev", string(config.SessionID), "subagents", "agent-"+id)
	raw, err := security.ReadPrivate(path+".jsonl", maxHistoryTranscript)
	if err != nil {
		t.Fatal(err)
	}
	// Native 2.1.236 creates this sidecar with mode 0644 inside our 0700
	// fixture runtime. Read it through that private root without changing
	// native bytes/permissions or weakening the product's ReadPrivate API.
	metadataPath, err := filepath.Rel(config.Home, path+".meta.json")
	if err != nil {
		t.Fatal(err)
	}
	file, err := root.Open(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	closeErr := file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil || len(metadata) > 64<<10 {
		t.Fatal("native fixture sidecar could not be read within its bound", closeErr)
	}
	return raw, metadata
}
