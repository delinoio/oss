package domain

import "slices"

type ConflictStrategy string

const (
	MergeConflictStrategy  ConflictStrategy = "merge"
	RebaseConflictStrategy ConflictStrategy = "rebase"
)

type SessionStrategy string

const (
	ReuseSession     SessionStrategy = "reuse"
	DedicatedSession SessionStrategy = "dedicated"
)

type RemediationPolicy struct {
	CIFailure         bool               `json:"ci_failure"`
	ReviewFeedback    bool               `json:"review_feedback"`
	MergeConflict     bool               `json:"merge_conflict"`
	ReviewerSelectors []ReviewerSelector `json:"reviewer_selectors,omitempty"`
	ConflictStrategy  ConflictStrategy   `json:"conflict_strategy"`
	SessionStrategy   SessionStrategy    `json:"session_strategy"`
	AttemptLimit      uint32             `json:"attempt_limit"`
	AgentID           ID                 `json:"agent_id,omitempty"`
	MachineID         ID                 `json:"machine_id,omitempty"`
}

func DefaultRemediationPolicy() RemediationPolicy {
	return RemediationPolicy{ConflictStrategy: MergeConflictStrategy, SessionStrategy: ReuseSession, AttemptLimit: 3}
}

func (p RemediationPolicy) Validate() error {
	if p.ConflictStrategy != MergeConflictStrategy && p.ConflictStrategy != RebaseConflictStrategy {
		return Fail(InvalidArgument, "Invalid conflict strategy.", "Select merge or rebase.")
	}
	if p.SessionStrategy != ReuseSession && p.SessionStrategy != DedicatedSession {
		return Fail(InvalidArgument, "Invalid remediation session strategy.", "Select reuse or dedicated.")
	}
	if p.AttemptLimit < 1 || p.AttemptLimit > 100 {
		return Fail(InvalidArgument, "Invalid remediation attempt limit.", "Use 1 through 100 attempts; the default is 3.")
	}
	for _, id := range []ID{p.AgentID, p.MachineID} {
		if id != "" {
			if err := id.Validate(); err != nil {
				return err
			}
		}
	}
	return ValidateReviewerSelectors(p.ReviewerSelectors)
}

// An explicit repository policy replaces the complete server policy. In
// particular, false switches, empty selectors and missing execution choices
// never inherit a more permissive value. The returned snapshot owns its slice.
func (r Repository) EffectiveRemediation(defaults RemediationPolicy) (RemediationPolicy, error) {
	policy := defaults
	if r.Remediation != nil {
		policy = *r.Remediation
	}
	if err := policy.Validate(); err != nil {
		return RemediationPolicy{}, err
	}
	policy.ReviewerSelectors = slices.Clone(policy.ReviewerSelectors)
	return policy, nil
}
