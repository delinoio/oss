package domain

// PR activity retains only original metadata. It is published with its source
// transaction, never reconstructed from a later provider or execution result.
const PRActivityRecord PRProblemRecordType = "pull-request-activity"

type PRActivityAction string

const (
	PRActivityObserved        PRActivityAction = "problem-observed"
	PRActivityDismissed       PRActivityAction = "problem-dismissed"
	PRActivityAttempt         PRActivityAction = "remediation-attempt"
	PRActivityVerifiedHandled PRActivityAction = "verified-handled"
)

type PRActivity struct {
	Version            uint32                    `json:"version"`
	Type               PRProblemRecordType       `json:"type"`
	Action             PRActivityAction          `json:"action"`
	SourceID           ID                        `json:"source_id"`
	SourceRevision     uint64                    `json:"source_revision"`
	SetID              ID                        `json:"set_id"`
	RemoteRepositoryID string                    `json:"remote_repository_id"`
	PullRequestID      string                    `json:"pull_request_id"`
	Number             string                    `json:"number"`
	Owner              string                    `json:"owner"`
	Name               string                    `json:"name"`
	Problems           []PRRemediationProblemRef `json:"problems"`
	Actor              PRProblemDismissal        `json:"actor"`
	AttemptState       PRRemediationAttemptState `json:"attempt_state,omitempty"`
	Mode               PRRemediationMode         `json:"mode,omitempty"`
	Outcome            ExecutionOutcome          `json:"outcome,omitempty"`
	ExecutionID        ID                        `json:"execution_id,omitempty"`
	VerificationID     ID                        `json:"verification_id,omitempty"`
}

func (v PRActivity) Validate() error {
	if v.Version != 1 || v.Type != PRActivityRecord || v.SourceID.Validate() != nil || v.SetID.Validate() != nil || v.SourceRevision == 0 || v.SourceRevision >= 1<<63 || !PositiveDecimal(v.RemoteRepositoryID) || !PositiveDecimal(v.PullRequestID) || !PositiveDecimal(v.Number) || ValidateGitHubRepository(v.Owner, v.Name) != nil || v.Actor.Validate() != nil || len(v.Problems) == 0 || len(v.Problems) > MaxPRFeedback+MaxCIContexts+1 {
		return invalidPRProblem()
	}
	seen := map[ID]bool{}
	for _, p := range v.Problems {
		if p.ID.Validate() != nil || !lowerDigest(p.ContentVersion) || seen[p.ID] {
			return invalidPRProblem()
		}
		seen[p.ID] = true
	}
	if v.Action == PRActivityVerifiedHandled {
		if v.VerificationID.Validate() != nil || v.SourceID != v.VerificationID || v.AttemptState != "" || v.Mode != "" || v.Outcome != "" || v.ExecutionID != "" {
			return invalidPRProblem()
		}
		return nil
	}
	if v.VerificationID != "" {
		return invalidPRProblem()
	}
	if v.Action == PRActivityObserved || v.Action == PRActivityDismissed {
		if len(v.Problems) != 1 || v.Problems[0].ID != v.SourceID || v.AttemptState != "" || v.Mode != "" || v.Outcome != "" || v.ExecutionID != "" {
			return invalidPRProblem()
		}
		return nil
	}
	if v.Action != PRActivityAttempt || !v.Mode.Valid() {
		return invalidPRProblem()
	}
	switch v.AttemptState {
	case PRRemediationReserved, PRRemediationBound, PRRemediationCanceled:
		if v.ExecutionID != "" || v.Outcome != "" {
			return invalidPRProblem()
		}
	case PRRemediationRunning, PRRemediationUncertain:
		if v.ExecutionID.Validate() != nil || v.Outcome != "" {
			return invalidPRProblem()
		}
	case PRRemediationFinished:
		if v.ExecutionID.Validate() != nil || (v.Outcome != ExecutionSucceeded && v.Outcome != ExecutionFailed && v.Outcome != ExecutionStopped && v.Outcome != ExecutionNotStarted) {
			return invalidPRProblem()
		}
	default:
		return invalidPRProblem()
	}
	return nil
}

const PRHandlingVerificationRecord PRProblemRecordType = "pull-request-handling-verification"

// Only an independently verified original handling result may create this
// immutable source. There is deliberately no public write or inferred-success
// adapter: production creation belongs to the separate remediation verifier.
// A digest commits to that verifier's proof without copying provider bodies.
type PRHandlingVerification struct {
	Version     uint32                    `json:"version"`
	Type        PRProblemRecordType       `json:"type"`
	SetID       ID                        `json:"set_id"`
	Problems    []PRRemediationProblemRef `json:"problems"`
	ProofDigest string                    `json:"proof_digest"`
	Actor       PRProblemDismissal        `json:"actor"`
}

func (v PRHandlingVerification) Validate() error {
	if v.Version != 1 || v.Type != PRHandlingVerificationRecord || v.SetID.Validate() != nil || !lowerDigest(v.ProofDigest) || v.Actor.Validate() != nil || len(v.Problems) == 0 || len(v.Problems) > MaxPRFeedback+MaxCIContexts+1 {
		return invalidPRProblem()
	}
	seen := map[ID]bool{}
	for _, ref := range v.Problems {
		if ref.ID.Validate() != nil || !lowerDigest(ref.ContentVersion) || seen[ref.ID] {
			return invalidPRProblem()
		}
		seen[ref.ID] = true
	}
	return nil
}
