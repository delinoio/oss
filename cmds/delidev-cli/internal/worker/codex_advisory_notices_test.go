// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"strings"
	"testing"
)

func TestCodexAdvisoryNoticesPublishNoApprovalAndCompleteOnlyOriginalTurn(t *testing.T) {
	for _, notice := range []domain.NativeNotice{domain.NativeWarning, domain.NativeConfigWarning} {
		t.Run(string(notice), func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			before := len(rpc.events)
			handled, err := c.PublishCore(context.Background(), codex.Event{Kind: codex.NoticeEvent, ThreadID: c.thread, TurnID: c.turn, Notice: notice, Correlated: true})
			if !handled || err != nil || c.finished || c.blocked || len(rpc.events) != before+1 {
				t.Fatal("notice failed", handled, err)
			}
			var event domain.ExecutionEvent
			if err := domain.Decode(rpc.events[len(rpc.events)-1], &event); err != nil {
				t.Fatal(err)
			}
			if event.Kind != domain.ExecutionNoticeObserved || event.Notice != notice || event.Interaction != nil || event.ApprovalResponse != nil {
				t.Fatal("notice acquired response authority")
			}
			raw, _ := json.Marshal(event)
			if strings.Contains(string(raw), "private-") {
				t.Fatal("private content published")
			}
			before = len(rpc.events)
			replay := codex.Event{Kind: codex.MetadataEvent, ThreadID: c.thread, TurnID: c.turn, Metadata: codex.StrictReviewReplayChecked, Correlated: true}
			if handled, err := c.PublishCore(context.Background(), replay); !handled || err != nil || len(rpc.events) != before {
				t.Fatal("replay republished notice", handled, err)
			}
			terminal := codex.Event{Kind: codex.TurnCompletedEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
			if handled, err := c.PublishCore(context.Background(), terminal); !handled || err != nil || !c.finished || len(rpc.events) != before+1 {
				t.Fatal("original completion failed", handled, err)
			}
			before = len(rpc.events)
			if handled, err := c.PublishCore(context.Background(), replay); !handled || err != nil || len(rpc.events) != before {
				t.Fatal("terminal replay changed result")
			}
		})
	}
}

func TestCodexStrictReviewReplayCannotRelaxForeignOrRecoveryFences(t *testing.T) {
	for _, change := range []string{"thread", "turn", "correlation", "blocked"} {
		t.Run(change, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			before := len(rpc.events)
			event := codex.Event{Kind: codex.MetadataEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Metadata: codex.StrictReviewReplayChecked}
			switch change {
			case "thread":
				event.ThreadID = domain.NewID()
			case "turn":
				event.TurnID = domain.NewID()
			case "correlation":
				event.Correlated = false
			case "blocked":
				c.blocked = true
			}
			handled, _ := c.PublishCore(context.Background(), event)
			if handled || len(rpc.events) != before || c.finished {
				t.Fatal("unowned replay gained authority")
			}
			if change == "blocked" && !c.blocked {
				t.Fatal("replay cleared recovery")
			}
		})
	}
}
