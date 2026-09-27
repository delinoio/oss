package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

// A single provider message proposes two calls; the native user envelope then
// returns both original results. Lost acknowledgments must never replay tools.
func claudeToolFixture(t *testing.T, lose string) (*ClaudeContentPublisher, *openCodeBindingRPC, claude.LifecycleObservation) {
	t.Helper()
	c, rpc := newClaudeContentFixture(t)
	ctx := context.Background()
	if err := c.PublishInput(ctx); err != nil {
		t.Fatal(err)
	}
	publish := func(event claude.ContentEvent, stage string) {
		t.Helper()
		o := claudeContentObservation(c, event)
		rpc.lose = stage == lose
		handled, err := c.PublishObservation(ctx, o)
		if !handled {
			t.Fatal("original tool content unhandled")
		}
		if rpc.lose {
			if err == nil {
				t.Fatal("lost acknowledgment was accepted")
			}
			last := len(rpc.events) - 1
			rpc.lose = false
			if err := c.ReplayPending(ctx); err != nil {
				t.Fatal(err)
			}
			if rpc.requests[last] != rpc.requests[last+1] || !bytes.Equal(rpc.events[last], rpc.events[last+1]) {
				t.Fatal("tool publication replay changed original receipt")
			}
		} else if err != nil {
			t.Fatal(stage, err)
		}
	}
	publish(claude.ContentEvent{Kind: claude.ProviderMessageStarted}, "provider")
	results := claudeContentObservation(c, claude.ContentEvent{})
	results.Content = nil
	for n, id := range []string{"tool_original_one", "tool_original_two"} {
		index := uint32(n)
		native := &claude.NativeTool{ID: id, Name: "Read", Input: json.RawMessage(`{}`)}
		block := &claude.NativeContentBlock{Kind: claude.ToolUseBlock, Tool: native}
		publish(claude.ContentEvent{Kind: claude.ContentStarted, Index: &index, Block: block}, "start")
		delta := `{"file_path":"/private/fixture","large":9007199254740993}`
		publish(claude.ContentEvent{Kind: claude.ContentChanged, Index: &index, DeltaKind: claude.ToolInputDelta, Delta: &delta}, "input")
		native.Input, native.ProposedInput = json.RawMessage(delta), json.RawMessage(delta)
		publish(claude.ContentEvent{Kind: claude.ContentCompleted, Index: &index, Block: block}, "proposal")
		publish(claude.ContentEvent{Kind: claude.ContentStopped, Index: &index}, "stop")
		output, failed := "Original read result", n == 1
		results.Content = append(results.Content, claude.ContentEvent{Kind: claude.ToolResultObserved, MessageID: "msg_original", Index: &index, ToolResult: &claude.NativeToolResult{ID: id, Name: "Read", Text: &output, Error: &failed}})
	}
	publish(claude.ContentEvent{Kind: claude.ProviderMessageFinished}, "message-stop")
	return c, rpc, results
}

func TestClaudeToolsPreserveOriginalProposalsResultsAndReceipts(t *testing.T) {
	for _, lost := range []string{"start", "input", "proposal", "result"} {
		t.Run(lost, func(t *testing.T) {
			c, rpc, o := claudeToolFixture(t, lost)
			rpc.lose = lost == "result"
			handled, err := c.PublishObservation(context.Background(), o)
			if !handled {
				t.Fatal("original results unhandled")
			}
			if rpc.lose {
				if err == nil || len(c.queue) != 2 {
					t.Fatal("lost batch did not retain original results")
				}
				last := len(rpc.events) - 1
				rpc.lose = false
				if err := c.ReplayPending(context.Background()); err != nil {
					t.Fatal(err)
				}
				if rpc.requests[last] != rpc.requests[last+1] || !bytes.Equal(rpc.events[last], rpc.events[last+1]) {
					t.Fatal("result receipt changed")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !c.toolsComplete() {
				t.Fatal("original tool results missing")
			}
			for _, v := range c.tools {
				if v.content != nil {
					t.Fatal("complete payload retained after durable acknowledgment")
				}
			}
			results := 0
			seen := map[domain.ID]bool{}
			for index, raw := range rpc.events {
				if seen[rpc.requests[index]] {
					continue
				}
				seen[rpc.requests[index]] = true
				var e domain.ExecutionEvent
				if json.Unmarshal(raw, &e) != nil {
					t.Fatal("invalid publication")
				}
				if e.Kind == domain.ExecutionTurnFinished {
					t.Fatal("tool result fabricated terminal outcome")
				}
				if e.ClaudeTool != nil {
					results++
					if e.ClaudeTool.Result.NativeEventID != o.NativeID || e.ClaudeTool.Result.Error == nil || *e.ClaudeTool.Result.Text != "Original read result" {
						t.Fatal("result envelope or nullable error changed")
					}
				}
			}
			if results != 2 {
				t.Fatal("native result batch lost or duplicated calls", results)
			}
		})
	}
}

func TestClaudeToolResultBatchRejectsAllBeforePublishingAny(t *testing.T) {
	for _, change := range []string{"foreign", "duplicate", "child", "caller", "rich", "wrong-index"} {
		t.Run(change, func(t *testing.T) {
			c, rpc, o := claudeToolFixture(t, "")
			bad := &o.Content[1]
			switch change {
			case "foreign":
				bad.MessageID = "msg_foreign"
			case "duplicate":
				*bad = o.Content[0]
			case "child":
				bad.ParentToolID = "tool_child"
			case "caller":
				bad.ToolResult.CalledBy.ToolID = "provider_parent"
			case "rich":
				bad.ToolResult.Text = nil
				bad.ToolResult.Blocks = []claude.NativeContentBlock{{Kind: claude.ThinkingBlock}}
			case "wrong-index":
				index := uint32(0)
				bad.Index = &index
			}
			before := len(rpc.events)
			if _, err := c.PublishObservation(context.Background(), o); err == nil || len(rpc.events) != before || len(c.queue) != 0 {
				t.Fatal("invalid second result partially published batch")
			}
			for _, v := range c.tools {
				if v.state != domain.MessageStreaming || v.content.Result != nil {
					t.Fatal("rejected batch changed prior tool")
				}
			}
			if _, err := c.PublishObservation(context.Background(), o); err == nil {
				t.Fatal("latched invalid batch recovered silently")
			}
		})
	}
}
