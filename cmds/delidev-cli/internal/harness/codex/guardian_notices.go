// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// These original-process notices never create an approval or a review identity.
// Native diagnostics remain private; only existing generic notice kinds leave
// this decoder. Strict-review timestamps are transient correlation evidence.
func (c *Client) observeGuardianNoticeLocked(native nativewire.Event) (Event, error) {
	var fields map[string]json.RawMessage
	if domain.Decode(native.Params, &fields) != nil || fields == nil {
		return Event{}, incompatible()
	}
	var allowed []string
	switch native.Method {
	case "guardianWarning":
		allowed = []string{"threadId", "message"}
	case "deprecationNotice":
		allowed = []string{"summary", "details"}
	case "strictReviewRequired":
		allowed = []string{"threadId", "turnId", "startedAtMs"}
	default:
		return Event{}, incompatible()
	}
	for key := range fields {
		if !slices.Contains(allowed, key) {
			return Event{}, incompatible()
		}
	}
	if native.Method == "deprecationNotice" {
		var p struct {
			Summary *string `json:"summary"`
			Details *string `json:"details"`
		}
		if domain.Decode(native.Params, &p) != nil || p.Summary == nil || domain.Text(*p.Summary, "private deprecation summary", nativewire.MaxFrame, true) != nil || p.Details != nil && domain.Text(*p.Details, "private deprecation details", nativewire.MaxFrame, false) != nil {
			return Event{}, incompatible()
		}
		return Event{Kind: NoticeEvent, ThreadID: c.thread, Notice: domain.NativeConfigWarning, Correlated: true}, nil
	}
	if native.Method == "guardianWarning" {
		var p struct {
			ThreadID domain.ID `json:"threadId"`
			Message  *string   `json:"message"`
		}
		if domain.Decode(native.Params, &p) != nil || p.ThreadID.Validate() != nil || p.Message == nil || domain.Text(*p.Message, "private guardian warning", nativewire.MaxFrame, true) != nil {
			return Event{}, incompatible()
		}
		if p.ThreadID != c.thread {
			return privateNative(native), nil
		}
		return Event{Kind: NoticeEvent, ThreadID: c.thread, Notice: domain.NativeWarning, Correlated: true}, nil
	}
	var p struct {
		ThreadID    domain.ID `json:"threadId"`
		TurnID      domain.ID `json:"turnId"`
		StartedAtMS *int64    `json:"startedAtMs"`
	}
	if domain.Decode(native.Params, &p) != nil || p.ThreadID.Validate() != nil || p.TurnID.Validate() != nil || p.StartedAtMS == nil || *p.StartedAtMS < 0 {
		return Event{}, incompatible()
	}
	if p.ThreadID != c.thread {
		return privateNative(native), nil
	}
	turn, known := c.execution.turns[p.TurnID]
	if !known || c.execution.settings.ApprovalsReviewer != "auto_review" {
		return Event{}, incompatible()
	}
	if prior, exists := c.execution.strictReviewStarts[p.TurnID]; exists && prior > *p.StartedAtMS {
		return Event{}, incompatible()
	}
	if prior, exists := c.execution.strictReviewStarts[p.TurnID]; exists && prior == *p.StartedAtMS {
		event := c.metadata(StrictReviewReplayChecked)
		event.TurnID = p.TurnID
		return event, nil
	}
	if turn.Turn.Status.terminal() || p.TurnID != c.execution.active {
		return Event{}, incompatible()
	}
	// One bounded timestamp per already-owned turn; no synthetic review lifecycle
	// or completion receipt is introduced, and native auto-review remains separate.
	if c.execution.strictReviewStarts == nil {
		c.execution.strictReviewStarts = map[domain.ID]int64{}
	}
	c.execution.strictReviewStarts[p.TurnID] = *p.StartedAtMS
	return Event{Kind: NoticeEvent, ThreadID: c.thread, TurnID: p.TurnID, Notice: domain.NativeWarning, StrictReviewStartedAtMS: p.StartedAtMS, Correlated: true}, nil
}
