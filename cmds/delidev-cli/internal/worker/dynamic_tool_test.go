// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureDynamicResponder struct {
	t        *testing.T
	root     string
	calls    int
	delivery domain.DynamicReplyDelivery
	lost     bool
}

func (f *fixtureDynamicResponder) ReplyDynamicUnavailable(ctx context.Context, token, turn, claim domain.ID, commit func() error) (domain.DynamicToolReply, error) {
	f.calls++
	prepared, err := os.ReadFile(filepath.Join(f.root, "reply.json"))
	if err != nil || !strings.Contains(string(prepared), `"state":"prepared"`) {
		f.t.Fatal("reply preceded durable ownership", err)
	}
	if err := commit(); err != nil {
		f.t.Fatal(err)
	}
	intent, _ := os.ReadFile(filepath.Join(f.root, "reply.json"))
	if !strings.Contains(string(intent), `"state":"send-intent"`) {
		f.t.Fatal("wire preceded durable send intent")
	}
	reply := domain.DynamicToolReply{Token: token, ClaimID: claim, Delivery: f.delivery, Unavailable: true}
	if f.lost {
		return reply, publicationUncertain()
	}
	return reply, nil
}
func dynamicWorkerFixture(t *testing.T) (*CodexEventPublisher, codex.Event) {
	t.Helper()
	c, _ := codexTerminalStatusFixture(t)
	identity := domain.DynamicToolIdentity{Tool: "fixture_tool", ArgumentsDigest: strings.Repeat("a", 64)}
	event := codex.Event{Kind: codex.DynamicToolEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: "dynamic-call", Correlated: true, DynamicTool: &domain.DynamicToolUpdate{NativeID: "dynamic-call", Stage: domain.DynamicStarted, Snapshot: &domain.DynamicToolSnapshot{Identity: identity, Status: "inProgress"}}}
	if _, err := c.PublishCore(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	event.DynamicTool = &domain.DynamicToolUpdate{NativeID: event.ItemID, Stage: domain.DynamicRequested, Request: &domain.DynamicToolRequest{Token: domain.NewID(), IDKind: domain.InteractionNumberID, IDValue: "7", Identity: identity}}
	if _, err := c.PublishCore(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	return c, event
}
func TestDynamicDurableOwnerBeforeWireAndRestartNeverResends(t *testing.T) {
	for _, delivery := range []domain.DynamicReplyDelivery{domain.DynamicTransmitted, domain.DynamicUncertain, domain.DynamicNotSent} {
		t.Run(string(delivery), func(t *testing.T) {
			c, event := dynamicWorkerFixture(t)
			root := filepath.Join(c.publisher.config.Root, "jobs", string(c.publisher.job), "dynamic-replies", "by-call", dynamicJournalKey(event.ItemID))
			client := &fixtureDynamicResponder{t: t, root: root, delivery: delivery, lost: delivery == domain.DynamicUncertain}
			err := c.deliverDynamicUnavailable(context.Background(), context.Background(), event, client)
			if delivery == domain.DynamicUncertain && err == nil || delivery != domain.DynamicUncertain && err != nil || client.calls != 1 {
				t.Fatal("delivery boundary lost", err)
			}
			raw, err := os.ReadFile(filepath.Join(root, "reply.json"))
			var original dynamicReplyJournal
			if err != nil || domain.Decode(raw, &original) != nil || original.Delivery != delivery || original.Request.Token != event.DynamicTool.Request.Token || original.SessionID != c.publisher.input.SessionID || original.ExecutionID != c.publisher.execution || original.CallID != event.ItemID {
				t.Fatal("original journal lost", err)
			}
			if _, err := inspectDynamicReplyJournal(filepath.Join(root, "reply.json"), original); err != nil {
				t.Fatal(err)
			}
			// Simulate a recreated arrival token with the same original call/request.
			retained := c.dynamicTools[event.ItemID]
			retained.Tool.Reply = nil
			retained.Tool.Request.Token = domain.NewID()
			c.dynamicTools[event.ItemID] = retained
			event.DynamicTool.Request.Token = retained.Tool.Request.Token
			if err := c.deliverDynamicUnavailable(context.Background(), context.Background(), event, client); err == nil || client.calls != 1 {
				t.Fatal("restored intent repeated reply")
			}
			original.ExecutionID = domain.NewID()
			if _, err := inspectDynamicReplyJournal(filepath.Join(root, "reply.json"), original); err == nil {
				t.Fatal("foreign journal acquired authority")
			}
		})
	}
}
func TestDynamicNativeCompletionAndFailedTurnDoNotSettleRequest(t *testing.T) {
	c, event := dynamicWorkerFixture(t)
	identity := event.DynamicTool.Request.Identity
	event.DynamicTool = &domain.DynamicToolUpdate{NativeID: event.ItemID, Stage: domain.DynamicCompleted, Snapshot: &domain.DynamicToolSnapshot{Identity: identity, Status: "completed"}}
	if _, err := c.PublishCore(context.Background(), event); err != nil || c.dynamicToolsSettled() {
		t.Fatal("completion settled request", err)
	}
	terminal := codex.Event{Kind: codex.TurnCompletedEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnFailed}}
	if _, err := c.PublishCore(context.Background(), terminal); err != nil || !c.finished || c.dynamicToolsSettled() {
		t.Fatal("root failure erased original request", err)
	}
}
func TestDynamicCanceledControllerDoesNotSend(t *testing.T) {
	c, event := dynamicWorkerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &fixtureDynamicResponder{t: t}
	if err := c.deliverDynamicUnavailable(ctx, context.Background(), event, client); err == nil || client.calls != 0 {
		t.Fatal("canceled owner replied")
	}
}
