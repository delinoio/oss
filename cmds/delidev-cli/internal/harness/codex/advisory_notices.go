// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// The timestamp is private observation identity, never a review or receipt ID.
type strictReviewNoticeKey struct {
	Turn        domain.ID
	StartedAtMS int64
}

const StrictReviewReplayChecked MetadataKind = "strict-review-replay-checked"

func (c *Client) observeAdvisoryNoticeLocked(native nativewire.Event) (Event, error) {
	if len(native.Params) > nativewire.MaxFrame {
		return Event{}, incompatible()
	}
	if native.Kind != nativewire.Notification || c.execution == nil || c.execution.thread.ID != c.thread {
		return privateNative(native), nil
	}
	switch native.Method {
	case "guardianWarning":
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
	case "deprecationNotice":
		var fields map[string]json.RawMessage
		var p struct {
			Summary *string `json:"summary"`
			Details *string `json:"details"`
		}
		if domain.Decode(native.Params, &fields) != nil || len(fields) > 2 || domain.Decode(native.Params, &p) != nil || p.Summary == nil || domain.Text(*p.Summary, "private deprecation summary", nativewire.MaxFrame, true) != nil || p.Details != nil && domain.Text(*p.Details, "private deprecation details", nativewire.MaxFrame, false) != nil {
			return Event{}, incompatible()
		}
		return Event{Kind: NoticeEvent, ThreadID: c.thread, Notice: domain.NativeConfigWarning, Correlated: true}, nil
	case "strictReviewRequired":
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
		if c.execution.settings.ApprovalsReviewer != "auto_review" || c.execution.settings.ApprovalPolicy != ApprovalOnRequest {
			return Event{}, incompatible()
		}
		turn, known := c.execution.turns[p.TurnID]
		if !known {
			return Event{}, incompatible()
		}
		key := strictReviewNoticeKey{Turn: p.TurnID, StartedAtMS: *p.StartedAtMS}
		if c.execution.strictReviewNotices[key] {
			event := c.metadata(StrictReviewReplayChecked)
			event.TurnID = p.TurnID
			return event, nil
		}
		if turn.Turn.Status.terminal() || p.TurnID != c.execution.active || len(c.execution.strictReviewNotices) >= maxTrackedTurns {
			return Event{}, incompatible()
		}
		if c.execution.strictReviewNotices == nil {
			c.execution.strictReviewNotices = map[strictReviewNoticeKey]bool{}
		}
		c.execution.strictReviewNotices[key] = true
		// Generic bounded advisory progress deliberately invents no review identity,
		// decision or terminal receipt. The native auto-review lifecycle stays separate.
		return Event{Kind: NoticeEvent, ThreadID: c.thread, TurnID: p.TurnID, Notice: domain.NativeWarning, Correlated: true}, nil
	default:
		return privateNative(native), nil
	}
}
