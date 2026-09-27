package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestManualNativeClaudePublicBashTask(t *testing.T) {
	nativeClaudePublicDispatch(t, domain.ExecuteMode, claudePublicBashTask)
}

func TestManualNativeClaudeBashTaskRecovery(t *testing.T) {
	for _, turn := range []int{1, 2} {
		t.Run(fmt.Sprintf("turn-%d", turn), func(t *testing.T) {
			nativeClaudePublicDispatch(t, domain.ExecuteMode, claudePublicBashTask, claudePublicRecoveryCase{turn: turn})
		})
	}
}

func verifyClaudePublicBashTask(t *testing.T, ctx context.Context, f *firstDispatchFixture, completion domain.ExecutionCompletion) {
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
			count++
		}
	}
	if started != 1 || finished != 1 {
		t.Fatal("native task lifecycle missing", started, finished)
	}
	t.Logf("original tool progress events: %d", count)
	session, err := store.Decode[domain.Session](f.refresh(t))
	if err != nil || session.Execution.ClaudeTasks == nil || !session.Execution.ClaudeTasks.Closed() || !session.Execution.ClaudeTasks.InlineBashHistoryReady() || len(session.Execution.ClaudeTasks.Tasks) != 1 {
		t.Fatal("task state lost original closed work", err)
	}
}
