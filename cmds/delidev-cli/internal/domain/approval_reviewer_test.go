// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApprovalReviewerPreservesLegacyAndClosedSelections(t *testing.T) {
	legacy := AgentOptions{Permission: PermissionReadOnly}
	raw, _ := json.Marshal(legacy)
	if strings.Contains(string(raw), "reviewer") || legacy.ApprovalsReviewer.Effective() != CodexReviewerUser {
		t.Fatal("legacy bytes/default changed")
	}
	for _, reviewer := range []ApprovalsReviewer{"", "user", "auto_review", "guardian_subagent", "foreign"} {
		for _, policy := range []string{"", "never", "on-request"} {
			o := legacy
			o.ApprovalsReviewer = reviewer
			o.ApprovalPolicy = policy
			valid := reviewer.Valid() && (reviewer != CodexReviewerAuto || policy == "on-request")
			if (o.ValidateReviewer() == nil) != valid {
				t.Fatalf("reviewer/policy accepted incorrectly: %s/%s", reviewer, policy)
			}
		}
	}
}
func TestAutoReviewLifecycleRetainsIdentityAndDistinctOutcomes(t *testing.T) {
	for _, outcome := range []AutoReviewStatus{AutoReviewApproved, AutoReviewDenied, AutoReviewTimedOut, AutoReviewAborted} {
		start := AutoReviewObservation{ReviewID: "original-review", Status: AutoReviewInProgress, StartedAtMS: 10}
		state, err := ApplyAutoReview(nil, start)
		if err != nil {
			t.Fatal(err)
		}
		end := int64(11)
		done := start
		done.Status, done.CompletedAtMS = outcome, &end
		if _, err := ApplyAutoReview(nil, done); err == nil {
			t.Fatal("completion without start accepted")
		}
		next, err := ApplyAutoReview(state, done)
		if err != nil || next[start.ReviewID].Status != outcome || state[start.ReviewID].Status != AutoReviewInProgress {
			t.Fatal("lost immutable observation", err)
		}
		if _, err := ApplyAutoReview(next, done); err == nil {
			t.Fatal("new publication of terminal review accepted")
		}
		foreign := done
		foreign.StartedAtMS++
		if _, err := ApplyAutoReview(state, foreign); err == nil {
			t.Fatal("changed original timestamp accepted")
		}
		p := ExecutionProgressUpdate{ID: NewID(), Progress: NativeProgress{Kind: AutoReviewProgress, AutoReview: &done}}
		if p.Validate() != nil {
			t.Fatal("valid review progress refused")
		}
		p.Progress.Diff = new(string)
		if p.Validate() == nil {
			t.Fatal("mixed tool/artifact progress accepted")
		}
	}
}
func TestReviewerObservationAndForeignHarnessNeverDowngrade(t *testing.T) {
	c := ExecutionConfiguration{Harness: Codex, NativeModel: "root", Options: AgentOptions{Permission: PermissionReadOnly, ApprovalPolicy: "on-request", ApprovalsReviewer: CodexReviewerAuto}, ReviewerNativeModel: CodexReviewerNativeModel}
	o := ObservedExecutionSettings{Model: "root", Permission: PermissionReadOnly, ApprovalPolicy: "on-request", ApprovalsReviewer: CodexReviewerAuto}
	if o.Validate(c) != nil {
		t.Fatal("selected reviewer refused")
	}
	o.ApprovalsReviewer = CodexReviewerUser
	if o.Validate(c) == nil {
		t.Fatal("silent downgrade accepted")
	}
	c.Harness = ClaudeCode
	if c.ValidateNativeOptions() == nil {
		t.Fatal("foreign reviewer accepted")
	}
}
