package worker

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type dynamicFixtureResponder struct {
	t         *testing.T
	mapper    *CodexEventPublisher
	request   codex.DynamicRequest
	calls     int
	uncertain bool
}

func (f *dynamicFixtureResponder) ReplyDynamicUnavailable(ctx context.Context, id domain.ID, persist func(codex.DynamicReplyState) error) (codex.DynamicReplyState, error) {
	f.calls++
	state := codex.DynamicReplyState{Request: f.request, Stage: codex.DynamicSendIntent, Closure: codex.InteractionOpen}
	if err := persist(state); err != nil {
		return state, err
	}
	raw, err := security.ReadPrivate(f.mapper.dynamicPath(id), 64<<10)
	var saved dynamicReplyJournal
	if err != nil || domain.Decode(raw, &saved) != nil || saved.Reply.Stage != codex.DynamicSendIntent || saved.JobID != f.mapper.publisher.job || saved.ExecutionID != f.mapper.publisher.execution {
		f.t.Fatal("wire was not preceded by durable original ownership")
	}
	state.Stage = codex.DynamicTransmitted
	if f.uncertain {
		state.Stage = codex.DynamicUncertain
	}
	if err := persist(state); err != nil {
		return state, err
	}
	if f.uncertain {
		return state, errors.New("fixture lost native delivery")
	}
	return state, nil
}
func dynamicWorkerEvent(c *CodexEventPublisher) codex.Event {
	number := int64(7)
	request := codex.DynamicRequest{ID: domain.NewID(), NativeID: codex.NativeRequestID{Kind: codex.NumberRequestID, Number: &number}, ThreadID: c.thread, TurnID: c.turn, CallID: "dynamic-call", Tool: "fixture_tool", ArgumentsDigest: strings.Repeat("a", 64)}
	return codex.Event{Kind: codex.DynamicRequestedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: request.CallID, Correlated: true, DynamicRequest: &request}
}
func TestDynamicOriginalWorkerIntentReplayAndUncertaintyNeverResend(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "delivered", true: "uncertain"}[uncertain], func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			event := dynamicWorkerEvent(c)
			native := &dynamicFixtureResponder{t: t, mapper: c, request: *event.DynamicRequest, uncertain: uncertain}
			before := len(rpc.events)
			err := c.handleDynamic(context.Background(), event, native)
			if (err != nil) != uncertain {
				t.Fatal(err)
			}
			if native.calls != 1 || len(rpc.events) != before || c.finished {
				t.Fatal("dynamic reply dispatched a handler or fabricated public/root result")
			}
			raw, err := security.ReadPrivate(c.dynamicPath(event.DynamicRequest.ID), 64<<10)
			var journal dynamicReplyJournal
			if err != nil || domain.Decode(raw, &journal) != nil || journal.Reply.Stage == codex.DynamicObserved || journal.Reply.Closure != codex.InteractionOpen {
				t.Fatal("lost original attempted reply")
			}
			if c.handleDynamic(context.Background(), event, native) == nil || native.calls != 1 {
				t.Fatal("duplicate request repeated reply")
			}
			// Reconstructed bookkeeping cannot adopt an existing original directory.
			c.blocked = false
			c.dynamicReplies = map[domain.ID]dynamicReplyJournal{}
			if c.handleDynamic(context.Background(), event, native) == nil || native.calls != 1 {
				t.Fatal("restart adopted original attempt")
			}
		})
	}
}
func TestDynamicResolutionAndRootClosureRemainSeparate(t *testing.T) {
	c, _ := codexTerminalStatusFixture(t)
	event := dynamicWorkerEvent(c)
	native := &dynamicFixtureResponder{t: t, mapper: c, request: *event.DynamicRequest}
	if err := c.handleDynamic(context.Background(), event, native); err != nil {
		t.Fatal(err)
	}
	state := c.dynamicReplies[event.DynamicRequest.ID].Reply
	state.Closure = codex.InteractionNativeClosed
	resolved := codex.Event{Kind: codex.DynamicResolvedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: state.Request.CallID, Correlated: true, DynamicReply: &state}
	if err := c.handleDynamic(context.Background(), resolved, nil); err != nil || c.finished {
		t.Fatal("resolution completed root", err)
	}
	if err := c.endDynamicReplies(); err != nil {
		t.Fatal(err)
	}
	if c.dynamicReplies[state.Request.ID].Reply.Closure != codex.InteractionNativeClosed || native.calls != 1 {
		t.Fatal("turn end replaced observed resolution")
	}
	if _, err := os.Stat(c.dynamicPath(state.Request.ID)); err != nil {
		t.Fatal("original evidence removed")
	}
}

func TestDynamicLateNativeResolutionDoesNotRewriteRootCompletion(t *testing.T) {
	c, _ := codexTerminalStatusFixture(t)
	event := dynamicWorkerEvent(c)
	native := &dynamicFixtureResponder{t: t, mapper: c, request: *event.DynamicRequest}
	if err := c.handleDynamic(context.Background(), event, native); err != nil {
		t.Fatal(err)
	}
	if err := c.endDynamicReplies(); err != nil {
		t.Fatal(err)
	}
	c.finished = true
	state := c.dynamicReplies[event.DynamicRequest.ID].Reply
	state.Closure = codex.InteractionNativeClosed
	resolved := codex.Event{Kind: codex.DynamicResolvedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: state.Request.CallID, Correlated: true, Late: true, DynamicReply: &state}
	if err := c.handleDynamic(context.Background(), resolved, nil); err != nil || !c.finished || native.calls != 1 {
		t.Fatal("late resolution changed root or repeated reply", err)
	}
}
