package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestManualNativeClaudePublicBashTask(t *testing.T) {
	nativeClaudePublicDispatch(t, domain.ExecuteMode, claudePublicBashTask)
}

func TestManualNativeClaudePublicLongBashProgress(t *testing.T) {
	nativeClaudePublicDispatch(t, domain.ExecuteMode, claudePublicBashToolProgress)
}

func TestManualNativeClaudeBashTaskRecovery(t *testing.T) {
	for _, turn := range []int{1, 2} {
		t.Run(fmt.Sprintf("turn-%d", turn), func(t *testing.T) {
			nativeClaudePublicDispatch(t, domain.ExecuteMode, claudePublicBashTask, claudePublicRecoveryCase{turn: turn})
		})
	}
}

func verifyClaudePublicBashTask(t *testing.T, ctx context.Context, f *firstDispatchFixture, completion domain.ExecutionCompletion, requireToolProgress bool) {
	t.Helper()
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: domain.ID(f.change.Session.Id), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	count, started, finished := 0, 0, 0
	for _, row := range rows {
		m, err := store.Decode[domain.ExecutionMessage](row)
		if err != nil {
			t.Fatal(err)
		}
		if m.ExecutionID == completion.ExecutionID && m.ClaudeProgress != nil && m.ClaudeProgress.Kind == domain.ClaudeTaskLifecycleProgress {
			v := m.ClaudeProgress.Task
			if m.ClaudeProgress.Validate() != nil {
				t.Fatal("invalid original native task")
			}
			if v.Kind == domain.ClaudeTaskStarted {
				started++
				if v.Backgrounded != nil {
					t.Fatal("unexpected background mode observation")
				}
			}
			if v.Kind == domain.ClaudeTaskNotified && v.Status != nil && *v.Status == domain.ClaudeTaskCompleted {
				finished++
			}
			t.Logf("original task event: %s", v.Kind)
		}
		if m.ExecutionID == completion.ExecutionID && m.ClaudeProgress != nil && m.ClaudeProgress.Kind == domain.ClaudeToolProgress {
			if m.ClaudeProgress.Validate() != nil || m.ClaudeProgress.Tool.Tool.Name != "Bash" {
				t.Fatal("native tool progress lost original ownership")
			}
			p := m.ClaudeProgress.Tool
			if requireToolProgress && (p.ParentToolID == nil || *p.ParentToolID != p.Tool.NativeID || p.Heartbeat == nil || !*p.Heartbeat || !domain.ValidClaudeToolHeartbeat(p.Tool.NativeID, p.NativeToolID)) {
				t.Fatal("native heartbeat lost separate original identity or owner")
			}
			count++
		}
	}
	if started != 1 || finished != 1 {
		t.Fatal("native task lifecycle missing", started, finished)
	}
	t.Logf("original tool progress events: %d", count)
	if requireToolProgress && count == 0 {
		t.Fatal("long native Bash did not publish tool progress")
	}
	session, err := store.Decode[domain.Session](f.refresh(t))
	if err != nil || session.Execution.ClaudeTasks == nil || !session.Execution.ClaudeTasks.Closed() || !session.Execution.ClaudeTasks.InlineBashHistoryReady() || len(session.Execution.ClaudeTasks.Tasks) != 1 {
		t.Fatal("task state lost original closed work", err)
	}
}

func TestManualNativeClaudePublicBackgroundTaskStop(t *testing.T) {
	nativeClaudePublicDispatch(t, domain.ExecuteMode, claudePublicBackgroundStop)
}

func claudePublicBackgroundResult(t *testing.T, raw []byte, id, contains string) bool {
	t.Helper()
	var request struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &request) != nil {
		t.Error("invalid background result request")
		return false
	}
	found := 0
	for _, message := range request.Messages {
		if message.Role != "user" {
			continue
		}
		var blocks []struct {
			Type    string          `json:"type"`
			ID      string          `json:"tool_use_id"`
			Error   bool            `json:"is_error"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_result" && b.ID == id {
				if b.Error || len(b.Content) == 0 || contains != "" && !strings.Contains(string(b.Content), contains) {
					t.Error("background result changed original task or failed")
					return false
				}
				found++
			}
		}
	}
	if found != 1 {
		t.Error("missing or duplicated original background tool result")
		return false
	}
	return true
}

func verifyClaudePublicBackgroundStop(t *testing.T, ctx context.Context, f *firstDispatchFixture, completion domain.ExecutionCompletion) {
	t.Helper()
	session, err := store.Decode[domain.Session](f.refresh(t))
	if err != nil || completion.Version != 1 || session.Dispatch != domain.DispatchPaused || session.Execution.ClaudeTasks == nil || !session.Execution.ClaudeTasks.Closed() || session.Execution.ClaudeTasks.InlineBashHistoryReady() || len(session.Execution.ClaudeTasks.Tasks) != 1 {
		t.Fatal("background stop gained history authority or lost closure", err)
	}
	var taskID string
	for id, task := range session.Execution.ClaudeTasks.Tasks {
		if task.Tool.NativeID != "toolu_public_background" || task.Status != domain.ClaudeTaskStopped && task.Status != domain.ClaudeTaskKilled {
			t.Fatal("background task lost original stopped state")
		}
		taskID = id
	}
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: domain.ID(f.change.Session.Id), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var returned, stopped uint64
	started, background, cleared, stopResult := false, false, false, false
	for _, row := range rows {
		m, err := store.Decode[domain.ExecutionMessage](row)
		if err != nil {
			t.Fatal(err)
		}
		if m.ExecutionID != completion.ExecutionID {
			continue
		}
		if m.ClaudeTool != nil && m.State == domain.MessageComplete && m.ClaudeTool.Result != nil {
			switch m.ClaudeTool.Reference.NativeID {
			case "toolu_public_background":
				returned = m.LastSequence
			case "toolu_public_background_stop":
				stopResult = m.ClaudeTool.Reference.Name == "TaskStop" && m.ClaudeTool.Proposal != nil && strings.Contains(m.ClaudeTool.Proposal.Applied, taskID) && (m.ClaudeTool.Result.Error == nil || !*m.ClaudeTool.Result.Error)
			}
		}
		if m.ClaudeProgress == nil || m.ClaudeProgress.Task == nil {
			continue
		}
		v := m.ClaudeProgress.Task
		if v.Validate() != nil {
			t.Fatal("invalid original background task observation")
		}
		switch v.Kind {
		case domain.ClaudeTaskStarted:
			started = v.ID == taskID
		case domain.ClaudeBackgroundTasksChanged:
			if len(*v.Background) == 0 {
				cleared = true
			}
			for _, entry := range *v.Background {
				if entry.ID == taskID {
					background = true
				}
			}
		case domain.ClaudeTaskUpdated:
			if v.ID == taskID && v.Patch.Status != nil && *v.Patch.Status == domain.ClaudeTaskKilled {
				stopped = m.FirstSequence
			}
		case domain.ClaudeTaskNotified:
			if v.ID == taskID && v.Status != nil && *v.Status == domain.ClaudeTaskStopped {
				stopped = m.FirstSequence
			}
		}
		t.Logf("original background task event: %s", v.Kind)
	}
	if !started || !background || !cleared || !stopResult || returned == 0 || stopped <= returned {
		t.Fatal("background tool/task lifecycles were conflated", started, background, cleared, stopResult, returned, stopped)
	}
}
