package domain

import "time"

const MaxPRRemediationAttempts = 10000

const PRRemediationAttemptRecord PRProblemRecordType = "pull-request-remediation-attempt"

type PRRemediationAttemptState string

const (
	PRRemediationReserved  PRRemediationAttemptState = "reserved"
	PRRemediationBound     PRRemediationAttemptState = "bound"
	PRRemediationRunning   PRRemediationAttemptState = "running"
	PRRemediationUncertain PRRemediationAttemptState = "uncertain"
	PRRemediationFinished  PRRemediationAttemptState = "finished"
	PRRemediationCanceled  PRRemediationAttemptState = "canceled"
)

func (s PRRemediationAttemptState) Active() bool {
	return s == PRRemediationReserved || s == PRRemediationBound || s == PRRemediationRunning || s == PRRemediationUncertain
}

type PRRemediationLimit struct {
	Limit        uint32    `json:"limit"`
	Attempts     uint32    `json:"attempts"`
	PolicyDigest string    `json:"policy_digest"`
	At           time.Time `json:"at"`
}

// One durable chain belongs to the stable remote PR. Local aliases, replacement
// sessions, new heads and changed problem versions never create a fresh budget.
// Explicit resumption advances a baseline while preserving lifetime counters.
type PRRemediationChain struct {
	ID                ID                  `json:"id"`
	Sequence          uint32              `json:"sequence"`
	AutomaticAttempts uint32              `json:"automatic_attempts"`
	ResumeBaseline    uint32              `json:"resume_baseline"`
	ActiveAttemptID   ID                  `json:"active_attempt_id,omitempty"`
	Limit             *PRRemediationLimit `json:"limit,omitempty"`
	LastResume        *PRProblemDismissal `json:"last_resume,omitempty"`
}

func (v PRRemediationChain) Validate() error {
	if v.ID.Validate() != nil || v.Sequence > MaxPRRemediationAttempts || v.AutomaticAttempts > v.Sequence || v.ResumeBaseline > v.AutomaticAttempts || (v.ActiveAttemptID != "" && v.ActiveAttemptID.Validate() != nil) {
		return invalidPRRemediation()
	}
	if v.ResumeBaseline > 0 && v.LastResume == nil {
		return invalidPRRemediation()
	}
	if v.LastResume != nil && v.LastResume.Validate() != nil {
		return invalidPRRemediation()
	}
	if v.Limit != nil {
		l := v.Limit
		if l.Limit < 1 || l.Limit > 100 || l.Attempts < l.Limit || l.Attempts != v.AutomaticAttempts-v.ResumeBaseline || !lowerDigest(l.PolicyDigest) || !ciEvidenceTime(l.At) {
			return invalidPRRemediation()
		}
	}
	return nil
}

func (v PRRemediationChain) CanStartAutomatic(policy RemediationPolicy) bool {
	return v.Validate() == nil && policy.Validate() == nil && v.AutomaticAttempts-v.ResumeBaseline < policy.AttemptLimit
}

type PRRemediationProblemRef struct {
	ID             ID     `json:"id"`
	ContentVersion string `json:"content_version"`
}

type PRRemediationAttempt struct {
	Version               uint32                    `json:"version"`
	Type                  PRProblemRecordType       `json:"type"`
	SetID                 ID                        `json:"set_id"`
	ChainID               ID                        `json:"chain_id"`
	Sequence              uint32                    `json:"sequence"`
	Mode                  PRRemediationMode         `json:"mode"`
	State                 PRRemediationAttemptState `json:"state"`
	Policy                RemediationPolicy         `json:"policy"`
	Problems              []PRRemediationProblemRef `json:"problems"`
	Reserved              PRProblemDismissal        `json:"reserved"`
	SessionID             ID                        `json:"session_id,omitempty"`
	InputID               ID                        `json:"input_id,omitempty"`
	InputDigest           string                    `json:"input_digest,omitempty"`
	ExecutionID           ID                        `json:"execution_id,omitempty"`
	StartedAt             *time.Time                `json:"started_at,omitempty"`
	FinishedAt            *time.Time                `json:"finished_at,omitempty"`
	Outcome               ExecutionOutcome          `json:"outcome,omitempty"`
	GitTarget             *PRGitTarget              `json:"git_target,omitempty"`
	ProjectID             ID                        `json:"project_id,omitempty"`
	StartupRejectionJobID ID                        `json:"startup_rejection_job_id,omitempty"`
	AutomaticLinkID       ID                        `json:"automatic_link_id,omitempty"`
	AutomaticLinkRevision uint64                    `json:"automatic_link_revision,omitempty,string"`
}

func (v PRRemediationAttempt) Validate() error {
	if (v.AutomaticLinkID == "") != (v.AutomaticLinkRevision == 0) || v.AutomaticLinkID != "" && (v.Mode != PRRemediationAutomatic || v.AutomaticLinkID.Validate() != nil || v.GitTarget == nil || v.AutomaticLinkRevision >= 1<<63) {
		return invalidPRRemediation()
	}
	if (v.GitTarget == nil) != (v.ProjectID == "") || (v.GitTarget != nil && (v.GitTarget.Validate() != nil || v.ProjectID.Validate() != nil)) {
		return invalidPRRemediation()
	}
	if v.StartupRejectionJobID != "" && (v.State != PRRemediationFinished || v.Outcome != ExecutionNotStarted || v.StartupRejectionJobID.Validate() != nil) {
		return invalidPRRemediation()
	}
	if v.Version != 1 || v.Type != PRRemediationAttemptRecord || v.SetID.Validate() != nil || v.ChainID.Validate() != nil || v.Sequence < 1 || v.Sequence > MaxPRRemediationAttempts || !v.Mode.Valid() || v.Policy.Validate() != nil || v.Reserved.Validate() != nil || len(v.Problems) == 0 || len(v.Problems) > MaxPRFeedback+MaxCIContexts+1 {
		return invalidPRRemediation()
	}
	seen := map[ID]bool{}
	for _, p := range v.Problems {
		if p.ID.Validate() != nil || !lowerDigest(p.ContentVersion) || seen[p.ID] {
			return invalidPRRemediation()
		}
		seen[p.ID] = true
	}
	bound := v.SessionID != "" && v.InputID != ""
	if (v.SessionID == "") != (v.InputID == "") || (bound && (v.SessionID.Validate() != nil || v.InputID.Validate() != nil)) {
		return invalidPRRemediation()
	}
	if (bound && !lowerDigest(v.InputDigest)) || (!bound && v.InputDigest != "") {
		return invalidPRRemediation()
	}
	started := v.ExecutionID != "" && v.StartedAt != nil
	if (v.ExecutionID == "") != (v.StartedAt == nil) || (started && (!bound || v.ExecutionID.Validate() != nil || !ciEvidenceTime(*v.StartedAt) || v.StartedAt.Before(v.Reserved.At))) {
		return invalidPRRemediation()
	}
	if v.FinishedAt != nil && (!ciEvidenceTime(*v.FinishedAt) || v.FinishedAt.Before(v.Reserved.At) || (started && v.FinishedAt.Before(*v.StartedAt))) {
		return invalidPRRemediation()
	}
	switch v.State {
	case PRRemediationReserved:
		if bound || started || v.FinishedAt != nil || v.Outcome != "" {
			return invalidPRRemediation()
		}
	case PRRemediationBound:
		if !bound || started || v.FinishedAt != nil || v.Outcome != "" {
			return invalidPRRemediation()
		}
	case PRRemediationRunning, PRRemediationUncertain:
		if !started || v.FinishedAt != nil || v.Outcome != "" {
			return invalidPRRemediation()
		}
	case PRRemediationFinished:
		rejected := v.Outcome == ExecutionNotStarted && v.StartupRejectionJobID != ""
		if !started || v.FinishedAt == nil || (!rejected && v.Outcome != ExecutionSucceeded && v.Outcome != ExecutionFailed && v.Outcome != ExecutionStopped) {
			return invalidPRRemediation()
		}
	case PRRemediationCanceled:
		if started || v.FinishedAt == nil || v.Outcome != "" {
			return invalidPRRemediation()
		}
	default:
		return invalidPRRemediation()
	}
	return nil
}

// Validate is shared by version-specific dismissal and explicit chain actions.
// Its type retains the v18 JSON shape so earlier evidence stays byte-stable.
func (v PRProblemDismissal) Validate() error {
	if v.RequestID.Validate() != nil || !ciEvidenceTime(v.At) {
		return invalidPRProblem()
	}
	if v.ActorType == ClientDevice || v.ActorType == WorkerDevice {
		if v.DeviceID.Validate() != nil {
			return invalidPRProblem()
		}
	} else if v.ActorType != OwnerDevice || v.DeviceID != "" {
		return invalidPRProblem()
	}
	return nil
}
