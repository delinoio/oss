// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"reflect"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

type openCodeContextMessage struct {
	role      opencode.MessageRole
	summary   bool
	finalized bool
	parts     map[string]opencode.PartKind
}

// Called under the composer gate with an original frozen adapter observation.
// Context users and summary text stay outside the canonical transcript. Exact
// new step-finish sources retain their own accounting identities once only.
func (c *OpenCodeEventPublisher) publishContext(ctx context.Context, o opencode.Observation) error {
	b := c.text.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	claims, err := b.readClaims()
	if err != nil || !b.validPublicationClaims(claims) || b.stage != openCodeAccepted || c.text.blocked {
		return publicationUncertain()
	}
	if c.contextMessages == nil {
		c.contextMessages = map[string]*openCodeContextMessage{}
	}
	if c.contextCompactions == nil {
		c.contextCompactions = domain.NativeCompactionState{}
	}
	if c.text.contextUsers == nil {
		c.text.contextUsers = map[string]bool{}
	}
	if o.Compaction != nil {
		if o.Compaction.Harness != domain.OpenCode {
			return publicationUncertain()
		}
		next, err := domain.ApplyNativeCompaction(c.contextCompactions, *o.Compaction)
		if err != nil {
			return err
		}
		update := domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.NativeCompactionProgress, Compaction: o.Compaction}}
		if err := b.publisher.Publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionProgressObserved, NativeThreadID: b.thread, NativeTurnID: b.turn, Progress: &update}); err != nil {
			return err
		}
		c.contextCompactions = next
	}
	if o.Message != nil {
		m := o.Message
		if domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(m.SessionID), m.SessionID != b.thread) ||
			m.ID == b.turn || c.text.messages[m.ID] != nil {
			return publicationUncertain()
		}
		previous := c.contextMessages[m.ID]
		if previous == nil {
			if len(c.contextMessages)+len(c.text.messages) >= maxOpenCodeTextMessages || o.MessageFinalized {
				return publicationUncertain()
			}
			previous = &openCodeContextMessage{role: m.Role, summary: m.Assistant != nil, parts: map[string]opencode.PartKind{}}
			c.contextMessages[m.ID] = previous
		}
		if previous.role != m.Role || previous.summary != (m.Assistant != nil) || previous.finalized && !o.MessageFinalized {
			return publicationUncertain()
		}
		if m.User != nil {
			c.text.contextUsers[m.ID] = true
			return nil
		}
		if m.Assistant == nil || m.Assistant.Summary == nil || !*m.Assistant.Summary || m.Assistant.Agent != "compaction" || m.Assistant.Mode != "compaction" || !c.text.contextUsers[m.Assistant.ParentID] {
			return publicationUncertain()
		}
		if o.MessageFinalized {
			if m.Assistant.Completed == nil || c.usage.steps[m.ID] != "" && m.Assistant.Error == nil {
				return publicationUncertain()
			}
			if err := c.publishContextUsage(ctx, m.ID, m.ID, domain.OpenCodeMessageUsage, m.Assistant.Cost.String(), m.Assistant.Usage); err != nil {
				return err
			}
			previous.finalized = true
		}
		return nil
	}
	if o.Part != nil {
		p := o.Part
		if domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(p.SessionID), p.SessionID != b.thread) {
			return publicationUncertain()
		}
		if p.Tool != nil && p.Tool.Timing != nil && p.Tool.Timing.Compacted != nil {
			return nil
		}
		owner := c.contextMessages[p.MessageID]
		if owner == nil {
			return publicationUncertain()
		}
		if old, exists := owner.parts[p.ID]; exists && old != p.Kind {
			return publicationUncertain()
		}
		owner.parts[p.ID] = p.Kind
		if p.Kind == opencode.StepStartPartKind {
			if !owner.summary || owner.finalized || c.usage.steps[p.MessageID] != "" && c.usage.steps[p.MessageID] != p.ID {
				return publicationUncertain()
			}
			c.usage.starts[p.ID] = p.MessageID
			c.usage.steps[p.MessageID] = p.ID
		}
		if p.Kind == opencode.StepFinishPartKind {
			if p.Step == nil || p.Step.Usage == nil || p.Step.Cost == nil || !owner.summary || owner.finalized || c.usage.steps[p.MessageID] == "" && c.usage.values[p.ID].NativeID == "" {
				return publicationUncertain()
			}
			if err := c.publishContextUsage(ctx, p.ID, p.MessageID, domain.OpenCodeStepUsage, p.Step.Cost.String(), *p.Step.Usage); err != nil {
				return err
			}
			delete(c.usage.steps, p.MessageID)
		}
	}
	return nil
}

func (c *OpenCodeEventPublisher) publishContextUsage(ctx context.Context, id, parent string, source domain.OpenCodeUsageSource, estimate string, counts opencode.NativeUsage) error {
	value := domain.OpenCodeUsageObservation{Source: source, NativeID: id, NativeParentID: parent, NativeEstimate: estimate, Counts: domain.OpenCodeTokenCounts{Input: strconv.FormatUint(counts.Input, 10), Output: strconv.FormatUint(counts.Output, 10), Reasoning: strconv.FormatUint(counts.Reasoning, 10), CacheRead: strconv.FormatUint(counts.CacheRead, 10), CacheWrite: strconv.FormatUint(counts.CacheWrite, 10)}}
	if counts.Total != nil {
		total := strconv.FormatUint(*counts.Total, 10)
		value.Counts.Total = &total
	}
	if value.Validate() != nil {
		return publicationUncertain()
	}
	if old, exists := c.usage.values[id]; exists {
		if !reflect.DeepEqual(old, value) {
			return publicationUncertain()
		}
		return nil
	}
	if source == domain.OpenCodeMessageUsage {
		if last, exists := c.usage.last[parent]; !exists || !reflect.DeepEqual(last.Counts, value.Counts) {
			return publicationUncertain()
		}
	}
	b := c.text.binding
	if err := b.publisher.Publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionOpenCodeUsageObserved, ObservationID: domain.NewID(), NativeThreadID: b.thread, NativeTurnID: b.turn, OpenCodeUsage: &value}); err != nil {
		return err
	}
	c.usage.values[id] = value
	if source == domain.OpenCodeStepUsage {
		c.usage.last[parent] = value
	}
	return nil
}
