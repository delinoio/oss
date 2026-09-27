package worker

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func TestClaudeToolProgressSharesOriginalOutboxWithoutCompletingTools(t *testing.T) {
	c, rpc, result := claudeToolFixture(t, "")
	ctx := context.Background()
	elapsed, heartbeat := claude.NativeSeconds("9.007199254740993e+15"), false
	o := result
	o.NativeID = string(domain.NewID())
	o.Kind, o.Content, o.Progress = claude.ProgressObserved, nil, &claude.NativeProgressObservation{Kind: claude.ToolProgressObserved, ToolID: "tool_original_one", ToolName: "Read", Elapsed: &elapsed, Heartbeat: &heartbeat}
	rpc.lose = true
	if handled, err := c.PublishToolProgressObservation(ctx, o); !handled || err == nil {
		t.Fatal("lost progress acknowledgment accepted")
	}
	last := len(rpc.events) - 1
	if c.toolsComplete() || c.PublishInput(ctx) == nil {
		t.Fatal("progress completed tools or bypassed pending receipt")
	}
	rpc.lose = false
	if err := c.binding.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if rpc.requests[last] != rpc.requests[last+1] || !bytes.Equal(rpc.events[last], rpc.events[last+1]) {
		t.Fatal("original progress receipt changed")
	}
	var event domain.ExecutionEvent
	if domain.Decode(rpc.events[last], &event) != nil || event.ClaudeProgress.Observation.Tool.ElapsedSeconds != string(elapsed) || event.ClaudeProgress.Observation.Tool.Heartbeat == nil || *event.ClaudeProgress.Observation.Tool.Heartbeat {
		t.Fatal("native number or false heartbeat changed")
	}
	if _, err := c.PublishObservation(ctx, result); err != nil {
		t.Fatal(err)
	}
	for _, tool := range c.tools {
		if tool.content != nil {
			t.Fatal("completed payload retained")
		}
	}
	o.NativeID = string(domain.NewID())
	summary := "Original advisory summary"
	o.Progress = &claude.NativeProgressObservation{Kind: claude.ToolSummaryObserved, Summary: &summary, PrecedingTools: []string{"tool_original_two", "tool_original_one"}}
	if handled, err := c.PublishToolProgressObservation(ctx, o); !handled || err != nil {
		t.Fatal("summary lost released original tool identity", err)
	}
	if domain.Decode(rpc.events[len(rpc.events)-1], &event) != nil || event.ClaudeProgress.Observation.ToolSummary.Tools[0].NativeID != "tool_original_two" || c.binding.stage != claudeInputAccepted {
		t.Fatal("summary reordered references or finished input")
	}
}

func TestClaudeToolProgressRejectsUnownedTaskMixedAndCompletedTools(t *testing.T) {
	for _, scenario := range []string{"tool", "name", "input", "parent", "task", "subagent", "retry", "negative", "mixed", "finished", "heartbeat-parent", "heartbeat-false", "heartbeat-collision", "duplicate-summary", "foreign-summary"} {
		t.Run(scenario, func(t *testing.T) {
			c, rpc, result := claudeToolFixture(t, "")
			o := result
			elapsed := claude.NativeSeconds("0.001")
			o.Kind, o.Content, o.Progress = claude.ProgressObserved, nil, &claude.NativeProgressObservation{Kind: claude.ToolProgressObserved, ToolID: "tool_original_one", ToolName: "Read", Elapsed: &elapsed}
			switch scenario {
			case "heartbeat-parent", "heartbeat-false", "heartbeat-collision":
				parent, beat := "tool_original_one", true
				o.Progress.ToolID, o.Progress.ParentToolID, o.Progress.Heartbeat = parent+"-heartbeat-0", &parent, &beat
				if scenario == "heartbeat-parent" {
					parent = "foreign"
				}
				if scenario == "heartbeat-false" {
					beat = false
				}
				if scenario == "heartbeat-collision" {
					c.tools[o.Progress.ToolID] = c.tools[parent]
				}
			case "tool":
				o.Progress.ToolID = "foreign"
			case "name":
				o.Progress.ToolName = "Bash"
			case "input":
				o.InputID = domain.NewID()
			case "parent":
				v := "parent"
				o.Progress.ParentToolID = &v
			case "task":
				v := "task"
				o.Progress.TaskID = &v
			case "subagent":
				v := "agent"
				o.Progress.SubagentType = &v
			case "retry":
				o.Progress.Retry = &claude.NativeRetryObservation{}
			case "negative":
				elapsed = "-1"
			case "mixed":
				o.Progress.Thinking = &claude.NativeThinkingEstimate{}
			case "finished":
				if _, err := c.PublishObservation(context.Background(), result); err != nil {
					t.Fatal(err)
				}
			case "duplicate-summary", "foreign-summary":
				summary := "Original summary"
				ids := []string{"tool_original_one", "tool_original_one"}
				if scenario == "foreign-summary" {
					ids[1] = "foreign"
				}
				o.Progress = &claude.NativeProgressObservation{Kind: claude.ToolSummaryObserved, Summary: &summary, PrecedingTools: ids}
			}
			before := len(rpc.events)
			if handled, err := c.PublishToolProgressObservation(context.Background(), o); !handled || err == nil || len(rpc.events) != before {
				t.Fatal("invalid progress was published")
			}
		})
	}
}

func TestClaudeToolHeartbeatReplaysOriginalSeparateReferences(t *testing.T) {
	c, rpc, result := claudeToolFixture(t, "")
	parent, beat, elapsed := "tool_original_one", true, claude.NativeSeconds("30")
	o := result
	o.NativeID = string(domain.NewID())
	o.Kind, o.Content, o.Progress = claude.ProgressObserved, nil, &claude.NativeProgressObservation{Kind: claude.ToolProgressObserved, ToolID: parent + "-heartbeat-0", ToolName: "Read", ParentToolID: &parent, Elapsed: &elapsed, Heartbeat: &beat}
	rpc.lose = true
	if handled, err := c.PublishToolProgressObservation(context.Background(), o); !handled || err == nil {
		t.Fatal("lost heartbeat receipt accepted")
	}
	last := len(rpc.events) - 1
	rpc.lose = false
	if err := c.binding.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rpc.events[last], rpc.events[last+1]) || rpc.requests[last] != rpc.requests[last+1] {
		t.Fatal("heartbeat replay changed")
	}
	var e domain.ExecutionEvent
	if domain.Decode(rpc.events[last], &e) != nil {
		t.Fatal("invalid published event")
	}
	p := e.ClaudeProgress.Observation.Tool
	if p.Validate() != nil || p.Tool.NativeID != parent || p.ParentToolID == nil || *p.ParentToolID != parent || p.NativeToolID != o.Progress.ToolID || c.toolsComplete() {
		t.Fatal("heartbeat lost original identities or completed tool")
	}
}
