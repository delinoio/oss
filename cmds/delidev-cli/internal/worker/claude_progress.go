package worker

import (
	"context"
	"reflect"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

// This shares the original binding/content outbox and may run before input
// acceptance. No progress event can consume the native user replay or a claim.
func (b *ClaudeBindingPublisher) PublishProgressObservation(ctx context.Context, o claude.LifecycleObservation) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.verify(); err != nil {
		return false, err
	}
	if o.Kind != claude.ProgressObserved || o.Progress == nil || o.Progress.Kind != claude.SessionStatusObserved && o.Progress.Kind != claude.ThinkingTokensEstimated && o.Progress.Kind != claude.APIRetryObserved {
		return false, nil
	}
	accepted := b.stage == claudeInputAccepted
	n := o.Progress
	v := domain.ClaudeProgressObservation{NativeEventID: o.NativeID, InputAccepted: accepted}
	switch n.Kind {
	case claude.SessionStatusObserved:
		if !reflect.DeepEqual(*n, claude.NativeProgressObservation{Kind: n.Kind, Status: n.Status, Permission: n.Permission, CompactResult: n.CompactResult, CompactError: n.CompactError}) {
			return true, b.block()
		}
		v.Kind, v.Status = domain.ClaudeStatusProgress, &domain.ClaudeStatusObservation{}
		if n.CompactError != nil {
			value := *n.CompactError
			v.Status.CompactError = &value
		}
		if n.Status != nil {
			value := domain.ClaudeSessionStatus(*n.Status)
			v.Status.Status = &value
		}
		if n.Permission != nil {
			value := domain.ClaudePermissionMode(*n.Permission)
			v.Status.Permission = &value
		}
		if n.CompactResult != nil {
			value := domain.ClaudeCompactResult(*n.CompactResult)
			v.Status.CompactResult = &value
		}
	case claude.APIRetryObserved:
		if n.APIRetry == nil || !reflect.DeepEqual(*n, claude.NativeProgressObservation{Kind: n.Kind, APIRetry: n.APIRetry}) {
			return true, b.block()
		}
		r := n.APIRetry
		v.Kind, v.APIRetry = domain.ClaudeAPIRetryProgress, &domain.ClaudeAPIRetryObservation{
			NativeEventID: o.NativeID, Attempt: domain.ClaudeProgressCount(strconv.FormatUint(r.Attempt, 10)),
			MaxRetries: domain.ClaudeProgressCount(strconv.FormatUint(r.MaxRetries, 10)), DelayMS: domain.ClaudeProgressCount(strconv.FormatUint(r.DelayMS, 10)), Error: domain.ClaudeAPIProblem(r.Error),
		}
		if r.ErrorStatus != nil {
			value := *r.ErrorStatus
			v.APIRetry.ErrorStatus = &value
		}
	case claude.ThinkingTokensEstimated:
		if !accepted || n.Thinking == nil || !reflect.DeepEqual(*n, claude.NativeProgressObservation{Kind: n.Kind, Thinking: n.Thinking}) {
			return true, b.block()
		}
		v.Kind, v.Thinking = domain.ClaudeThinkingProgress, &domain.ClaudeThinkingObservation{Tokens: domain.ClaudeProgressCount(strconv.FormatUint(n.Thinking.Tokens, 10)), Delta: domain.ClaudeProgressCount(strconv.FormatUint(n.Thinking.Delta, 10))}
	}
	return b.publishProgressLocked(ctx, o, v)
}

// The caller holds the shared binding lock and has validated the selected
// native family. All progress uses one original sequence and durable outbox.
func (b *ClaudeBindingPublisher) publishProgressLocked(ctx context.Context, o claude.LifecycleObservation, v domain.ClaudeProgressObservation) (bool, error) {
	accepted := b.stage == claudeInputAccepted
	compaction := v.Kind == domain.ClaudeCompactionProgress || v.Kind == domain.ClaudeCompactionSummaryProgress
	inputMatches := o.InputID == b.journal.InputID && o.Accepted == accepted
	if compaction {
		inputMatches = o.InputID == "" && !o.Accepted
	}
	if b.stage != claudeSessionBound && !accepted ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(o.SessionID), o.SessionID != b.journal.SessionID) ||
		!inputMatches || o.TurnID != b.turn || o.NativeID == b.turn || o.NativeID == string(b.journal.InputID) || b.progressSeen[o.NativeID] || len(b.progressSeen) >= 65536 {
		return true, b.block()
	}
	u := &domain.ExecutionClaudeProgress{ID: domain.NewID(), Observation: v}
	if u.Validate() != nil {
		return true, b.block()
	}
	if compaction {
		next, err := domain.ApplyClaudeCompaction(b.compaction, *u)
		if err != nil {
			return true, b.block()
		}
		b.compactionNext = next
	}
	if b.progressSeen == nil {
		b.progressSeen = map[string]bool{}
	}
	b.progressSeen[o.NativeID] = true
	b.progressResume, b.stage, b.sequence = b.stage, claudeProgressPending, b.sequence+1
	if err := b.publish(ctx, domain.ExecutionEvent{Sequence: b.sequence, Kind: domain.ExecutionClaudeProgressObserved, NativeThreadID: string(b.journal.SessionID), NativeTurnID: b.turn, ClaudeProgress: u}); err != nil {
		return true, err
	}
	b.stage = b.progressResume
	b.commitCompactionProgress()
	if logger := b.publisher.config.Logger; logger != nil {
		logger.Info("claude_progress_observed", "job_id", b.journal.JobID, "kind", v.Kind, "input_accepted", accepted)
	}
	return true, nil
}
