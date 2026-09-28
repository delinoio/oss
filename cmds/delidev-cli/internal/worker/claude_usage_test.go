package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func TestClaudeUsagePublishesExactOriginalReportsWithoutCharges(t *testing.T) {
	c, rpc := newClaudeContentFixture(t)
	ctx := context.Background()
	if err := c.PublishInput(ctx); err != nil {
		t.Fatal(err)
	}
	var native claude.ProviderUsage
	if err := json.Unmarshal([]byte(`{"input_tokens":9007199254740993,"output_tokens":0,"cache_read_input_tokens":null,"iterations":[{"type":"advisor_message","model":"advisor","output_tokens":2}]}`), &native); err != nil {
		t.Fatal(err)
	}
	o := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ProviderMessageStarted, Usage: &native})
	if _, err := c.PublishObservation(ctx, o); err != nil {
		t.Fatal(err)
	}
	rpc.lose = true
	if handled, err := c.PublishUsageObservation(ctx, o); !handled || err == nil {
		t.Fatal("lost usage acknowledgment was not retained", err)
	}
	original := len(rpc.events) - 1
	rpc.lose = false
	if err := c.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if rpc.requests[original] != rpc.requests[original+1] || !bytes.Equal(rpc.events[original], rpc.events[original+1]) {
		t.Fatal("usage retry replaced original receipt")
	}
	var event domain.ExecutionEvent
	if domain.Decode(rpc.events[original], &event) != nil || event.Kind != domain.ExecutionClaudeUsageObserved || *event.ClaudeUsage.Provider.Input != "9007199254740993" || *event.ClaudeUsage.Provider.Output != "0" || event.ClaudeUsage.Provider.CacheRead != nil {
		t.Fatal("exact native counters changed")
	}
	if _, err := c.PublishObservation(ctx, claudeContentObservation(c, claude.ContentEvent{Kind: claude.ProviderMessageFinished})); err != nil {
		t.Fatal(err)
	}
	cost := claude.NativeUSD("0.0006994999999999999")
	result := claude.LifecycleObservation{Kind: claude.InputFinished, SessionID: o.SessionID, InputID: o.InputID, TurnID: o.TurnID, Accepted: true, NativeID: string(domain.NewID()), Result: &claude.NativeResult{Usage: &claude.ResultUsage{MainLoop: &native, Models: map[string]claude.NativeModelUsage{"fixture": {Input: native.Input, CostUSD: &cost}}, NativeCostUSD: &cost}}}
	rpc.lose = true
	if handled, err := c.PublishUsageObservation(ctx, result); !handled || err == nil {
		t.Fatal("lost result acknowledgment was not retained", err)
	}
	rpc.lose = false
	if err := c.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if domain.Decode(rpc.events[len(rpc.events)-1], &event) != nil || event.ClaudeUsage.Source != domain.ClaudeInputResultUsage || *event.ClaudeUsage.Result.NativeCostUSD != domain.ClaudeNativeUSD(cost) {
		t.Fatal("native cumulative estimate changed")
	}
	if _, err := c.PublishUsageObservation(ctx, result); err == nil {
		t.Fatal("result report was repeated")
	}
}

func TestClaudeUsageRejectsForeignOrUnpublishedSources(t *testing.T) {
	for _, name := range []string{"unpublished", "session", "input", "turn", "model", "message", "event", "child", "duplicate", "pending-result", "missing-result", "counter"} {
		t.Run(name, func(t *testing.T) {
			c, _ := newClaudeContentFixture(t)
			ctx := context.Background()
			if err := c.PublishInput(ctx); err != nil {
				t.Fatal(err)
			}
			n := int64(1)
			o := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ProviderMessageStarted, Usage: &claude.ProviderUsage{Input: &n}})
			if name != "unpublished" {
				if _, err := c.PublishObservation(ctx, o); err != nil {
					t.Fatal(err)
				}
			}
			switch name {
			case "session":
				o.SessionID = domain.NewID()
			case "input":
				o.InputID = domain.NewID()
			case "turn":
				o.TurnID = string(domain.NewID())
			case "model":
				o.Content[0].Model = "other"
			case "message":
				o.Content[0].MessageID = "other"
			case "event":
				o.NativeID = string(domain.NewID())
			case "child":
				o.Content[0].ParentToolID = "child"
			case "duplicate":
				if _, err := c.PublishUsageObservation(ctx, o); err != nil {
					t.Fatal(err)
				}
			case "pending-result":
				o.Kind = claude.InputFinished
				o.Result = &claude.NativeResult{Usage: &claude.ResultUsage{}}
			case "missing-result":
				o.Kind = claude.InputFinished
			case "counter":
				n = -1
			}
			if _, err := c.PublishUsageObservation(ctx, o); err == nil || c.binding.stage != claudeBindingBlocked {
				t.Fatal("invalid original usage accepted", err)
			}
		})
	}
}
