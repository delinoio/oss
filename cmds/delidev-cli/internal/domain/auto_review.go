// SPDX-License-Identifier: Apache-2.0
package domain

import "slices"

type AutoReviewStatus string

const (
	AutoReviewInProgress AutoReviewStatus = "inProgress"
	AutoReviewApproved   AutoReviewStatus = "approved"
	AutoReviewDenied     AutoReviewStatus = "denied"
	AutoReviewTimedOut   AutoReviewStatus = "timedOut"
	AutoReviewAborted    AutoReviewStatus = "aborted"
	AutoReviewProgress   ProgressKind     = "codex-auto-review"
)

// This metadata never grants an approval, input retry or successful tool result.
// Native action context, rationale and assessment remain private.
type AutoReviewObservation struct {
	ReviewID      string           `json:"review_id"`
	TargetItemID  *string          `json:"target_item_id"`
	Status        AutoReviewStatus `json:"status"`
	StartedAtMS   int64            `json:"started_at_ms"`
	CompletedAtMS *int64           `json:"completed_at_ms"`
}

func (v AutoReviewObservation) Validate() error {
	if Text(v.ReviewID, "native review identity", 1024, true) != nil || v.TargetItemID != nil && Text(*v.TargetItemID, "review target", 1024, true) != nil || v.StartedAtMS < 0 || !slices.Contains([]AutoReviewStatus{AutoReviewInProgress, AutoReviewApproved, AutoReviewDenied, AutoReviewTimedOut, AutoReviewAborted}, v.Status) || (v.Status == AutoReviewInProgress) != (v.CompletedAtMS == nil) || v.CompletedAtMS != nil && *v.CompletedAtMS < v.StartedAtMS {
		return invalidObservation()
	}
	return nil
}

type AutoReviewState map[string]AutoReviewObservation

func ApplyAutoReview(prior AutoReviewState, v AutoReviewObservation) (AutoReviewState, error) {
	if v.Validate() != nil || len(prior) > 4096 {
		return nil, invalidObservation()
	}
	old, exists := prior[v.ReviewID]
	if v.Status == AutoReviewInProgress && exists || v.Status != AutoReviewInProgress && (!exists || old.Status != AutoReviewInProgress || old.StartedAtMS != v.StartedAtMS || (old.TargetItemID == nil) != (v.TargetItemID == nil) || old.TargetItemID != nil && *old.TargetItemID != *v.TargetItemID) || !exists && len(prior) >= 4096 {
		return nil, invalidObservation()
	}
	next := AutoReviewState{}
	for k, o := range prior {
		next[k] = o
	}
	next[v.ReviewID] = v
	return next, nil
}

// Closed confirms that original observed reviews have independent terminal outcomes.
func (s AutoReviewState) Closed() bool {
	for _, review := range s {
		if review.Status == AutoReviewInProgress {
			return false
		}
	}
	return true
}
