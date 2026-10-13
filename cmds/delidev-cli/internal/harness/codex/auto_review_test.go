// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func reviewFixture(thread, turn domain.ID, status string) map[string]any {
	p := map[string]any{"threadId": thread, "turnId": turn, "reviewId": "review-fixture", "startedAtMs": int64(10), "targetItemId": "original-tool", "review": map[string]any{"status": status, "riskLevel": nil, "userAuthorization": nil, "rationale": nil}, "action": map[string]any{"type": "command", "source": "shell", "command": "true", "cwd": "/fixture"}}
	if status != "inProgress" {
		p["completedAtMs"] = int64(11)
		p["decisionSource"] = "agent"
	}
	return p
}
func TestNativeAutoReviewLifecycleAndReplayRemainObservations(t *testing.T) {
	for _, outcome := range []string{"approved", "denied", "timedOut", "aborted"} {
		c, turn := observationClient()
		c.execution.settings.ApprovalsReviewer = "auto_review"
		start := reviewFixture(c.thread, turn, "inProgress")
		event, err := observeFixture(c, "item/autoApprovalReview/started", start)
		if err != nil || event.Kind != AutoReviewEvent || event.AutoReview.Status != domain.AutoReviewInProgress {
			t.Fatal("start failed", err)
		}
		event, err = observeFixture(c, "item/autoApprovalReview/started", start)
		if err != nil || event.Kind != MetadataEvent {
			t.Fatal("exact replay duplicated progress", err)
		}
		done := reviewFixture(c.thread, turn, outcome)
		event, err = observeFixture(c, "item/autoApprovalReview/completed", done)
		if err != nil || event.Kind != AutoReviewEvent || string(event.AutoReview.Status) != outcome {
			t.Fatal("completion failed", err)
		}
		raw, _ := json.Marshal(event.AutoReview)
		if string(raw) == "" {
			t.Fatal("missing projection")
		}
		event, err = observeFixture(c, "item/autoApprovalReview/completed", done)
		if err != nil || event.Kind != MetadataEvent {
			t.Fatal("terminal replay duplicated progress", err)
		}
	}
}
func TestNativeAutoReviewRejectsMismatchesBeforePublication(t *testing.T) {
	for _, mutate := range []func(map[string]any){func(p map[string]any) { delete(p, "targetItemId") }, func(p map[string]any) { p["turnId"] = domain.NewID() }, func(p map[string]any) { p["review"].(map[string]any)["status"] = "unknown" }, func(p map[string]any) { p["action"].(map[string]any)["extra"] = "private" }} {
		c, turn := observationClient()
		c.execution.settings.ApprovalsReviewer = "auto_review"
		p := reviewFixture(c.thread, turn, "inProgress")
		mutate(p)
		if _, err := observeFixture(c, "item/autoApprovalReview/started", p); err == nil {
			t.Fatal("invalid review accepted")
		}
	}
	c, turn := observationClient()
	if _, err := observeFixture(c, "item/autoApprovalReview/started", reviewFixture(c.thread, turn, "inProgress")); err == nil {
		t.Fatal("manual-reviewer execution received AI progress")
	}
	c.execution.settings.ApprovalsReviewer = "auto_review"
	if _, err := observeFixture(c, "item/autoApprovalReview/completed", reviewFixture(c.thread, turn, "approved")); err == nil {
		t.Fatal("out-of-order completion accepted")
	}
	_, _ = observeFixture(c, "item/autoApprovalReview/started", reviewFixture(c.thread, turn, "inProgress"))
	p := reviewFixture(c.thread, turn, "approved")
	p["action"].(map[string]any)["command"] = "different"
	if _, err := observeFixture(c, "item/autoApprovalReview/completed", p); err == nil {
		t.Fatal("changed action accepted")
	}
}
func TestAutoReviewWireSelectionRetainsSandboxAndRejectsPolicyMismatch(t *testing.T) {
	s := threadSettings(t)
	s.Options.ApprovalsReviewer = domain.CodexReviewerAuto
	s.Options.ApprovalPolicy = "on-request"
	p, err := s.wireSettings()
	if err != nil || p.ApprovalsReviewer != "auto_review" || p.ApprovalPolicy != ApprovalOnRequest {
		t.Fatal("reviewer not carried", err)
	}
	s.Options.ApprovalPolicy = "never"
	if _, err = s.wireSettings(); err == nil {
		t.Fatal("incompatible policy silently converted")
	}
}

func TestAutoReviewReplayRejectsChangedPrivateReviewContext(t *testing.T) {
	c, turn := observationClient()
	c.execution.settings.ApprovalsReviewer = "auto_review"
	start := reviewFixture(c.thread, turn, "inProgress")
	if _, err := observeFixture(c, "item/autoApprovalReview/started", start); err != nil {
		t.Fatal(err)
	}
	start["review"].(map[string]any)["rationale"] = "changed private evidence"
	if _, err := observeFixture(c, "item/autoApprovalReview/started", start); err == nil {
		t.Fatal("changed native replay discarded")
	}
}

func networkReviewFixture(thread, turn domain.ID, status, protocol string) map[string]any {
	p := reviewFixture(thread, turn, status)
	p["action"] = map[string]any{"type": "networkAccess", "target": "https://example.test:443", "host": "example.test", "protocol": protocol, "port": 443}
	return p
}

func TestNativeAutoReviewNetworkAccessRejectsUnknownProtocolsBeforeStateChange(t *testing.T) {
	for _, protocol := range []string{"ftp", "", "unknown", "HTTP", "Https", "socks5tcp", "socks5UDP", " http"} {
		c, turn := observationClient()
		c.execution.settings.ApprovalsReviewer = "auto_review"
		if _, err := observeFixture(c, "item/autoApprovalReview/started", networkReviewFixture(c.thread, turn, "inProgress", protocol)); err == nil {
			t.Fatalf("protocol %q was accepted", protocol)
		}
		if len(c.execution.autoReviews) != 0 || len(c.execution.autoReviewActions) != 0 || len(c.execution.autoReviewPayloads) != 0 {
			t.Fatalf("protocol %q changed auto-review state", protocol)
		}
		// A rejected start leaves no state, so a completion cannot settle it.
		if _, err := observeFixture(c, "item/autoApprovalReview/completed", networkReviewFixture(c.thread, turn, "approved", protocol)); err == nil {
			t.Fatalf("protocol %q completion was accepted", protocol)
		}
	}
	// A valid start followed by a completion with an unknown protocol must not settle.
	c, turn := observationClient()
	c.execution.settings.ApprovalsReviewer = "auto_review"
	if _, err := observeFixture(c, "item/autoApprovalReview/started", networkReviewFixture(c.thread, turn, "inProgress", string(ApprovalHTTPS))); err != nil {
		t.Fatal(err)
	}
	if _, err := observeFixture(c, "item/autoApprovalReview/completed", networkReviewFixture(c.thread, turn, "approved", "ftp")); err == nil {
		t.Fatal("completion with unknown protocol was accepted")
	}
	if got := c.execution.autoReviews[turn]["review-fixture"].Status; got != domain.AutoReviewInProgress {
		t.Fatalf("rejected completion changed review state: %v", got)
	}
}

func TestNativeAutoReviewNetworkAccessAllowedProtocolsSettleAndReplay(t *testing.T) {
	for _, protocol := range []NetworkApprovalProtocol{ApprovalHTTP, ApprovalHTTPS, ApprovalSOCKSTCP, ApprovalSOCKSUDP} {
		c, turn := observationClient()
		c.execution.settings.ApprovalsReviewer = "auto_review"
		start := networkReviewFixture(c.thread, turn, "inProgress", string(protocol))
		done := networkReviewFixture(c.thread, turn, "approved", string(protocol))
		event, err := observeFixture(c, "item/autoApprovalReview/started", start)
		if err != nil || event.Kind != AutoReviewEvent || event.AutoReview.Status != domain.AutoReviewInProgress {
			t.Fatalf("%s start failed: %v", protocol, err)
		}
		if event, err = observeFixture(c, "item/autoApprovalReview/started", start); err != nil || event.Kind != MetadataEvent {
			t.Fatalf("%s start replay was not idempotent: %v", protocol, err)
		}
		if event, err = observeFixture(c, "item/autoApprovalReview/completed", done); err != nil || event.Kind != AutoReviewEvent || event.AutoReview.Status != domain.AutoReviewApproved {
			t.Fatalf("%s completion failed: %v", protocol, err)
		}
		if event, err = observeFixture(c, "item/autoApprovalReview/completed", done); err != nil || event.Kind != MetadataEvent {
			t.Fatalf("%s completion replay was not idempotent: %v", protocol, err)
		}
	}
}
