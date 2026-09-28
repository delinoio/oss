package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

type PRRemediationMode string

const (
	PRRemediationManual    PRRemediationMode = "manual"
	PRRemediationAutomatic PRRemediationMode = "automatic"
)

func (m PRRemediationMode) Valid() bool {
	return m == PRRemediationManual || m == PRRemediationAutomatic
}

type PRRemediationReason string

const (
	PRRemediationEligible        PRRemediationReason = "eligible"
	PRRemediationAlreadyHandled  PRRemediationReason = "already-handled"
	PRRemediationDisabled        PRRemediationReason = "automatic-kind-disabled"
	PRRemediationStale           PRRemediationReason = "observation-expired"
	PRRemediationClosed          PRRemediationReason = "pull-request-closed"
	PRRemediationVersionChanged  PRRemediationReason = "original-version-unconfirmed"
	PRRemediationReviewerUnknown PRRemediationReason = "reviewer-unknown"
	PRRemediationReviewerDenied  PRRemediationReason = "reviewer-does-not-match"
	PRRemediationCIUnknown       PRRemediationReason = "ci-evaluation-unknown"
	PRRemediationNoCIFailure     PRRemediationReason = "required-failure-not-current"
	PRRemediationConflictUnknown PRRemediationReason = "mergeability-unknown"
	PRRemediationNoConflict      PRRemediationReason = "conflict-not-current"
)

// This bounds the interval between a new provider observation and the atomic
// dispatch claim. It is not a cache TTL or permission to reuse a stored proof.
const MaxPRRemediationObservationAge = 30 * time.Second

// EvaluatePRRemediation checks only the selected kind's remote prerequisites.
// The controller must also revalidate local policy, profile generation, session,
// workspace, Worker, account and native permissions in its dispatch transaction.
// In particular, a successful decision is never a transferable execution grant.
func EvaluatePRRemediation(problem PRProblem, observed RepositoryQueryResult, policy RemediationPolicy, mode PRRemediationMode, now time.Time) (PRRemediationReason, error) {
	if problem.Validate() != nil || observed.Validate() != nil || policy.Validate() != nil || !mode.Valid() || !ciEvidenceTime(now) {
		return "", invalidPRRemediation()
	}
	target, err := PRTargetFromObservation(observed)
	if err != nil || !target.SamePR(problem.Target) || target.RepositoryNodeID != problem.Target.RepositoryNodeID || target.PullRequestNodeID != problem.Target.PullRequestNodeID || target.Number != problem.Target.Number {
		return "", invalidPRRemediation()
	}
	if problem.State != PRProblemUnhandled {
		return PRRemediationAlreadyHandled, nil
	}
	if mode == PRRemediationAutomatic && !policy.AutomaticKind(problem.Kind) {
		return PRRemediationDisabled, nil
	}
	if observed.ObservedAt.After(now) || now.Sub(observed.ObservedAt) > MaxPRRemediationObservationAge {
		return PRRemediationStale, nil
	}
	item := observed.Items[0]
	if item.State != RepositoryItemOpen || item.Merged == nil || *item.Merged {
		return PRRemediationClosed, nil
	}
	switch problem.Kind {
	case PRFeedbackProblem:
		if mode == PRRemediationManual {
			// An explicit action may address original retained feedback even after
			// a provider edit/deletion. It still requires a fresh open PR binding.
			if observed.Query.Operation != RepositoryDetail {
				return "", invalidPRRemediation()
			}
			return PRRemediationEligible, nil
		}
		if observed.Query.Operation != RepositoryReviewers || observed.Reviewers == nil {
			return "", invalidPRRemediation()
		}
		found := false
		for _, entry := range observed.Reviewers.Feedback.Entries {
			if entry.NodeID == problem.Feedback.NodeID && entry.ContentVersion == problem.ContentVersion {
				// Author identity does not participate in the body/edit digest.
				// Independently retain its original namespace instead of letting
				// changed provider metadata substitute another selected author.
				original, current := problem.Feedback.Author, entry.Author
				if original == nil || current == nil || original.Kind != current.Kind || original.ID != current.ID || original.NodeID != current.NodeID {
					return PRRemediationReviewerUnknown, nil
				}
				found = true
				break
			}
		}
		if !found {
			return PRRemediationVersionChanged, nil
		}
		switch observed.Reviewers.Match(problem.Feedback.NodeID, policy.ReviewerSelectors) {
		case ReviewerMatches:
			return PRRemediationEligible, nil
		case ReviewerDoesNotMatch:
			return PRRemediationReviewerDenied, nil
		default:
			return PRRemediationReviewerUnknown, nil
		}
	case PRCIProblem:
		if observed.Query.Operation != RepositoryCI || observed.CI == nil {
			return "", invalidPRRemediation()
		}
		if observed.CI.Result.State == CIUnknown {
			return PRRemediationCIUnknown, nil
		}
		proof, err := NewPRCIObservation(problem.SetID, observed)
		if err != nil {
			return "", err
		}
		contexts, err := proof.FailureContexts()
		if err != nil {
			return "", err
		}
		for _, current := range contexts {
			if current.NodeID == problem.CI.Context.NodeID && current.Version() == problem.ContentVersion {
				return PRRemediationEligible, nil
			}
		}
		return PRRemediationNoCIFailure, nil
	case PRMergeConflictProblem:
		if observed.Query.Operation != RepositoryDetail {
			return "", invalidPRRemediation()
		}
		if item.Mergeable == nil {
			return PRRemediationConflictUnknown, nil
		}
		if *item.Mergeable {
			return PRRemediationNoConflict, nil
		}
		original := problem.Conflict
		if !problem.Current || original.BaseRef != item.BaseRef || original.HeadRef != item.HeadRef || original.Observation.BaseSHA != item.BaseSHA || original.Observation.HeadSHA != item.HeadSHA {
			return PRRemediationVersionChanged, nil
		}
		return PRRemediationEligible, nil
	default:
		return "", invalidPRRemediation()
	}
}

func (p RemediationPolicy) AutomaticKind(kind PRProblemKind) bool {
	switch kind {
	case PRFeedbackProblem:
		return p.ReviewFeedback
	case PRCIProblem:
		return p.CIFailure
	case PRMergeConflictProblem:
		return p.MergeConflict
	default:
		return false
	}
}

func (p RemediationPolicy) Digest() string {
	if p.Validate() != nil {
		return ""
	}
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func invalidPRRemediation() error {
	return Fail(RecoveryRequired, "The PR remediation evidence is inconsistent.", "Preserve the original PR, problem versions and execution ownership before reconciliation.")
}

func PRRemediationInputDigest(input QueuedInput) string {
	if input.Sequence == 0 || input.ContentRevision == 0 || (SessionInput{Prompt: input.Prompt, Mode: input.Mode}).Validate() != nil {
		return ""
	}
	raw, _ := json.Marshal(struct {
		Sequence, Revision uint64
		Prompt             string
		Mode               SessionMode
	}{input.Sequence, input.ContentRevision, input.Prompt, input.Mode})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
