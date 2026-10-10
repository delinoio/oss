// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCodexGuardianNoticesPublishOnlyAdvisoriesAndOneOriginalResult(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	c.publisher.input.Configuration.Options.ApprovalsReviewer = domain.CodexReviewerAuto
	beforeOptions := c.publisher.input.Configuration.Options
	before := len(rpc.events)
	started := int64(0)
	for _, event := range []codex.Event{
		{Kind: codex.NoticeEvent, ThreadID: c.thread, Correlated: true, Notice: domain.NativeWarning},
		{Kind: codex.NoticeEvent, ThreadID: c.thread, Correlated: true, Notice: domain.NativeConfigWarning},
		{Kind: codex.NoticeEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Notice: domain.NativeWarning, StrictReviewStartedAtMS: &started},
	} {
		if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil || c.finished || c.blocked {
			t.Fatal("advisory interrupted original turn", err)
		}
	}
	if len(rpc.events) != before+3 || !reflect.DeepEqual(beforeOptions, c.publisher.input.Configuration.Options) {
		t.Fatal("notice changed execution configuration")
	}
	replay := codex.Event{Kind: codex.MetadataEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Metadata: codex.StrictReviewReplayChecked}
	if handled, err := c.PublishCore(context.Background(), replay); !handled || err != nil || len(rpc.events) != before+3 {
		t.Fatal("replay acquired publication authority", err)
	}
	terminal := codex.Event{Kind: codex.TurnCompletedEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
	if handled, err := c.PublishCore(context.Background(), terminal); !handled || err != nil || !c.finished || len(rpc.events) != before+4 {
		t.Fatal("original result changed", err)
	}
	if handled, err := c.PublishCore(context.Background(), replay); !handled || err != nil || len(rpc.events) != before+4 {
		t.Fatal("terminal replay changed original result", err)
	}
	for _, raw := range rpc.events[before : before+3] {
		data := raw
		if strings.Contains(string(data), "startedAt") || strings.Contains(string(data), "review_id") || strings.Contains(string(data), "auto_review") {
			t.Fatal("advisory fabricated review progress")
		}
	}
}

func TestCodexStrictReviewNoticesKeepReviewerAndRecoveryFences(t *testing.T) {
	for _, mismatch := range []string{"reviewer", "thread", "turn", "uncorrelated", "negative", "blocked"} {
		t.Run(mismatch, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			c.publisher.input.Configuration.Options.ApprovalsReviewer = domain.CodexReviewerAuto
			stamp := int64(0)
			event := codex.Event{Kind: codex.NoticeEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Notice: domain.NativeWarning, StrictReviewStartedAtMS: &stamp}
			switch mismatch {
			case "reviewer":
				c.publisher.input.Configuration.Options.ApprovalsReviewer = domain.CodexReviewerUser
			case "thread":
				event.ThreadID = domain.NewID()
			case "turn":
				event.TurnID = domain.NewID()
			case "uncorrelated":
				event.Correlated = false
			case "negative":
				stamp = -1
			case "blocked":
				c.blocked = true
			}
			before := len(rpc.events)
			if handled, err := c.PublishCore(context.Background(), event); handled || err == nil || len(rpc.events) != before || !c.blocked {
				t.Fatal("notice relaxed original fence", handled, err)
			}
		})
	}
}
