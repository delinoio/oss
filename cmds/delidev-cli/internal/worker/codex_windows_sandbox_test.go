// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCodexWindowsSandboxPublishesOnlyGenericWarningAndOriginalCompletion(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	before := len(rpc.events)
	warning := codex.Event{Kind: codex.NoticeEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Notice: domain.NativeWarning}
	if handled, err := c.PublishCore(context.Background(), warning); !handled || err != nil || len(rpc.events) != before+1 || c.finished || c.blocked {
		t.Fatal("generic warning failed", handled, err)
	}
	var published domain.ExecutionEvent
	if err := domain.Decode(rpc.events[len(rpc.events)-1], &published); err != nil {
		t.Fatal(err)
	}
	if published.Kind != domain.ExecutionNoticeObserved || published.Notice != domain.NativeWarning || published.Interaction != nil || published.ApprovalResponse != nil || published.ProblemCode != "" {
		t.Fatal("warning gained native content or approval authority")
	}
	before = len(rpc.events)
	for _, kind := range []codex.MetadataKind{codex.WindowsSandboxWarningDiscarded, codex.WindowsSandboxSetupDiscarded} {
		metadata := codex.Event{Kind: codex.MetadataEvent, ThreadID: c.thread, Correlated: true, Metadata: kind}
		for replay := 0; replay < 4; replay++ {
			if handled, err := c.PublishCore(context.Background(), metadata); !handled || err != nil || len(rpc.events) != before || c.finished || c.blocked {
				t.Fatal("discarded safety metadata changed publication/result", handled, err)
			}
		}
	}
	terminal := codex.Event{Kind: codex.TurnCompletedEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
	if handled, err := c.PublishCore(context.Background(), terminal); !handled || err != nil || !c.finished || len(rpc.events) != before+1 {
		t.Fatal("original completion was replaced", handled, err)
	}
	for _, kind := range []codex.MetadataKind{codex.WindowsSandboxWarningDiscarded, codex.WindowsSandboxSetupDiscarded} {
		event := codex.Event{Kind: codex.MetadataEvent, ThreadID: c.thread, Correlated: true, Metadata: kind}
		if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil || len(rpc.events) != before+1 || !c.finished {
			t.Fatal("late safety metadata changed completion", handled, err)
		}
	}
}

func TestCodexWindowsSandboxMetadataRetainsOwnershipAndRecoveryFences(t *testing.T) {
	for _, kind := range []codex.MetadataKind{codex.WindowsSandboxWarningDiscarded, codex.WindowsSandboxSetupDiscarded} {
		for _, change := range []string{"thread", "turn", "correlation", "blocked", "extension"} {
			c, rpc := codexTerminalStatusFixture(t)
			before := len(rpc.events)
			event := codex.Event{Kind: codex.MetadataEvent, ThreadID: c.thread, Metadata: kind, Correlated: true}
			switch change {
			case "thread":
				event.ThreadID = domain.NewID()
			case "turn":
				event.TurnID = domain.NewID()
			case "correlation":
				event.Correlated = false
			case "blocked":
				c.blocked = true
			case "extension":
				event.Kind = codex.NativeExtensionEvent
			}
			if handled, _ := c.PublishCore(context.Background(), event); handled || len(rpc.events) != before || c.finished {
				t.Fatal("unowned safety metadata acquired product authority", kind, change)
			}
			if change == "blocked" && !c.blocked {
				t.Fatal("setup or warning cleared recovery")
			}
		}
	}
}

func TestTitleWindowsSandboxDiscardRetainsNarrowMetadataProfile(t *testing.T) {
	for _, kind := range []codex.MetadataKind{codex.WindowsSandboxWarningDiscarded, codex.WindowsSandboxSetupDiscarded} {
		if err := validateTitleMetadata(kind); err != nil {
			t.Fatal("validated passive safety metadata interrupted title inference", err)
		}
	}
	for _, kind := range []codex.MetadataKind{codex.CodexAppsStartupObserved, codex.ThreadMetadataDiscarded, "unvalidated-windows-setup"} {
		if err := validateTitleMetadata(kind); domain.SafeError(err).Code != domain.Unsupported {
			t.Fatal("safety metadata support widened title authority", kind, err)
		}
	}
}
