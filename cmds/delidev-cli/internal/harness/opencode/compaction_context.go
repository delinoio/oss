// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// These private records join original native messages, parts and the independently
// emitted completion event. They contain no summary text or execution authority.
type NativeContextRecord struct {
	ActionID            domain.ID `json:"action_id,omitempty"`
	InputRequestID      domain.ID `json:"input_request_id"`
	SourceInputID       string    `json:"source_input_id"`
	UserID              string    `json:"user_id"`
	PartID              string    `json:"part_id"`
	SummaryID           string    `json:"summary_id"`
	CompletedEventID    string    `json:"completed_event_id"`
	Auto                bool      `json:"auto"`
	OverflowEventID     string    `json:"overflow_event_id,omitempty"`
	OverflowAssistantID string    `json:"overflow_assistant_id,omitempty"`
	Overflow            bool      `json:"overflow,omitempty"`
	ContinueID          string    `json:"continue_id,omitempty"`
	TailStartID         string    `json:"tail_start_id,omitempty"`
}

type NativePrunedPart struct {
	ID             string `json:"id"`
	MessageID      string `json:"message_id"`
	OriginalSHA256 string `json:"original_sha256"`
	SHA256         string `json:"sha256"`
	Compacted      uint64 `json:"compacted"`
}

func (o *inputObserver) activeContext() *NativeContextRecord {
	if len(o.contextRecords) == 0 {
		return nil
	}
	return &o.contextRecords[len(o.contextRecords)-1]
}

func (o *inputObserver) contextClosed() bool {
	if o.contextPending != "" {
		return false
	}
	for _, c := range o.contextRecords {
		if c.CompletedEventID == "" {
			return false
		}
	}
	return true
}

func (o *inputObserver) contextUserAllowed(v NativeMessage, old *observedMessage) bool {
	if v.ID == o.input.receipt.MessageID {
		return true
	}
	if o.contextUsers == nil {
		o.contextUsers = map[string]bool{}
	}
	if old != nil {
		return o.contextUsers[v.ID] || o.contextPending == v.ID
	}
	if !o.progress.UserSeen || !o.progress.InputPartSeen || o.progress.SettledObserved || o.contextPending != "" || o.stop != nil {
		return false
	}
	prior := o.messages[o.progress.AssistantID]
	c := o.activeContext()
	if prior != nil && (!prior.finalized || prior.value.Assistant.Error != nil) {
		return false
	}
	// A compaction can run before a model response when the restored context
	// already exceeds its native limit. The original accepted input still owns it.
	if c != nil && c.CompletedEventID == "" && (prior == nil || prior.value.ID != c.SummaryID || prior.value.Assistant.Summary == nil || !*prior.value.Assistant.Summary) {
		return false
	}
	o.contextPending = v.ID
	return true
}

func (o *inputObserver) contextAssistantAllowed(v NativeMessage) bool {
	a := v.Assistant
	if a == nil {
		return false
	}
	settings := o.creation.settings
	if a.Agent == "compaction" && a.Mode == "compaction" && a.Summary != nil && *a.Summary {
		c := o.activeContext()
		if c == nil || c.CompletedEventID != "" || a.ParentID != c.UserID || o.contextPending != "" || c.SummaryID != "" && c.SummaryID != v.ID {
			return false
		}
		c.SummaryID = v.ID
		return true
	}
	return a.Summary == nil && a.Agent == string(settings.Agent) && a.Mode == string(settings.Agent) && a.ParentID == o.contextParent && o.contextPending == "" && o.contextClosed()
}

func (o *inputObserver) contextAssistantSuccessor(v, prior NativeMessage) bool {
	if v.Assistant == nil || prior.Assistant == nil {
		return false
	}
	c := o.activeContext()
	return c != nil && (v.Assistant.ParentID == c.UserID && prior.ID != c.SummaryID || prior.ID == c.SummaryID && c.CompletedEventID != "" && v.Assistant.ParentID == o.contextParent)
}

func (o *inputObserver) contextUserPart(v NativePart, old *observedPart) error {
	c := o.activeContext()
	if v.Kind == CompactionPartKind {
		p := v.Compaction
		if p == nil || !p.Auto && o.contextManual == "" || p.Auto && o.contextManual != "" {
			return observerProblem()
		}
		if old == nil {
			if o.contextPending != v.MessageID || len(o.messages[v.MessageID].parts) != 0 || c != nil && c.CompletedEventID == "" || len(o.contextRecords) >= 128 {
				return observerProblem()
			}
			c = &NativeContextRecord{ActionID: o.contextManual, InputRequestID: o.input.receipt.RequestID, SourceInputID: o.input.receipt.MessageID, UserID: v.MessageID, PartID: v.ID, Auto: p.Auto}
			if p.Overflow != nil {
				c.Overflow = *p.Overflow
				if c.Overflow {
					c.OverflowAssistantID = o.progress.AssistantID
					c.OverflowEventID = o.contextOverflow[c.OverflowAssistantID]
					if c.OverflowEventID == "" {
						return observerProblem()
					}
				}
			}
			o.contextRecords = append(o.contextRecords, *c)
			c = o.activeContext()
			o.contextUsers[v.MessageID] = true
			o.contextPending = ""
		} else if c == nil || c.UserID != v.MessageID || c.PartID != v.ID || c.CompletedEventID != "" || old.value.Compaction == nil || old.value.Compaction.Auto != p.Auto || !equalOptionalBool(old.value.Compaction.Overflow, p.Overflow) || old.value.Compaction.TailStart != nil {
			return observerProblem()
		}
		if p.TailStart != nil {
			if c.SummaryID == "" || !o.messages[c.SummaryID].finalized || !o.contextTailKnown(*p.TailStart) {
				return observerProblem()
			}
			c.TailStartID = *p.TailStart
		}
		return nil
	}
	if c == nil || !c.Auto || c.SummaryID == "" || o.contextPending != v.MessageID || v.Kind != TextPartKind || old != nil || len(o.messages[v.MessageID].parts) != 0 || v.Text == nil {
		return observerProblem()
	}
	summary := o.messages[c.SummaryID]
	if summary == nil || !summary.finalized || summary.value.Assistant.Error != nil || summary.value.Assistant.Finish == nil || *summary.value.Assistant.Finish != FinishStop {
		return observerProblem()
	}
	text := v.Text
	if c.Overflow && text.Synthetic == nil && text.Timing == nil && text.Metadata == nil && text.Ignored == nil {
		original := o.parts[o.input.receipt.PartID]
		if original == nil || original.value.Text == nil || text.Text != original.value.Text.Text {
			return observerProblem()
		}
	} else {
		fields, err := shape(text.Metadata, []string{"compaction_continue"}, nil)
		if err != nil || string(fields["compaction_continue"]) != "true" || text.Synthetic == nil || !*text.Synthetic || text.Ignored != nil || text.Timing == nil || text.Timing.End == nil {
			return observerProblem()
		}
	}
	o.contextUsers[v.MessageID] = true
	c.ContinueID = v.MessageID
	o.contextParent = v.MessageID
	o.contextPending = ""
	return nil
}

func equalOptionalBool(a, b *bool) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func (o *inputObserver) contextTailKnown(id string) bool {
	if o.messages[id] != nil {
		return true
	}
	// Restoration records are separately inspected before observation. They do
	// not permit adopting a new message or changing a retained tail identity.
	for _, m := range o.contextBaseInventory {
		if m.ID == id {
			return true
		}
	}
	return false
}

func (o *inputObserver) compacted(event string) (*domain.NativeCompactionObservation, error) {
	c := o.activeContext()
	if c == nil || c.CompletedEventID != "" || c.SummaryID == "" || o.contextPending != "" {
		return nil, observerProblem()
	}
	m := o.messages[c.SummaryID]
	if m == nil || !m.finalized || m.value.Assistant.Error != nil || m.value.Assistant.Finish == nil || *m.value.Assistant.Finish != FinishStop || !o.messageClosed(m.value, m) {
		return nil, observerProblem()
	}
	c.CompletedEventID = event
	if !c.Auto {
		return nil, nil
	}
	return &domain.NativeCompactionObservation{Harness: domain.OpenCode, Trigger: domain.NativeAutomaticCompaction, Stage: domain.NativeCompactionCompleted, NativeItemID: c.PartID}, nil
}

func (o *inputObserver) contextObservation(r *inputObservation) error {
	if r.Message != nil {
		m := r.Message
		r.ContextOverflow = m.Assistant != nil && m.Assistant.Completed != nil && m.Assistant.Finish == nil && m.Assistant.Error == nil && o.contextOverflow[m.ID] != ""
		r.ContextOnly = m.ID != o.input.receipt.MessageID && m.User != nil || m.Assistant != nil && m.Assistant.Summary != nil
	} else if r.Part != nil {
		p := r.Part
		m := o.messages[p.MessageID]
		r.ContextOnly = m != nil && (m.value.User != nil && p.MessageID != o.input.receipt.MessageID || m.value.Assistant != nil && m.value.Assistant.Summary != nil) || p.Tool != nil && p.Tool.Timing != nil && p.Tool.Timing.Compacted != nil
		if p.Compaction != nil && !r.Repeated && p.Compaction.TailStart == nil && p.Compaction.Auto {
			r.Compaction = &domain.NativeCompactionObservation{Harness: domain.OpenCode, Trigger: domain.NativeAutomaticCompaction, Stage: domain.NativeCompactionStarted, NativeItemID: p.ID}
		}
	} else if r.Delta != nil {
		m := o.messages[r.Delta.MessageID]
		r.ContextOnly = m != nil && m.value.Assistant != nil && m.value.Assistant.Summary != nil
	}
	return nil
}

func unprunedPart(raw []byte) []byte {
	fields, err := object(raw)
	if err != nil {
		return nil
	}
	state, err := object(fields["state"])
	if err != nil {
		return nil
	}
	timing, err := object(state["time"])
	if err != nil {
		return nil
	}
	delete(timing, "compacted")
	state["time"], _ = json.Marshal(timing)
	fields["state"], _ = json.Marshal(state)
	result, _ := json.Marshal(fields)
	return canonicalNative(result)
}

func (o *inputObserver) compactedToolPart(v NativePart, raw []byte) (*NativePart, bool, error) {
	if v.Tool.State != ToolCompleted || v.Tool.Timing.End == nil || *v.Tool.Timing.Compacted < *v.Tool.Timing.End {
		return nil, false, observerProblem()
	}
	old := o.parts[v.ID]
	if old != nil && bytes.Equal(old.raw, canonicalNative(raw)) {
		copy, _ := decodeNativePart(raw)
		return &copy, true, nil
	}
	original := ""
	if old != nil {
		if old.value.MessageID != v.MessageID || old.value.Tool == nil || old.value.Tool.State != ToolCompleted || old.value.Tool.Timing.Compacted != nil || !bytes.Equal(old.raw, unprunedPart(raw)) {
			return nil, false, observerProblem()
		}
		original = mutationDigest(old.raw)
	} else {
		for _, m := range o.contextBaseInventory {
			if m.ID == v.MessageID {
				for _, p := range m.Parts {
					if p.ID == v.ID && p.Kind == ToolPartKind {
						original = p.Digest
					}
				}
			}
		}
		if original == "" || mutationDigest(unprunedPart(raw)) != original {
			return nil, false, observerProblem()
		}
	}
	for _, p := range o.contextPruned {
		if p.ID == v.ID {
			return nil, false, observerProblem()
		}
	}
	o.contextPruned = append(o.contextPruned, NativePrunedPart{ID: v.ID, MessageID: v.MessageID, OriginalSHA256: original, SHA256: mutationDigest(canonicalNative(raw)), Compacted: *v.Tool.Timing.Compacted})
	if old != nil {
		o.parts[v.ID] = &observedPart{raw: canonicalNative(raw), value: v}
	}
	copy, _ := decodeNativePart(raw)
	return &copy, false, nil
}

func cloneContextRecords(v []NativeContextRecord) []NativeContextRecord { return slices.Clone(v) }

// The pinned summary task may update the native continuation user's ancillary
// summary after idle. This independently read metadata grants no input receipt,
// compaction lifecycle, assistant settlement or canonical conversation content.
func (o *inputObserver) readContextUserSummary(old *observedMessage, raw []byte) bool {
	if old == nil || old.value.User == nil || !o.contextUsers[old.value.ID] || !bytes.Equal(messageBase(raw, UserMessageRole), old.base) {
		return false
	}
	next, err := decodeNativeMessage(raw)
	if err != nil || next.ID != old.value.ID || next.SessionID != o.input.receipt.SessionID || next.User == nil || next.User.Summary == nil {
		return false
	}
	old.raw, old.value = canonicalNative(raw), next
	return true
}
