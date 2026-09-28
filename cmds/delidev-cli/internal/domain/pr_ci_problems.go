package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

const PRCIObservationRecord PRProblemRecordType = "pull-request-ci-observation"

// One original complete CI observation is shared by all failed result versions
// first captured in that collection. Later reads must not overwrite its rules,
// evaluated operands, lifecycle or provider output.
type PRCIObservation struct {
	Version          uint32               `json:"version"`
	Type             PRProblemRecordType  `json:"type"`
	SetID            ID                   `json:"set_id"`
	Target           SessionPullRequest   `json:"target"`
	Observation      PRProblemObservation `json:"observation"`
	BaseRef          string               `json:"base_ref"`
	HeadRef          string               `json:"head_ref"`
	PullRequestState RepositoryItemState  `json:"pull_request_state"`
	Merged           bool                 `json:"merged"`
	Mergeable        *bool                `json:"mergeable"`
	CI               PullRequestCI        `json:"ci"`
}

func (v PRCIObservation) Validate() error {
	if v.Version != 1 || v.Type != PRCIObservationRecord || v.SetID.Validate() != nil || v.Target.Validate() != nil || v.Observation.Validate() != nil || Text(v.BaseRef, "CI base ref", 1024, true) != nil || Text(v.HeadRef, "CI head ref", 1024, true) != nil || (v.PullRequestState != RepositoryItemOpen && v.PullRequestState != RepositoryItemClosed) {
		return invalidPRProblem()
	}
	item := RepositoryItem{State: v.PullRequestState, Merged: &v.Merged, Mergeable: v.Mergeable, BaseRef: v.BaseRef, HeadRef: v.HeadRef, BaseSHA: v.Observation.BaseSHA, HeadSHA: v.Observation.HeadSHA}
	if v.CI.Validate(item) != nil {
		return invalidPRProblem()
	}
	return nil
}

func (v PRCIObservation) Digest() string {
	if v.Validate() != nil {
		return ""
	}
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func PRTargetFromObservation(observed RepositoryQueryResult) (SessionPullRequest, error) {
	if observed.Validate() != nil || observed.Query.Kind != RepositoryPullRequest || len(observed.Items) != 1 || observed.Items[0].IdentitySource != RepositoryPullRequestIdentity {
		return SessionPullRequest{}, invalidPRProblem()
	}
	item := observed.Items[0]
	value := SessionPullRequest{Version: 1, Provider: observed.Repository.Provider, RepositoryID: observed.RepositoryID, RemoteRepositoryID: observed.Repository.ID, RepositoryNodeID: observed.Repository.NodeID, Owner: observed.Repository.Owner, Name: observed.Repository.Name, PullRequestID: item.ID, PullRequestNodeID: item.NodeID, Number: item.Number, Title: item.Title, ObservedAt: observed.ObservedAt}
	return value, value.Validate()
}

func NewPRCIObservation(set ID, observed RepositoryQueryResult) (PRCIObservation, error) {
	if set.Validate() != nil || observed.Validate() != nil || observed.Query.Operation != RepositoryCI || observed.CI == nil {
		return PRCIObservation{}, invalidPRProblem()
	}
	target, err := PRTargetFromObservation(observed)
	if err != nil {
		return PRCIObservation{}, err
	}
	item := observed.Items[0]
	value := PRCIObservation{Version: 1, Type: PRCIObservationRecord, SetID: set, Target: target, Observation: PRProblemObservation{BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, ObservedAt: observed.ObservedAt}, BaseRef: item.BaseRef, HeadRef: item.HeadRef, PullRequestState: item.State, Merged: *item.Merged, Mergeable: item.Mergeable, CI: *observed.CI}
	// Isolate all provider slices and nullable allocations before a retained
	// observation can be reused as immutable evidence by multiple problems.
	raw, err := json.Marshal(value)
	if err != nil {
		return PRCIObservation{}, invalidPRProblem()
	}
	var cloned PRCIObservation
	if Decode(raw, &cloned) != nil {
		return PRCIObservation{}, invalidPRProblem()
	}
	return cloned, cloned.Validate()
}

// Only the independently recomputed overall CI classification may select
// problems. Unknown rules/commit provenance cannot be bypassed by a failed
// optional result or by a caller-supplied requirement's result-node list.
func (v PRCIObservation) FailureContexts() ([]CIContext, error) {
	if err := v.Validate(); err != nil {
		return nil, err
	}
	result := []CIContext{}
	if v.CI.Result.State != CITerminalFailure {
		return result, nil
	}
	contexts := v.CI.Head.Contexts
	if v.CI.Result.Source == CITestMergeCommit {
		if v.CI.TestMerge == nil {
			return nil, invalidPRProblem()
		}
		contexts = v.CI.TestMerge.Contexts
	}
	eligible := map[string]bool{}
	for _, requirement := range v.CI.Result.Requirements {
		if requirement.State != CITerminalFailure {
			continue
		}
		for _, node := range requirement.ResultNodeIDs {
			eligible[node] = true
		}
	}
	for _, entry := range contexts {
		if eligible[entry.NodeID] && entry.Required && ciResultState(entry) == CITerminalFailure {
			result = append(result, entry)
		}
	}
	if len(result) == 0 {
		return nil, invalidPRProblem()
	}
	return result, nil
}

type PRCIProblemEvidence struct {
	ObservationID ID                    `json:"observation_id"`
	Context       CIContext             `json:"context"`
	Source        EvaluatedCommitSource `json:"source"`
	RulesDigest   string                `json:"rules_digest"`
}

func (v PRCIProblemEvidence) Validate() error {
	if v.ObservationID.Validate() != nil || v.Context.Validate(v.Context.CommitSHA) != nil || !v.Context.Required || ciResultState(v.Context) != CITerminalFailure || (v.Source != CIHeadCommit && v.Source != CITestMergeCommit) || !lowerDigest(v.RulesDigest) {
		return invalidPRProblem()
	}
	return nil
}

func (v PRCIProblemEvidence) Matches(proof PRCIObservation) bool {
	if v.Validate() != nil || v.Source != proof.CI.Result.Source || v.RulesDigest != proof.CI.Rules.Digest {
		return false
	}
	entries, err := proof.FailureContexts()
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if reflect.DeepEqual(entry, v.Context) {
			return true
		}
	}
	return false
}

// The latest CI observation is separate from immutable failed-result proofs.
// A currently unknown or non-failing evaluation cannot erase original evidence.
type PRCIObservationSummary struct {
	Observation  PRProblemObservation  `json:"observation"`
	State        CIState               `json:"state"`
	Reason       CIReason              `json:"reason"`
	Source       EvaluatedCommitSource `json:"source"`
	EvaluatedSHA string                `json:"evaluated_sha,omitempty"`
	RulesDigest  string                `json:"rules_digest"`
}

func (v PRCIObservationSummary) Validate() error {
	if v.Observation.Validate() != nil || !lowerDigest(v.RulesDigest) {
		return invalidPRProblem()
	}
	switch v.State {
	case CIUnknown, CIMissing, CIPending, CINonFailing, CITerminalFailure, CINotRequired:
	default:
		return invalidPRProblem()
	}
	switch v.Reason {
	case CIObserved, CINoMatchingResult, CIAppUnverified, CIUnknownNativeResult, CIWorkflowUnverified, CIUnsupportedRule, CICommitUnverified, CIClosedPR:
	default:
		return invalidPRProblem()
	}
	switch v.Source {
	case CIUnknownCommit:
		if v.EvaluatedSHA != "" || v.State != CIUnknown {
			return invalidPRProblem()
		}
	case CIHeadCommit:
		if v.EvaluatedSHA != v.Observation.HeadSHA {
			return invalidPRProblem()
		}
	case CITestMergeCommit:
		if !repositorySHA(v.EvaluatedSHA) || v.EvaluatedSHA == v.Observation.HeadSHA {
			return invalidPRProblem()
		}
	default:
		return invalidPRProblem()
	}
	return nil
}

func (v PRCIObservation) Summary() PRCIObservationSummary {
	return PRCIObservationSummary{Observation: v.Observation, State: v.CI.Result.State, Reason: v.CI.Result.Reason, Source: v.CI.Result.Source, EvaluatedSHA: v.CI.Result.EvaluatedSHA, RulesDigest: v.CI.Rules.Digest}
}
