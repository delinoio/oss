package worker

import (
	"context"
	"reflect"
	"slices"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func (b *ClaudeBindingPublisher) PublishCompactionObservation(ctx context.Context, o claude.LifecycleObservation) (bool, error) {
	if o.Kind != claude.CompactionObserved && o.Kind != claude.CompactionSummaryObserved {
		return false, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.verify(); err != nil {
		return true, err
	}
	if !reflect.DeepEqual(o, claude.LifecycleObservation{Kind: o.Kind, SessionID: o.SessionID, TurnID: o.TurnID, NativeID: o.NativeID, Native: o.Native, Compaction: o.Compaction, Summary: o.Summary}) {
		return true, b.block()
	}
	v := domain.ClaudeProgressObservation{NativeEventID: o.NativeID, InputAccepted: b.stage == claudeInputAccepted}
	switch o.Kind {
	case claude.CompactionObserved:
		if o.Compaction == nil || o.Summary != nil {
			return true, b.block()
		}
		n := o.Compaction
		v.Kind = domain.ClaudeCompactionProgress
		v.Compaction = &domain.ClaudeCompactionBoundary{Trigger: domain.ClaudeCompactionTrigger(n.Trigger), Before: domain.ClaudeProgressCount(strconv.FormatUint(n.Before, 10)), After: claudeCompactionCount(n.After), DurationMS: claudeCompactionCount(n.DurationMS), CumulativeDropped: claudeCompactionCount(n.CumulativeDropped), LogicalParent: cloneClaudeTaskField(n.LogicalParent)}
		if n.Segment != nil {
			v.Compaction.Segment = &domain.ClaudePreservedSegment{Head: n.Segment.Head, Anchor: n.Segment.Anchor, Tail: n.Segment.Tail}
		}
		if n.Messages != nil {
			v.Compaction.Messages = &domain.ClaudePreservedMessages{Anchor: n.Messages.Anchor, IDs: slices.Clone(n.Messages.IDs), AllIDs: slices.Clone(n.Messages.AllIDs)}
		}
	case claude.CompactionSummaryObserved:
		if o.Summary == nil || o.Compaction != nil || b.compaction == nil || b.compaction.Pending == nil {
			return true, b.block()
		}
		n := o.Summary
		v.Kind = domain.ClaudeCompactionSummaryProgress
		v.CompactionSummary = &domain.ClaudeCompactionSummary{BoundaryID: n.BoundaryID, BoundaryMessageID: b.compaction.Pending.BoundaryMessageID, Text: cloneClaudeTaskField(n.Text)}
		if n.Blocks != nil {
			v.CompactionSummary.Blocks = make([]domain.ClaudeTextBlock, 0, len(n.Blocks))
			for _, block := range n.Blocks {
				display, err := claudeDisplayBlock(&block)
				if err != nil || display.Kind != domain.ClaudeText {
					return true, b.block()
				}
				v.CompactionSummary.Blocks = append(v.CompactionSummary.Blocks, display)
			}
		}
	}
	return b.publishProgressLocked(ctx, o, v)
}

func claudeCompactionCount(n *uint64) *domain.ClaudeProgressCount {
	if n == nil {
		return nil
	}
	value := domain.ClaudeProgressCount(strconv.FormatUint(*n, 10))
	return &value
}

func (b *ClaudeBindingPublisher) commitCompactionProgress() {
	if b.compactionNext != nil {
		b.compaction = b.compactionNext
		b.compactionNext = nil
	}
}
