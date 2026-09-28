package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type PRConflictState string

const (
	PRConflictUnknown       PRConflictState = "unknown"
	PRConflictPresent       PRConflictState = "conflicting"
	PRConflictAbsent        PRConflictState = "mergeable"
	PRConflictNotApplicable PRConflictState = "not-applicable"
)

// The transition identity survives unknown observations and repeated reads of
// the same refs/commits. A verified resolution or a different conflict scope
// permits a new identity; merely restarting a collector does not.
type PRConflictSnapshot struct {
	TransitionID ID                   `json:"transition_id"`
	BaseRef      string               `json:"base_ref"`
	HeadRef      string               `json:"head_ref"`
	Observation  PRProblemObservation `json:"observation"`
}

func (v PRConflictSnapshot) Validate() error {
	if v.TransitionID.Validate() != nil || Text(v.BaseRef, "conflict base ref", 1024, true) != nil || Text(v.HeadRef, "conflict head ref", 1024, true) != nil || v.Observation.Validate() != nil {
		return invalidPRProblem()
	}
	return nil
}

func (v PRConflictSnapshot) ContentVersion() string {
	if v.Validate() != nil {
		return ""
	}
	raw, _ := json.Marshal(struct {
		Transition                         ID
		BaseRef, HeadRef, BaseSHA, HeadSHA string
	}{v.TransitionID, v.BaseRef, v.HeadRef, v.Observation.BaseSHA, v.Observation.HeadSHA})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type PRConflictObservation struct {
	Observation      PRProblemObservation `json:"observation"`
	BaseRef          string               `json:"base_ref"`
	HeadRef          string               `json:"head_ref"`
	PullRequestState RepositoryItemState  `json:"pull_request_state"`
	Merged           bool                 `json:"merged"`
	Mergeable        *bool                `json:"mergeable"`
	State            PRConflictState      `json:"state"`
	// An unknown observation retains the last unresolved transition for dedup,
	// but never exposes that old transition as a currently verified conflict.
	Active *PRConflictSnapshot `json:"active,omitempty"`
}

func classifyPRConflict(state RepositoryItemState, merged bool, mergeable *bool) PRConflictState {
	if state == RepositoryItemClosed || merged {
		return PRConflictNotApplicable
	}
	if mergeable == nil {
		return PRConflictUnknown
	}
	if *mergeable {
		return PRConflictAbsent
	}
	return PRConflictPresent
}

func (v PRConflictObservation) Validate() error {
	if v.Observation.Validate() != nil || Text(v.BaseRef, "conflict base ref", 1024, true) != nil || Text(v.HeadRef, "conflict head ref", 1024, true) != nil || (v.PullRequestState != RepositoryItemOpen && v.PullRequestState != RepositoryItemClosed) || v.State != classifyPRConflict(v.PullRequestState, v.Merged, v.Mergeable) {
		return invalidPRProblem()
	}
	if v.Active != nil && v.Active.Validate() != nil {
		return invalidPRProblem()
	}
	switch v.State {
	case PRConflictPresent:
		if v.Active == nil || v.Active.BaseRef != v.BaseRef || v.Active.HeadRef != v.HeadRef || v.Active.Observation.BaseSHA != v.Observation.BaseSHA || v.Active.Observation.HeadSHA != v.Observation.HeadSHA {
			return invalidPRProblem()
		}
	case PRConflictAbsent, PRConflictNotApplicable:
		if v.Active != nil {
			return invalidPRProblem()
		}
	case PRConflictUnknown:
	default:
		return invalidPRProblem()
	}
	return nil
}

// This reducer requires a complete PR detail observation, independently of CI
// and rules access. Its caller must bind the previous record's stable PR and
// revision before committing the result; the reducer grants no execution.
func ObservePRConflict(previous *PRConflictObservation, observed RepositoryQueryResult) (PRConflictObservation, error) {
	if observed.Validate() != nil || observed.Query.Kind != RepositoryPullRequest || observed.Query.Operation != RepositoryDetail || previous != nil && previous.Validate() != nil {
		return PRConflictObservation{}, invalidPRProblem()
	}
	item := observed.Items[0]
	value := PRConflictObservation{Observation: PRProblemObservation{BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, ObservedAt: observed.ObservedAt}, BaseRef: item.BaseRef, HeadRef: item.HeadRef, PullRequestState: item.State, Merged: *item.Merged, Mergeable: item.Mergeable}
	value.State = classifyPRConflict(value.PullRequestState, value.Merged, value.Mergeable)
	if previous != nil && previous.Active != nil {
		copy := *previous.Active
		value.Active = &copy
	}
	switch value.State {
	case PRConflictAbsent, PRConflictNotApplicable:
		value.Active = nil
	case PRConflictPresent:
		if value.Active == nil || value.Active.BaseRef != value.BaseRef || value.Active.HeadRef != value.HeadRef || value.Active.Observation.BaseSHA != value.Observation.BaseSHA || value.Active.Observation.HeadSHA != value.Observation.HeadSHA {
			value.Active = &PRConflictSnapshot{TransitionID: NewID(), BaseRef: value.BaseRef, HeadRef: value.HeadRef, Observation: value.Observation}
		}
	}
	// Copy a native nullable scalar so a caller cannot mutate accepted evidence
	// by reusing the response allocation after this reduction.
	if value.Mergeable != nil {
		mergeable := *value.Mergeable
		value.Mergeable = &mergeable
	}
	return value, value.Validate()
}
