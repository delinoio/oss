// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func advisoryFixture() (*Client, domain.ID) {
	c, turn := observationClient()
	c.execution.settings.ApprovalsReviewer = "auto_review"
	return c, turn
}

func TestGuardianAndDeprecationAcceptEmptySchemaStrings(t *testing.T) {
	for _, sample := range []struct{ label, value string }{{"empty", ""}, {"whitespace", " \t"}} {
		for _, item := range []struct {
			method string
			params func(*Client) map[string]any
			notice domain.NativeNotice
		}{
			{
				method: "guardianWarning",
				params: func(c *Client) map[string]any {
					return map[string]any{"threadId": c.thread, "message": sample.value}
				},
				notice: domain.NativeWarning,
			},
			{
				method: "deprecationNotice",
				params: func(*Client) map[string]any {
					return map[string]any{"summary": sample.value}
				},
				notice: domain.NativeConfigWarning,
			},
		} {
			t.Run(item.method+"/"+sample.label, func(t *testing.T) {
				c, _ := advisoryFixture()
				event, err := observeFixture(c, item.method, item.params(c))
				if err != nil || event.Kind != NoticeEvent || event.Notice != item.notice || !event.Correlated {
					t.Fatal("valid schema string was rejected or changed notice category", event, err)
				}
			})
		}
	}
}

func TestGuardianDeprecationStrictReviewKeepOnlyBoundedNoticesAndOriginalCompletion(t *testing.T) {
	for _, method := range []string{"guardianWarning", "deprecationNotice", "autoApprovalReview/strictReviewRequired"} {
		t.Run(method, func(t *testing.T) {
			c, turn := advisoryFixture()
			settings := c.execution.settings
			params := map[string]any{"threadId": c.thread, "message": "private-guardian-instructions"}
			if method == "deprecationNotice" {
				params = map[string]any{"summary": "private-deprecated-setting", "details": nil}
			}
			if method == "autoApprovalReview/strictReviewRequired" {
				params = map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 0}
			}
			event, err := observeFixture(c, method, params)
			if err != nil || event.Kind != NoticeEvent || !event.Correlated || event.Interaction != nil || event.AutoReview != nil {
				t.Fatal("valid notice failed or gained approval", event, err)
			}
			raw, _ := json.Marshal(event)
			if strings.Contains(string(raw), "private-") {
				t.Fatal("native notice leaked", string(raw))
			}
			if !reflect.DeepEqual(settings, c.execution.settings) || len(c.execution.autoReviews) != 0 || len(c.execution.interactions.arrivals) != 0 {
				t.Fatal("notice changed permission/review ownership")
			}
			event, err = observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": fixtureTurn(turn, TurnCompleted)})
			if err != nil || event.Kind != TurnCompletedEvent || event.Late || !event.Correlated {
				t.Fatal("notice blocked original completion", event, err)
			}
			if method == "autoApprovalReview/strictReviewRequired" {
				replay, err := observeFixture(c, method, params)
				if err != nil || replay.Kind != MetadataEvent || replay.Metadata != StrictReviewReplayChecked {
					t.Fatal("exact terminal replay regained progress", replay, err)
				}
				params["startedAtMs"] = 1
				if _, err := observeFixture(c, method, params); err == nil {
					t.Fatal("new terminal notice accepted")
				}
			}
		})
	}
}
func TestGuardianDeprecationStrictReviewMalformedAndForeignFences(t *testing.T) {
	for _, method := range []string{"guardianWarning", "deprecationNotice", "autoApprovalReview/strictReviewRequired"} {
		for _, change := range []string{"missing", "null", "unknown", "oversized", "foreign", "negative", "reviewer", "turn", "notification-kind"} {
			t.Run(method+"/"+change, func(t *testing.T) {
				c, turn := advisoryFixture()
				params := map[string]any{"threadId": c.thread, "message": "private-body"}
				if method == "deprecationNotice" {
					params = map[string]any{"summary": "private-summary", "details": nil}
				}
				if method == "autoApprovalReview/strictReviewRequired" {
					params = map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 1}
				}
				switch change {
				case "missing":
					if method == "guardianWarning" {
						delete(params, "message")
					} else if method == "deprecationNotice" {
						delete(params, "summary")
					} else {
						delete(params, "startedAtMs")
					}
				case "null":
					if method == "guardianWarning" {
						params["message"] = nil
					} else if method == "deprecationNotice" {
						params["summary"] = nil
					} else {
						params["startedAtMs"] = nil
					}
				case "unknown":
					params["approval"] = true
				case "oversized":
					if method == "guardianWarning" {
						params["message"] = strings.Repeat("x", nativewire.MaxFrame+1)
					} else if method == "deprecationNotice" {
						params["details"] = strings.Repeat("x", nativewire.MaxFrame+1)
					} else {
						params["startedAtMs"] = "overflow"
					}
				case "foreign":
					if method == "deprecationNotice" {
						return
					}
					params["threadId"] = domain.NewID()
				case "negative":
					if method != "autoApprovalReview/strictReviewRequired" {
						return
					}
					params["startedAtMs"] = -1
				case "reviewer":
					if method != "autoApprovalReview/strictReviewRequired" {
						return
					}
					c.execution.settings.ApprovalsReviewer = "user"
				case "turn":
					if method != "autoApprovalReview/strictReviewRequired" {
						return
					}
					params["turnId"] = domain.NewID()
				}
				body, _ := json.Marshal(params)
				kind := nativewire.Notification
				if change == "notification-kind" {
					kind = nativewire.ServerRequest
				}
				event, err := c.observeEventLocked(nativewire.Event{Kind: kind, Method: method, Params: body})
				if err == nil && event.Kind != NativeExtensionEvent {
					t.Fatal("invalid notice accepted", event)
				}
				if len(c.execution.strictReviewNotices) != 0 || len(c.execution.autoReviews) != 0 || len(c.execution.interactions.arrivals) != 0 {
					t.Fatal("invalid notice changed native authority")
				}
			})
		}
	}
	c, turn := advisoryFixture()
	raw := `{"threadId":"` + string(c.thread) + `","turnId":"` + string(turn) + `","startedAtMs":0,"startedAtMs":1}`
	if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "autoApprovalReview/strictReviewRequired", Params: json.RawMessage(raw)}); err == nil {
		t.Fatal("duplicate native keys accepted")
	}
}
func TestStrictReviewNoticeReplayAndRecoveryRemainIndependent(t *testing.T) {
	c, turn := advisoryFixture()
	problem := turnUncertain()
	c.problem = problem
	c.execution.paused = true
	params := map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 23}
	first, err := observeFixture(c, "autoApprovalReview/strictReviewRequired", params)
	if err != nil || first.Kind != NoticeEvent {
		t.Fatal(err)
	}
	replay, err := observeFixture(c, "autoApprovalReview/strictReviewRequired", params)
	if err != nil || replay.Metadata != StrictReviewReplayChecked || len(c.execution.strictReviewNotices) != 1 {
		t.Fatal("replay changed progress", err)
	}
	if c.problem != problem || !c.execution.paused {
		t.Fatal("notice cleared recovery")
	}
	c.execution.strictReviewNotices = map[strictReviewNoticeKey]bool{}
	for i := 0; i < maxTrackedTurns; i++ {
		c.execution.strictReviewNotices[strictReviewNoticeKey{Turn: turn, StartedAtMS: int64(i)}] = true
	}
	params["startedAtMs"] = maxTrackedTurns
	if _, err := observeFixture(c, "autoApprovalReview/strictReviewRequired", params); err == nil {
		t.Fatal("notice retention bound ignored")
	}
}

func TestDeprecationOptionalDetailsStayPrivate(t *testing.T) {
	for _, details := range []string{"absent", "null", "text"} {
		t.Run(details, func(t *testing.T) {
			c, _ := advisoryFixture()
			p := map[string]any{"summary": "private-summary"}
			if details == "null" {
				p["details"] = nil
			} else if details == "text" {
				p["details"] = "private-migration-instructions"
			}
			event, err := observeFixture(c, "deprecationNotice", p)
			raw, _ := json.Marshal(event)
			if err != nil || event.Kind != NoticeEvent || strings.Contains(string(raw), "private") {
				t.Fatal("valid optional details failed or leaked", err)
			}
		})
	}
}

func TestStrictReviewNoticeCannotSettleNativeReviewOrChangeSandbox(t *testing.T) {
	c, turn := advisoryFixture()
	settings := c.execution.settings
	start := reviewFixture(c.thread, turn, "inProgress")
	if _, err := observeFixture(c, "item/autoApprovalReview/started", start); err != nil {
		t.Fatal(err)
	}
	if _, err := observeFixture(c, "autoApprovalReview/strictReviewRequired", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 10}); err != nil {
		t.Fatal(err)
	}
	if c.execution.autoReviews[turn].Closed() || !reflect.DeepEqual(settings, c.execution.settings) {
		t.Fatal("notice settled review or changed sandbox")
	}
	if _, err := observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": fixtureTurn(turn, TurnCompleted)}); err == nil {
		t.Fatal("notice completed unsettled review")
	}
	if _, err := observeFixture(c, "item/autoApprovalReview/completed", reviewFixture(c.thread, turn, "denied")); err != nil {
		t.Fatal(err)
	}
	if _, err := observeFixture(c, "autoApprovalReview/strictReviewRequired", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 11}); err != nil {
		t.Fatal(err)
	}
	if c.execution.autoReviews[turn]["review-fixture"].Status != domain.AutoReviewDenied || !reflect.DeepEqual(settings, c.execution.settings) {
		t.Fatal("notice reversed native denial")
	}
}

func TestGuardianDeprecationStrictReviewOrderedWireLogsRemainPrivate(t *testing.T) {
	c, capture := openThreadFixture(t, "thread-turn-advisory-auto-review")
	settings := threadSettings(t)
	settings.Options.ApprovalsReviewer = domain.CodexReviewerAuto
	if _, err := c.StartThread(context.Background(), domain.NewID(), settings); err != nil {
		t.Fatal(err)
	}
	result, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, TurnStartedEvent)
	nextKind(t, c, MessageCompletedEvent)
	var logs bytes.Buffer
	c.logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	for _, item := range []struct {
		method string
		params map[string]any
	}{
		{"guardianWarning", map[string]any{"threadId": c.thread, "message": "private-wire-guardian-instructions"}},
		{"deprecationNotice", map[string]any{"summary": "private-wire-deprecation", "details": "private-wire-migration"}},
		{"autoApprovalReview/strictReviewRequired", map[string]any{"threadId": c.thread, "turnId": result.TurnID, "startedAtMs": 0}},
	} {
		fixtureSignal(t, c, "notify", map[string]any{"method": item.method, "params": item.params})
		event := nextKind(t, c, NoticeEvent)
		raw, _ := json.Marshal(event)
		if strings.Contains(string(raw), "private-wire") || strings.Contains(logs.String(), "private-wire") {
			t.Fatal("native notice escaped event/log projection")
		}
	}
	if len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(c.execution.interactions.arrivals) != 0 {
		t.Fatal("advisory sent native input/response")
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": string(TurnCompleted)})
	event := nextKind(t, c, TurnCompletedEvent)
	if event.TurnID != result.TurnID || event.Turn.Status != TurnCompleted {
		t.Fatal("original result replaced")
	}
}

func TestStrictReviewNoticeRejectsBareDiscriminator(t *testing.T) {
	c, turn := advisoryFixture()
	event, err := observeFixture(c, "strictReviewRequired", map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 0})
	if err != nil || event.Kind != NativeExtensionEvent || event.Correlated || len(c.execution.strictReviewNotices) != 0 {
		t.Fatal("unsupported bare discriminator gained notice authority", event, err)
	}
}
