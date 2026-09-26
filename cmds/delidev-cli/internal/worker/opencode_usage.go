package worker

import (
	"context"
	"reflect"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

// This mapper consumes the same original observations after the transcript
// mapper. It publishes overlapping native step/final-message observations, not
// charges, inferred totals, exact response coverage or cumulative differences.
type OpenCodeUsagePublisher struct {
	transcript *OpenCodeTextPublisher
	seen       map[string]bool
	steps      map[string]string
	starts     map[string]string
	last       map[string]domain.OpenCodeUsageObservation
	values     map[string]domain.OpenCodeUsageObservation
	blocked    bool
}

func OpenOpenCodeUsagePublisher(transcript *OpenCodeTextPublisher) (*OpenCodeUsagePublisher, error) {
	if transcript == nil || transcript.binding == nil {
		return nil, publicationUncertain()
	}
	b := transcript.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if transcript.blocked || transcript.usageAttached || b.stage != openCodeAccepted {
		return nil, publicationUncertain()
	}
	if _, err := b.readClaims(); err != nil {
		return nil, err
	}
	if sequence, err := b.publisher.acknowledgedSequence(); err != nil || sequence != 2 {
		return nil, publicationUncertain()
	}
	transcript.usageAttached = true
	return &OpenCodeUsagePublisher{transcript: transcript, seen: map[string]bool{}, steps: map[string]string{}, starts: map[string]string{}, last: map[string]domain.OpenCodeUsageObservation{}, values: map[string]domain.OpenCodeUsageObservation{}}, nil
}

func (c *OpenCodeUsagePublisher) PublishObservation(ctx context.Context, o opencode.Observation) (handled bool, returned error) {
	if c == nil || c.transcript == nil {
		return false, publicationUncertain()
	}
	t := c.transcript
	b := t.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	defer func() {
		if returned != nil {
			c.blocked = true
			t.blocked = true
			if b.publisher.config.Logger != nil {
				b.publisher.config.Logger.Warn("opencode_usage_publication_uncertain", "job_id", b.reference.JobID, "execution_id", b.reference.ExecutionID, "code", domain.SafeError(returned).Code)
			}
		}
	}()
	if c.blocked || t.blocked || b.stage != openCodeAccepted {
		return false, publicationUncertain()
	}
	if _, err := b.readClaims(); err != nil {
		return false, err
	}
	if domain.NativeIdentity(o.EventID).Validate(domain.OpenCode, domain.NativeEventIdentity) != nil || c.seen[o.EventID] || len(c.seen) >= 65536 {
		return false, publicationUncertain()
	}
	defer func() {
		if returned == nil && handled {
			c.seen[o.EventID] = true
		}
	}()
	var value domain.OpenCodeUsageObservation
	var counts opencode.NativeUsage
	switch {
	case o.Kind == opencode.MessagePartUpdatedEvent && o.Part != nil && o.Part.Kind == opencode.StepStartPartKind:
		p := o.Part
		owner := t.messages[p.MessageID]
		if p.SessionID != b.thread || owner == nil || owner.role != domain.AssistantMessage || p.Step == nil || domain.NativeIdentity(p.ID).Validate(domain.OpenCode, domain.NativePartIdentity) != nil {
			return true, publicationUncertain()
		}
		if revision := t.revisions[p.ID]; revision != nil && (revision.parent != p.MessageID || revision.kind != p.Kind) {
			return true, publicationUncertain()
		}
		if parent := c.starts[p.ID]; parent != "" {
			if parent != p.MessageID {
				return true, publicationUncertain()
			}
			return true, nil
		}
		if owner.finalized || c.steps[p.MessageID] != "" || len(c.starts) >= maxOpenCodeTextParts || t.parts[p.ID] != nil || t.tools[p.ID] != nil || c.values[p.ID].NativeID != "" {
			return true, publicationUncertain()
		}
		c.steps[p.MessageID] = p.ID
		c.starts[p.ID] = p.MessageID
		return true, nil
	case o.Kind == opencode.MessagePartUpdatedEvent && o.Part != nil && o.Part.Kind == opencode.StepFinishPartKind:
		p := o.Part
		owner := t.messages[p.MessageID]
		if p.SessionID != b.thread || owner == nil || owner.role != domain.AssistantMessage || p.Step == nil || p.Step.Usage == nil || p.Step.Cost == nil || p.Step.Reason == nil {
			return true, publicationUncertain()
		}
		if revision := t.revisions[p.ID]; revision != nil && (revision.parent != p.MessageID || revision.kind != p.Kind) {
			return true, publicationUncertain()
		}
		value = domain.OpenCodeUsageObservation{Source: domain.OpenCodeStepUsage, NativeID: p.ID, NativeParentID: p.MessageID, NativeEstimate: string(*p.Step.Cost)}
		counts = *p.Step.Usage
	case o.Kind == opencode.MessageUpdatedEvent && o.MessageFinalized:
		m := o.Message
		if m == nil || !t.seen[o.EventID] || m.SessionID != b.thread || m.Assistant == nil || m.User != nil || m.Assistant.ParentID != b.turn || m.Assistant.Completed == nil {
			return true, publicationUncertain()
		}
		owner := t.messages[m.ID]
		if owner == nil || owner.role != domain.AssistantMessage || !owner.finalized || c.steps[m.ID] != "" && m.Assistant.Error == nil {
			return true, publicationUncertain()
		}
		value = domain.OpenCodeUsageObservation{Source: domain.OpenCodeMessageUsage, NativeID: m.ID, NativeParentID: m.ID, NativeEstimate: string(m.Assistant.Cost)}
		counts = m.Assistant.Usage
	default:
		return false, nil
	}
	value.Counts = domain.OpenCodeTokenCounts{Input: strconv.FormatUint(counts.Input, 10), Output: strconv.FormatUint(counts.Output, 10), Reasoning: strconv.FormatUint(counts.Reasoning, 10), CacheRead: strconv.FormatUint(counts.CacheRead, 10), CacheWrite: strconv.FormatUint(counts.CacheWrite, 10)}
	if counts.Total != nil {
		text := strconv.FormatUint(*counts.Total, 10)
		value.Counts.Total = &text
	}
	if value.Validate() != nil {
		return true, publicationUncertain()
	}
	if old, exists := c.values[value.NativeID]; exists {
		if !reflect.DeepEqual(old, value) {
			return true, publicationUncertain()
		}
		return true, nil
	}
	if len(c.values) >= maxOpenCodeTextParts+maxOpenCodeTextMessages {
		return true, publicationUncertain()
	}
	if value.Source == domain.OpenCodeStepUsage && (c.steps[value.NativeParentID] == "" || t.messages[value.NativeParentID].finalized || c.starts[value.NativeID] != "" || t.parts[value.NativeID] != nil || t.tools[value.NativeID] != nil) {
		return true, publicationUncertain()
	}
	if value.Source == domain.OpenCodeMessageUsage && o.Message.Assistant.Error == nil {
		last, ok := c.last[value.NativeParentID]
		if !ok || !reflect.DeepEqual(last.Counts, value.Counts) {
			return true, publicationUncertain()
		}
	}
	if err := b.publisher.Publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionOpenCodeUsageObserved, ObservationID: domain.NewID(), NativeThreadID: b.thread, NativeTurnID: b.turn, OpenCodeUsage: &value}); err != nil {
		return true, err
	}
	c.values[value.NativeID] = value
	if value.Source == domain.OpenCodeMessageUsage && o.Message.Assistant.Error != nil {
		delete(c.steps, value.NativeParentID)
	}
	if value.Source == domain.OpenCodeStepUsage {
		delete(c.steps, value.NativeParentID)
		c.last[value.NativeParentID] = value
	}
	return true, nil
}
