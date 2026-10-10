// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCodexLifecycleMetadataHasNoPublicMutation(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	before := len(rpc.events)
	for _, kind := range []codex.MetadataKind{codex.NativeThreadMetadataDiscarded, codex.NativeProjectMetadataDiscarded, codex.NativeLifecycleSupplementDiscarded} {
		for n := 0; n < 3; n++ {
			event := codex.Event{Kind: codex.MetadataEvent, Metadata: kind, Correlated: true, ThreadID: c.thread}
			handled, err := c.PublishCore(context.Background(), event)
			if err != nil || !handled || c.finished || c.blocked || len(rpc.events) != before {
				t.Fatal("private metadata mutated public execution", handled, err)
			}
		}
	}
	terminal := codex.Event{Kind: codex.TurnCompletedEvent, Correlated: true, ThreadID: c.thread, TurnID: c.turn, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
	if handled, err := c.PublishCore(context.Background(), terminal); !handled || err != nil || !c.finished || len(rpc.events) != before+1 {
		t.Fatal("original completion changed", err)
	}
}

func TestCodexNativeThreadLossBlocksWithoutCompletingOrCleaning(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	before := len(rpc.events)
	problem := domain.Fail(domain.RecoveryRequired, "Original lifecycle requires reconciliation.", "Preserve original cleanup.")
	loss := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.NativeThreadLifecycleLost, Correlated: true, ThreadID: c.thread, Problem: problem}
	if handled, err := c.PublishCore(context.Background(), loss); !handled || err != problem || !c.blocked || c.finished || len(rpc.events) != before {
		t.Fatal("loss fabricated terminal or cleanup proof", handled, err)
	}
	for _, event := range []codex.Event{
		{Kind: codex.MetadataEvent, Metadata: codex.NativeThreadMetadataDiscarded, Correlated: true, ThreadID: c.thread},
		{Kind: codex.TurnCompletedEvent, Correlated: true, ThreadID: c.thread, TurnID: c.turn, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}},
	} {
		if handled, _ := c.PublishCore(context.Background(), event); handled || !c.blocked || c.finished || len(rpc.events) != before {
			t.Fatal("later metadata/terminal cleared original loss fence")
		}
	}
	if _, err := c.completionInputs(); err == nil {
		t.Fatal("loss granted complete input history")
	}
}

func TestCodexLifecycleMetadataRetainsForeignAndUnknownFences(t *testing.T) {
	for _, kind := range []codex.MetadataKind{codex.NativeThreadMetadataDiscarded, codex.NativeProjectMetadataDiscarded, codex.NativeLifecycleSupplementDiscarded, codex.NativeThreadLifecycleLost} {
		c, rpc := codexTerminalStatusFixture(t)
		before := len(rpc.events)
		event := codex.Event{Kind: codex.MetadataEvent, Metadata: kind, Correlated: true, ThreadID: domain.NewID()}
		if handled, _ := c.PublishCore(context.Background(), event); handled || len(rpc.events) != before || c.finished {
			t.Fatal("foreign metadata adopted")
		}
	}
	c, _ := codexTerminalStatusFixture(t)
	event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.NativeThreadLifecycleLost, Correlated: true, ThreadID: c.thread}
	if handled, err := c.PublishCore(context.Background(), event); handled || err == nil {
		t.Fatal("missing original recovery fact accepted")
	}
}
