package worker

import (
	"context"
	"reflect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func (c *ClaudeContentPublisher) PublishToolProgressObservation(ctx context.Context, o claude.LifecycleObservation) (bool, error) {
	if o.Kind != claude.ProgressObserved || o.Progress == nil || o.Progress.Kind != claude.ToolProgressObserved && o.Progress.Kind != claude.ToolSummaryObserved {
		return false, nil
	}
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := c.verify(); err != nil {
		return true, err
	}
	n := o.Progress
	v := domain.ClaudeProgressObservation{NativeEventID: o.NativeID, InputAccepted: true}
	switch n.Kind {
	case claude.ToolProgressObserved:
		// Task/child progress needs its own ownership adapter. Explicit native
		// root parent null and an absent heartbeat retain separate meanings.
		if n.Elapsed == nil || !reflect.DeepEqual(*n, claude.NativeProgressObservation{Kind: n.Kind, ToolID: n.ToolID, ToolName: n.ToolName, Elapsed: n.Elapsed, Heartbeat: n.Heartbeat}) {
			return true, b.block()
		}
		tool, exists := c.tools[n.ToolID]
		if !exists || tool.state != domain.MessageStreaming || tool.content == nil || tool.content.Proposal == nil || tool.reference.Name != n.ToolName {
			return true, b.block()
		}
		v.Kind, v.Tool = domain.ClaudeToolProgress, &domain.ClaudeToolProgressObservation{Tool: tool.reference, ElapsedSeconds: string(*n.Elapsed)}
		if n.Heartbeat != nil {
			value := *n.Heartbeat
			v.Tool.Heartbeat = &value
		}
	case claude.ToolSummaryObserved:
		if n.Summary == nil || !reflect.DeepEqual(*n, claude.NativeProgressObservation{Kind: n.Kind, Summary: n.Summary, PrecedingTools: n.PrecedingTools}) {
			return true, b.block()
		}
		v.Kind, v.ToolSummary = domain.ClaudeToolSummaryProgress, &domain.ClaudeToolSummaryObservation{Summary: *n.Summary, Tools: make([]domain.ClaudeToolReference, 0, len(n.PrecedingTools))}
		if len(n.PrecedingTools) == 0 || len(n.PrecedingTools) > 128 {
			return true, b.block()
		}
		for _, id := range n.PrecedingTools {
			tool, exists := c.tools[id]
			if !exists || tool.reference.Validate() != nil {
				return true, b.block()
			}
			v.ToolSummary.Tools = append(v.ToolSummary.Tools, tool.reference)
		}
	}
	return b.publishProgressLocked(ctx, o, v)
}
