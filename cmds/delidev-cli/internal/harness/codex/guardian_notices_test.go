// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestGuardianNoticesKeepPrivateTextAndNativeReviewAuthority(t *testing.T) {
	c, turn := observationClient()
	c.execution.settings.ApprovalsReviewer = "auto_review"
	before := c.execution.settings
	var logs bytes.Buffer
	c.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	for _, fixture := range []struct {
		method string
		params map[string]any
		notice domain.NativeNotice
	}{
		{"guardianWarning", map[string]any{"threadId": c.thread, "message": "private-sentinel"}, domain.NativeWarning},
		{"deprecationNotice", map[string]any{"summary": "private-sentinel", "details": "private-detail-sentinel"}, domain.NativeConfigWarning},
		{"deprecationNotice", map[string]any{"summary": "private-sentinel", "details": nil}, domain.NativeConfigWarning},
		{"strictReviewRequired", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 0}, domain.NativeWarning},
	} {
		event, err := observeFixture(c, fixture.method, fixture.params)
		raw, _ := json.Marshal(event)
		if err != nil || event.Kind != NoticeEvent || event.Notice != fixture.notice || !event.Correlated || strings.Contains(string(raw), "sentinel") || strings.Contains(string(raw), "StrictReviewStarted") {
			t.Fatalf("notice leaked or failed: %s %v", fixture.method, err)
		}
	}
	if !reflect.DeepEqual(before, c.execution.settings) || len(c.execution.autoReviews) != 0 || len(c.execution.interactions.arrivals) != 0 {
		t.Fatal("notice acquired review or permission authority")
	}
	if strings.Contains(logs.String(), "sentinel") {
		t.Fatal("private diagnostic logged")
	}
	event, err := observeFixture(c, "strictReviewRequired", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 0})
	if err != nil || event.Metadata != StrictReviewReplayChecked || event.TurnID != turn {
		t.Fatal("exact strict replay duplicated notice", err)
	}
	tracked := c.execution.turns[turn]
	tracked.Turn.Status = TurnCompleted
	c.execution.turns[turn] = tracked
	event, err = observeFixture(c, "strictReviewRequired", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 0})
	if err != nil || event.Metadata != StrictReviewReplayChecked {
		t.Fatal("terminal replay changed original result", err)
	}
	if _, err = observeFixture(c, "strictReviewRequired", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 1}); err == nil {
		t.Fatal("new terminal review accepted")
	}
}

func TestGuardianNoticesRejectMalformedAndForeignOwnership(t *testing.T) {
	c, turn := observationClient()
	c.execution.settings.ApprovalsReviewer = "auto_review"
	for _, fixture := range []struct {
		method string
		params map[string]any
	}{
		{"guardianWarning", map[string]any{"message": "private"}},
		{"guardianWarning", map[string]any{"threadId": c.thread, "message": nil}},
		{"guardianWarning", map[string]any{"threadId": c.thread, "message": strings.Repeat("x", nativewire.MaxFrame+1)}},
		{"guardianWarning", map[string]any{"threadId": c.thread, "message": "private", "extra": true}},
		{"guardianWarning", map[string]any{"ThreadId": c.thread, "message": "private"}},
		{"deprecationNotice", map[string]any{"summary": nil}},
		{"deprecationNotice", map[string]any{"summary": "private", "details": 1}},
		{"deprecationNotice", map[string]any{"summary": "private", "extra": true}},
		{"strictReviewRequired", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": -1}},
		{"strictReviewRequired", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": nil}},
		{"strictReviewRequired", map[string]any{"threadId": c.thread, "turnId": domain.NewID(), "startedAtMs": 0}},
		{"strictReviewRequired", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 0, "reviewId": "invented"}},
	} {
		if _, err := observeFixture(c, fixture.method, fixture.params); err == nil {
			t.Fatalf("malformed notice accepted: %s", fixture.method)
		}
	}
	for _, method := range []string{"guardianWarning", "strictReviewRequired"} {
		p := map[string]any{"threadId": domain.NewID(), "message": "private"}
		if method == "strictReviewRequired" {
			p = map[string]any{"threadId": domain.NewID(), "turnId": turn, "startedAtMs": 0}
		}
		event, err := observeFixture(c, method, p)
		if err != nil || event.Kind != NativeExtensionEvent {
			t.Fatal("foreign thread gained notice authority", err)
		}
	}
	c.execution.settings.ApprovalsReviewer = "user"
	if _, err := observeFixture(c, "strictReviewRequired", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 0}); err == nil {
		t.Fatal("manual reviewer accepted strict-review progress")
	}
}

func TestGuardianNoticesRejectServerRequestsAndDuplicateKeys(t *testing.T) {
	c, _ := observationClient()
	for _, method := range []string{"guardianWarning", "deprecationNotice", "strictReviewRequired"} {
		event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: method, ID: json.RawMessage(`"request"`), Params: json.RawMessage(`{}`)})
		if err == nil && event.Kind != NativeExtensionEvent {
			t.Fatal("notification created request authority")
		}
	}
	raw := json.RawMessage(`{"summary":"first","summary":"second"}`)
	if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "deprecationNotice", Params: raw}); err == nil {
		t.Fatal("duplicate diagnostic accepted")
	}
}

func TestGuardianNoticeCannotClearOriginalRecovery(t *testing.T) {
	c, _ := observationClient()
	original := domain.Fail(domain.RecoveryRequired, "Original uncertainty.", "Retain original ownership.")
	c.problem = original
	c.execution.paused = true
	for _, method := range []string{"guardianWarning", "deprecationNotice"} {
		p := map[string]any{"threadId": c.thread, "message": "private-sentinel"}
		if method == "deprecationNotice" {
			p = map[string]any{"summary": "private-sentinel"}
		}
		event, err := observeFixture(c, method, p)
		if err != nil || event.Kind != NoticeEvent || c.problem != original || !c.execution.paused {
			t.Fatal("advisory cleared original recovery", err)
		}
	}
}
