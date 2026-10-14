package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func claudeProtectedTextFixture(t *testing.T) (*ClaudeContentPublisher, *openCodeBindingRPC, func(claude.ContentEvent) (bool, error)) {
	t.Helper()
	c, rpc := newClaudeContentFixture(t)
	if err := c.PublishInput(context.Background()); err != nil {
		t.Fatal(err)
	}
	publish := func(content claude.ContentEvent) (bool, error) {
		return c.PublishObservation(context.Background(), claudeContentObservation(c, content))
	}
	if _, err := publish(claude.ContentEvent{Kind: claude.ProviderMessageStarted}); err != nil {
		t.Fatal(err)
	}
	zero, empty := uint32(0), ""
	if _, err := publish(claude.ContentEvent{Kind: claude.ContentStarted, Index: &zero, Block: &claude.NativeContentBlock{Kind: claude.TextBlock, Text: &empty}}); err != nil {
		t.Fatal(err)
	}
	return c, rpc, publish
}

func TestClaudeContentKeepsWithheldDeltaPrivateAndReleasesTailBeforeCompletion(t *testing.T) {
	c, rpc, publish := claudeProtectedTextFixture(t)
	zero := uint32(0)
	first, full := "Safe text ", "Safe text fixture-"
	before := len(rpc.events)
	if handled, err := publish(claude.ContentEvent{Kind: claude.ContentChanged, Index: &zero, DeltaKind: claude.TextDelta, Delta: &first}); !handled || err != nil {
		t.Fatal(err)
	}
	if handled, err := publish(claude.ContentEvent{Kind: claude.ContentChanged, Index: &zero, DeltaKind: claude.TextDelta, Withheld: true}); !handled || err != nil {
		t.Fatal(err)
	}
	if len(rpc.events) != before+1 {
		t.Fatal("a withheld delta produced a durable event")
	}
	tail := "fixture-"
	if handled, err := publish(claude.ContentEvent{Kind: claude.ContentCompleted, Index: &zero, Block: &claude.NativeContentBlock{Kind: claude.TextBlock, Text: &full}, Held: &tail}); !handled || err != nil {
		t.Fatal(err)
	}
	if len(rpc.events) != before+3 {
		t.Fatal("clean completion must publish exactly the ordered tail and the exact completion")
	}
	var appended, completed domain.ExecutionEvent
	if json.Unmarshal(rpc.events[before+1], &appended) != nil || json.Unmarshal(rpc.events[before+2], &completed) != nil {
		t.Fatal("durable events are not decodable")
	}
	if appended.ClaudeMessage == nil || appended.ClaudeMessage.Mutation != domain.ClaudeBlockAppend || *appended.ClaudeMessage.Delta != tail || completed.ClaudeMessage == nil || completed.ClaudeMessage.Mutation != domain.ClaudeBlockComplete || appended.Sequence+1 != completed.Sequence {
		t.Fatal("tail and completion lost their order or mutation kinds")
	}
	if c.messages["msg_original"].content.Blocks[0].Block.Text != full {
		t.Fatal("published block no longer equals the exact native block")
	}
}

func TestClaudeContentRejectsMalformedWithheldAndHeldObservations(t *testing.T) {
	zero, text, empty := uint32(0), "text", ""
	for _, name := range []string{"withheld-with-delta", "withheld-wrong-kind", "held-on-delta", "held-empty", "held-mismatch"} {
		t.Run(name, func(t *testing.T) {
			_, rpc, publish := claudeProtectedTextFixture(t)
			before := len(rpc.events)
			var event claude.ContentEvent
			switch name {
			case "withheld-with-delta":
				event = claude.ContentEvent{Kind: claude.ContentChanged, Index: &zero, DeltaKind: claude.TextDelta, Withheld: true, Delta: &text}
			case "withheld-wrong-kind":
				event = claude.ContentEvent{Kind: claude.ContentChanged, Index: &zero, DeltaKind: claude.ThinkingDelta, Withheld: true}
			case "held-on-delta":
				event = claude.ContentEvent{Kind: claude.ContentChanged, Index: &zero, DeltaKind: claude.TextDelta, Delta: &text, Held: &text}
			case "held-empty":
				event = claude.ContentEvent{Kind: claude.ContentCompleted, Index: &zero, Block: &claude.NativeContentBlock{Kind: claude.TextBlock, Text: &empty}, Held: &empty}
			case "held-mismatch":
				event = claude.ContentEvent{Kind: claude.ContentCompleted, Index: &zero, Block: &claude.NativeContentBlock{Kind: claude.TextBlock, Text: &empty}, Held: &text}
			}
			if _, err := publish(event); err == nil || len(rpc.events) != before {
				t.Fatal("malformed private holdback observation was accepted or published")
			}
		})
	}
}

func TestClaudeToolPublisherKeepsEscapedProtectedInputPrivate(t *testing.T) {
	c, rpc := newClaudeContentFixture(t)
	if err := c.PublishInput(context.Background()); err != nil {
		t.Fatal(err)
	}
	publish := func(event claude.ContentEvent) {
		t.Helper()
		if handled, err := c.PublishObservation(context.Background(), claudeContentObservation(c, event)); !handled || err != nil {
			t.Fatal("original tool observation was not accepted", err)
		}
	}
	publish(claude.ContentEvent{Kind: claude.ProviderMessageStarted})
	zero := uint32(0)
	publish(claude.ContentEvent{Kind: claude.ContentStarted, Index: &zero, Block: &claude.NativeContentBlock{Kind: claude.ToolUseBlock, Tool: &claude.NativeTool{ID: "tool_escaped", Name: "Write", Input: json.RawMessage(`{}`)}}})
	stream := security.NewProtectedJSON([]string{"abc"}).NewStream(security.StreamJSONText)
	parts := []string{`{"value":"`, `\u00`, `61b`, `c"}`}
	for index, part := range parts {
		released, err := stream.Push(part)
		if index == len(parts)-1 {
			if !errors.Is(err, security.ErrProtectedStreamValue) || released != "" {
				t.Fatal("completing fragment left the original guard", err)
			}
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		event := claude.ContentEvent{Kind: claude.ContentChanged, Index: &zero, DeltaKind: claude.ToolInputDelta}
		if released == "" {
			event.Withheld = true
		} else {
			event.Delta = &released
		}
		publish(event)
	}
	var appended strings.Builder
	for _, raw := range rpc.events {
		var event domain.ExecutionEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		if event.ClaudeTool != nil && event.ClaudeTool.Mutation == domain.ClaudeToolInputAppend && event.ClaudeTool.Delta != nil {
			appended.WriteString(*event.ClaudeTool.Delta)
		}
	}
	if !strings.HasPrefix(strings.Join(parts, ""), appended.String()) || strings.Contains(appended.String(), `\u0061bc`) || len(appended.String()) >= len(`{"value":"\u0061bc`) {
		t.Fatal("tool publisher exposed a complete decodable protected value")
	}
}
