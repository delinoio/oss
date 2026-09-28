package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"
)

const MaxRetainedPRProblems = 10000

type PRProblemRecordType string

const (
	PRProblemSetRecord      PRProblemRecordType = "pull-request-set"
	PRProblemEvidenceRecord PRProblemRecordType = "pull-request-evidence"
)

type PRProblemKind string

const (
	PRFeedbackProblem      PRProblemKind = "review-feedback"
	PRCIProblem            PRProblemKind = "ci-failure"
	PRMergeConflictProblem PRProblemKind = "merge-conflict"
)

type PRProblemState string

const (
	PRProblemUnhandled PRProblemState = "unhandled"
	PRProblemDismissed PRProblemState = "locally-dismissed"
)

// PRProblemKey is shared across local aliases and sessions. Names, checkout
// configuration, PR heads and token generations cannot create another PR owner.
func PRProblemKey(provider IntegrationProvider, repository, pullRequest string) string {
	if provider != GitHubCom || !PositiveDecimal(repository) || !PositiveDecimal(pullRequest) {
		return ""
	}
	raw, _ := json.Marshal([]string{string(provider), repository, pullRequest})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type PRProblemObservation struct {
	BaseSHA    string    `json:"base_sha"`
	HeadSHA    string    `json:"head_sha"`
	ObservedAt time.Time `json:"observed_at"`
}

func (v PRProblemObservation) Validate() error {
	if !repositorySHA(v.BaseSHA) || !repositorySHA(v.HeadSHA) || !ciEvidenceTime(v.ObservedAt) {
		return invalidPRProblem()
	}
	return nil
}

type PRProblemSet struct {
	Version     uint32                  `json:"version"`
	Type        PRProblemRecordType     `json:"type"`
	Target      SessionPullRequest      `json:"target"`
	Feedback    *PRProblemObservation   `json:"feedback,omitempty"`
	CI          *PRCIObservationSummary `json:"ci,omitempty"`
	Conflict    *PRConflictObservation  `json:"conflict,omitempty"`
	Remediation *PRRemediationChain     `json:"remediation,omitempty"`
}

func (v PRProblemSet) Validate() error {
	if v.Remediation != nil && v.Remediation.Validate() != nil {
		return invalidPRProblem()
	}
	if v.Version != 1 || v.Type != PRProblemSetRecord || v.Target.Validate() != nil || (v.Feedback == nil && v.CI == nil && v.Conflict == nil) || (v.Feedback != nil && v.Feedback.Validate() != nil) || (v.CI != nil && v.CI.Validate() != nil) || (v.Conflict != nil && v.Conflict.Validate() != nil) {
		return invalidPRProblem()
	}
	return nil
}

type PRFeedbackProviderState struct {
	NativeState    string `json:"native_state,omitempty"`
	ReviewState    string `json:"review_state,omitempty"`
	AuthorPresent  bool   `json:"author_present"`
	ThreadResolved *bool  `json:"thread_resolved,omitempty"`
	ThreadOutdated *bool  `json:"thread_outdated,omitempty"`
}

func FeedbackProviderState(entry PRFeedback, threads []PRFeedbackThread) (PRFeedbackProviderState, error) {
	v := PRFeedbackProviderState{NativeState: entry.NativeState, ReviewState: entry.ReviewState, AuthorPresent: entry.Author != nil}
	if entry.Kind == PRReviewComment {
		found := false
		for _, thread := range threads {
			if thread.NodeID == entry.ThreadNodeID && slices.Contains(thread.CommentNodes, entry.NodeID) {
				if found {
					return v, invalidPRProblem()
				}
				resolved, outdated := thread.Resolved, thread.Outdated
				v.ThreadResolved, v.ThreadOutdated, found = &resolved, &outdated, true
			}
		}
		if !found {
			return v, invalidPRProblem()
		}
	}
	return v, nil
}

type PRProblemDismissal struct {
	RequestID ID         `json:"request_id"`
	ActorType DeviceType `json:"actor_type"`
	DeviceID  ID         `json:"device_id,omitempty"`
	At        time.Time  `json:"at"`
}

type PRProblem struct {
	Version          uint32                   `json:"version"`
	Type             PRProblemRecordType      `json:"type"`
	SetID            ID                       `json:"set_id"`
	Kind             PRProblemKind            `json:"kind"`
	Target           SessionPullRequest       `json:"target"`
	Observation      PRProblemObservation     `json:"observation"`
	ContentVersion   string                   `json:"content_version"`
	Feedback         *PRFeedback              `json:"feedback,omitempty"`
	OriginalProvider *PRFeedbackProviderState `json:"original_provider,omitempty"`
	LatestProvider   *PRFeedbackProviderState `json:"latest_provider,omitempty"`
	CI               *PRCIProblemEvidence     `json:"ci,omitempty"`
	Conflict         *PRConflictSnapshot      `json:"conflict,omitempty"`
	Current          bool                     `json:"current"`
	State            PRProblemState           `json:"state"`
	Dismissal        *PRProblemDismissal      `json:"dismissal,omitempty"`
}

func (v PRFeedbackProviderState) validate(kind PRFeedbackKind) error {
	switch kind {
	case PRReviewBody:
		if !PublishedReviewState(v.NativeState) || v.ReviewState != "" || v.ThreadResolved != nil || v.ThreadOutdated != nil {
			return invalidPRProblem()
		}
	case PRConversationComment:
		if v.NativeState != "" || v.ReviewState != "" || v.ThreadResolved != nil || v.ThreadOutdated != nil {
			return invalidPRProblem()
		}
	case PRReviewComment:
		if v.NativeState != "SUBMITTED" || !PublishedReviewState(v.ReviewState) || v.ThreadResolved == nil || v.ThreadOutdated == nil {
			return invalidPRProblem()
		}
	default:
		return invalidPRProblem()
	}
	return nil
}

func (v PRProblem) Validate() error {
	if v.Version != 1 || v.Type != PRProblemEvidenceRecord || v.SetID.Validate() != nil || v.Target.Validate() != nil || v.Observation.Validate() != nil || !lowerDigest(v.ContentVersion) {
		return invalidPRProblem()
	}
	switch v.Kind {
	case PRFeedbackProblem:
		if v.Feedback == nil || v.OriginalProvider == nil || v.LatestProvider == nil || v.CI != nil || v.Conflict != nil || v.ContentVersion != v.Feedback.ContentVersion {
			return invalidPRProblem()
		}
		item := RepositoryItem{URL: RepositoryItemURL(v.Target.Owner, v.Target.Name, RepositoryPullRequest, v.Target.Number)}
		if v.Feedback.Validate(item) != nil || v.OriginalProvider.validate(v.Feedback.Kind) != nil || v.LatestProvider.validate(v.Feedback.Kind) != nil || v.OriginalProvider.NativeState != v.Feedback.NativeState || v.OriginalProvider.ReviewState != v.Feedback.ReviewState || v.OriginalProvider.AuthorPresent != (v.Feedback.Author != nil) {
			return invalidPRProblem()
		}
	case PRCIProblem:
		if v.CI == nil || v.Feedback != nil || v.OriginalProvider != nil || v.LatestProvider != nil || v.Conflict != nil || v.CI.Validate() != nil || v.ContentVersion != v.CI.Context.Version() {
			return invalidPRProblem()
		}
	case PRMergeConflictProblem:
		if v.Conflict == nil || v.Feedback != nil || v.OriginalProvider != nil || v.LatestProvider != nil || v.CI != nil || v.Conflict.Validate() != nil || v.ContentVersion != v.Conflict.ContentVersion() || v.Observation.BaseSHA != v.Conflict.Observation.BaseSHA || v.Observation.HeadSHA != v.Conflict.Observation.HeadSHA || !v.Observation.ObservedAt.Equal(v.Conflict.Observation.ObservedAt) {
			return invalidPRProblem()
		}
	default:
		return invalidPRProblem()
	}
	switch v.State {
	case PRProblemUnhandled:
		if v.Dismissal != nil {
			return invalidPRProblem()
		}
	case PRProblemDismissed:
		d := v.Dismissal
		if d == nil || d.RequestID.Validate() != nil || !ciEvidenceTime(d.At) {
			return invalidPRProblem()
		}
		if d.ActorType == ClientDevice {
			if d.DeviceID.Validate() != nil {
				return invalidPRProblem()
			}
		} else if d.ActorType != OwnerDevice || d.DeviceID != "" {
			return invalidPRProblem()
		}
	default:
		return invalidPRProblem()
	}
	return nil
}

func invalidPRProblem() error {
	return Fail(RecoveryRequired, "The retained PR problem evidence is inconsistent.", "Preserve the original problem and content version for inspection.")
}

func (v PRProblem) NativeNode() string {
	switch v.Kind {
	case PRFeedbackProblem:
		if v.Feedback != nil {
			return v.Feedback.NodeID
		}
	case PRCIProblem:
		if v.CI != nil {
			return v.CI.Context.NodeID
		}
	case PRMergeConflictProblem:
		if v.Conflict != nil {
			return "conflict:" + string(v.Conflict.TransitionID)
		}
	}
	return ""
}
