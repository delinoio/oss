package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"
)

const MaxCIEvidenceBytes = 512 << 10

type CIWorkflowEvidence struct {
	SuiteNodeID     string    `json:"suite_node_id,omitempty"`
	CommitSHA       string    `json:"commit_sha,omitempty"`
	NodeID          string    `json:"node_id"`
	RunNumber       string    `json:"run_number"`
	ObservedAttempt string    `json:"observed_attempt"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type CIContextEvidence struct {
	SuiteNodeID string              `json:"suite_node_id,omitempty"`
	StartedAt   *time.Time          `json:"started_at,omitempty"`
	CompletedAt *time.Time          `json:"completed_at,omitempty"`
	Workflow    *CIWorkflowEvidence `json:"workflow,omitempty"`
	CreatedAt   *time.Time          `json:"created_at,omitempty"`
	UpdatedAt   *time.Time          `json:"updated_at,omitempty"`
	Title       *string             `json:"title,omitempty"`
	Summary     *string             `json:"summary,omitempty"`
	Text        *string             `json:"text,omitempty"`
	Description *string             `json:"description,omitempty"`
}

func ciWorkflowCount(v string) bool {
	n, err := strconv.ParseUint(v, 10, 31)
	return err == nil && n > 0 && PositiveDecimal(v)
}

func ciEvidenceTime(v time.Time) bool { return !v.IsZero() && v.Year() >= 1970 && v.Year() <= 9999 }

func (v CIContextEvidence) validate(kind CIContextKind, event *string) error {
	for _, at := range []*time.Time{v.StartedAt, v.CompletedAt, v.CreatedAt, v.UpdatedAt} {
		if at != nil && !ciEvidenceTime(*at) {
			return invalidPRObservation()
		}
	}
	for _, output := range []*string{v.Title, v.Summary, v.Text, v.Description} {
		if output != nil && Text(*output, "CI original output", 64<<10, false) != nil {
			return invalidPRObservation()
		}
	}
	switch kind {
	case CICheckRun:
		if Text(v.SuiteNodeID, "CI suite identity", 256, true) != nil || v.CreatedAt != nil || v.UpdatedAt != nil || v.Description != nil || (v.Workflow == nil) != (event == nil) {
			return invalidPRObservation()
		}
		if v.StartedAt != nil && v.CompletedAt != nil && v.CompletedAt.Before(*v.StartedAt) {
			return invalidPRObservation()
		}
		if w := v.Workflow; w != nil {
			if (w.SuiteNodeID == "") != (w.CommitSHA == "") || w.SuiteNodeID != "" && (w.SuiteNodeID != v.SuiteNodeID || !repositorySHA(w.CommitSHA)) {
				return invalidPRObservation()
			}
			if Text(w.NodeID, "workflow identity", 256, true) != nil || !ciWorkflowCount(w.RunNumber) || !ciWorkflowCount(w.ObservedAttempt) || !ciEvidenceTime(w.CreatedAt) || !ciEvidenceTime(w.UpdatedAt) || w.UpdatedAt.Before(w.CreatedAt) {
				return invalidPRObservation()
			}
		}
	case CICommitStatus:
		if v.SuiteNodeID != "" || v.StartedAt != nil || v.CompletedAt != nil || v.Workflow != nil || v.Title != nil || v.Summary != nil || v.Text != nil || v.CreatedAt == nil || v.UpdatedAt == nil || v.UpdatedAt.Before(*v.CreatedAt) {
			return invalidPRObservation()
		}
	default:
		return invalidPRObservation()
	}
	return nil
}

// Version binds this original result's identity, lifecycle and output. Required
// flags and rule changes are applicability, not a new result. GitHub's workflow
// runAttempt describes the current workflow run, including a partial rerun; it
// is not proof that every retained CheckRun ran in that attempt. Exclude that
// aggregate and its updatedAt from result versions until per-job attempt
// attribution is independently available.
func (v CIContext) Version() string {
	if v.Validate(v.CommitSHA) != nil {
		return ""
	}
	evidence := *v.Evidence
	evidence.Workflow = nil
	var application *CheckApplication
	if v.Application != nil {
		application = &CheckApplication{ID: v.Application.ID, NodeID: v.Application.NodeID}
	}
	raw, _ := json.Marshal(struct {
		Kind                            CIContextKind
		NodeID, Name, CommitSHA, Status string
		Conclusion                      *string
		Application                     *CheckApplication
		Evidence                        CIContextEvidence
	}{v.Kind, v.NodeID, v.Name, v.CommitSHA, v.NativeStatus, v.NativeConclusion, application, evidence})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
