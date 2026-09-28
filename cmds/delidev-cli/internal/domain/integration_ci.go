package domain

import (
	"reflect"
	"strconv"
)

const MaxCIContexts = 500

type CIContextKind string

const (
	CICheckRun     CIContextKind = "check-run"
	CICommitStatus CIContextKind = "commit-status"
)

type CIContext struct {
	Kind             CIContextKind     `json:"kind"`
	NodeID           string            `json:"node_id"`
	Name             string            `json:"name"`
	CommitSHA        string            `json:"commit_sha"`
	Required         bool              `json:"required"`
	NativeStatus     string            `json:"native_status"`
	NativeConclusion *string           `json:"native_conclusion,omitempty"`
	Application      *CheckApplication `json:"application,omitempty"`
	WorkflowEvent    *string           `json:"workflow_event,omitempty"`
}

func (v CIContext) Validate(sha string) error {
	if v.CommitSHA != sha || Text(v.NodeID, "CI node identity", 256, true) != nil || Text(v.Name, "CI context", 1024, true) != nil || Text(v.NativeStatus, "CI status", 64, true) != nil {
		return invalidPRObservation()
	}
	switch v.Kind {
	case CICheckRun:
		if v.NativeConclusion != nil && Text(*v.NativeConclusion, "CI conclusion", 64, true) != nil {
			return invalidPRObservation()
		}
		if v.WorkflowEvent != nil && Text(*v.WorkflowEvent, "workflow event", 100, true) != nil {
			return invalidPRObservation()
		}
		if a := v.Application; a != nil && (!PositiveDecimal(a.ID) || Text(a.NodeID, "App identity", 256, true) != nil || Text(a.Slug, "App slug", 100, true) != nil) {
			return invalidPRObservation()
		}
	case CICommitStatus:
		if v.NativeConclusion != nil || v.Application != nil || v.WorkflowEvent != nil {
			return invalidPRObservation()
		}
	default:
		return invalidPRObservation()
	}
	return nil
}

type CIRollup struct {
	TotalCount string      `json:"total_count"`
	CommitSHA  string      `json:"commit_sha"`
	Contexts   []CIContext `json:"contexts"`
}

func (v CIRollup) Validate(sha string) error {
	if !repositorySHA(sha) || v.CommitSHA != sha || v.Contexts == nil || len(v.Contexts) > MaxCIContexts || v.TotalCount != strconv.Itoa(len(v.Contexts)) {
		return invalidPRObservation()
	}
	seen := map[string]bool{}
	for _, x := range v.Contexts {
		if x.Validate(sha) != nil || seen[x.NodeID] {
			return invalidPRObservation()
		}
		seen[x.NodeID] = true
	}
	return nil
}

type EvaluatedCommitSource string

const (
	CIHeadCommit      EvaluatedCommitSource = "head"
	CITestMergeCommit EvaluatedCommitSource = "test-merge"
	CIUnknownCommit   EvaluatedCommitSource = "unknown"
)

type CIState string

const (
	CIUnknown         CIState = "unknown"
	CIMissing         CIState = "missing"
	CIPending         CIState = "pending"
	CINonFailing      CIState = "non-failing"
	CITerminalFailure CIState = "terminal-failure"
	CINotRequired     CIState = "not-required"
)

type CIReason string

const (
	CIObserved            CIReason = "observed"
	CINoMatchingResult    CIReason = "no-matching-result"
	CIAppUnverified       CIReason = "app-unverified"
	CIUnknownNativeResult CIReason = "unknown-native-result"
	CIWorkflowUnverified  CIReason = "workflow-unverified"
	CIUnsupportedRule     CIReason = "unsupported-rule"
	CICommitUnverified    CIReason = "commit-unverified"
	CIClosedPR            CIReason = "closed-pr"
)

type CIRequirementResult struct {
	RulesetID     string   `json:"ruleset_id"`
	Context       string   `json:"context"`
	IntegrationID *string  `json:"integration_id,omitempty"`
	State         CIState  `json:"state"`
	Reason        CIReason `json:"reason"`
	ResultNodeIDs []string `json:"result_node_ids"`
}
type RequiredCIResult struct {
	Source       EvaluatedCommitSource `json:"source"`
	EvaluatedSHA string                `json:"evaluated_sha,omitempty"`
	State        CIState               `json:"state"`
	Reason       CIReason              `json:"reason"`
	Requirements []CIRequirementResult `json:"requirements"`
}
type PullRequestCI struct {
	Rules              PullRequestRules `json:"rules"`
	Head               CIRollup         `json:"head"`
	TestMerge          *CIRollup        `json:"test_merge,omitempty"`
	NativeMergeability string           `json:"native_mergeability"`
	InMergeQueue       bool             `json:"in_merge_queue"`
	Result             RequiredCIResult `json:"result"`
}

func ciResultState(v CIContext) CIState {
	if v.Kind == CICommitStatus {
		switch v.NativeStatus {
		case "ERROR", "FAILURE":
			return CITerminalFailure
		case "PENDING", "EXPECTED":
			return CIPending
		case "SUCCESS":
			return CINonFailing
		default:
			return CIUnknown
		}
	}
	if v.NativeStatus != "COMPLETED" {
		switch v.NativeStatus {
		case "QUEUED", "IN_PROGRESS", "WAITING", "PENDING", "REQUESTED":
			if v.NativeConclusion == nil {
				return CIPending
			}
		}
		return CIUnknown
	}
	if v.NativeConclusion == nil {
		return CIUnknown
	}
	switch *v.NativeConclusion {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return CINonFailing
	case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STALE", "STARTUP_FAILURE":
		return CITerminalFailure
	default:
		return CIUnknown
	}
}
func workflowEvaluated(v CIContext) bool {
	if v.WorkflowEvent == nil {
		return v.Application == nil || v.Application.ID != "15368"
	}
	switch *v.WorkflowEvent {
	case "push", "pull_request", "pull_request_review", "pull_request_target", "deployment", "deployment_status":
		return true
	}
	return false
}
func ciRequirement(rule ActiveRepositoryRule, check RequiredRuleCheck, contexts []CIContext) CIRequirementResult {
	result := CIRequirementResult{RulesetID: rule.RulesetID, Context: check.Context, IntegrationID: check.IntegrationID, State: CIMissing, Reason: CINoMatchingResult, ResultNodeIDs: []string{}}
	if check.IntegrationID != nil && *check.IntegrationID == "0" {
		result.State, result.Reason = CIUnknown, CIAppUnverified
		return result
	}
	checkSources := map[string]int{}
	for _, v := range contexts {
		if v.Name != check.Context || !v.Required {
			continue
		}
		state, reason := ciResultState(v), CIObserved
		if v.Kind == CICheckRun {
			if v.Application == nil {
				state, reason = CIUnknown, CIAppUnverified
			} else if v.Application.ID == "15368" && v.WorkflowEvent == nil {
				state, reason = CIUnknown, CIWorkflowUnverified
			} else if !workflowEvaluated(v) {
				continue
			}
		}
		if check.IntegrationID != nil {
			if v.Application == nil {
				state, reason = CIUnknown, CIAppUnverified
			} else if v.Application.ID != *check.IntegrationID {
				continue
			}
		}
		if state == CIUnknown && reason == CIObserved {
			reason = CIUnknownNativeResult
		}
		if v.Kind == CICheckRun && v.Application != nil {
			checkSources[v.Application.ID]++
			if checkSources[v.Application.ID] > 1 {
				state, reason = CIUnknown, CIUnknownNativeResult
			}
		}
		result.ResultNodeIDs = append(result.ResultNodeIDs, v.NodeID)
		// Unknown matching provenance blocks this kind, even if another result
		// failed. Both Checks and commit statuses with a required name matter.
		if result.State == CIMissing || ciPriority(state) > ciPriority(result.State) {
			result.State, result.Reason = state, reason
		}
	}
	return result
}
func ciPriority(state CIState) int {
	switch state {
	case CIUnknown:
		return 5
	case CITerminalFailure:
		return 4
	case CIMissing:
		return 3
	case CIPending:
		return 2
	case CINonFailing:
		return 1
	}
	return 0
}
func (v PullRequestCI) Evaluate(item RepositoryItem) RequiredCIResult {
	result := RequiredCIResult{Source: CIUnknownCommit, State: CIUnknown, Reason: CICommitUnverified, Requirements: []CIRequirementResult{}}
	if item.State != RepositoryItemOpen || item.Merged == nil || *item.Merged {
		result.Reason = CIClosedPR
		return result
	}
	if v.InMergeQueue || v.NativeMergeability == "UNKNOWN" || v.NativeMergeability == "MERGEABLE" && v.TestMerge == nil {
		return result
	}
	contexts := v.Head.Contexts
	result.Source, result.EvaluatedSHA = CIHeadCommit, v.Head.CommitSHA
	if v.TestMerge != nil && len(v.TestMerge.Contexts) > 0 {
		result.Source, result.EvaluatedSHA, contexts = CITestMergeCommit, v.TestMerge.CommitSHA, v.TestMerge.Contexts
	}
	result.State, result.Reason = CINotRequired, CIObserved
	for _, rule := range v.Rules.Rules {
		if rule.SourceKind == RulesetUnknownSource {
			result.State, result.Reason = CIUnknown, CIUnsupportedRule
		}
		switch rule.Type {
		case "required_status_checks":
			if rule.RequiredChecks == nil {
				result.State, result.Reason = CIUnknown, CIUnsupportedRule
				continue
			}
			if rule.RequiredChecks.UnknownParameters {
				result.State, result.Reason = CIUnknown, CIUnsupportedRule
			}
			for _, check := range rule.RequiredChecks.Checks {
				required := ciRequirement(rule, check, contexts)
				result.Requirements = append(result.Requirements, required)
				if ciPriority(required.State) > ciPriority(result.State) {
					result.State, result.Reason = required.State, required.Reason
				}
			}
		case "creation", "update", "deletion", "required_linear_history", "required_deployments", "required_signatures", "pull_request", "non_fast_forward", "commit_message_pattern", "commit_author_email_pattern", "committer_email_pattern", "branch_name_pattern", "tag_name_pattern", "file_path_restriction", "max_file_path_length", "file_extension_restriction", "max_file_size":
		default:
			result.State, result.Reason = CIUnknown, CIUnsupportedRule
		}
	}
	return result
}

func (v PullRequestCI) Validate(item RepositoryItem) error {
	if v.Rules.Validate(item) != nil || v.Head.Validate(item.HeadSHA) != nil || v.TestMerge != nil && (v.TestMerge.Validate(v.TestMerge.CommitSHA) != nil || v.TestMerge.CommitSHA == item.HeadSHA) {
		return invalidPRObservation()
	}
	switch v.NativeMergeability {
	case "MERGEABLE", "CONFLICTING", "UNKNOWN":
	default:
		return invalidPRObservation()
	}
	if v.NativeMergeability == "CONFLICTING" && v.TestMerge != nil {
		return invalidPRObservation()
	}
	if item.Mergeable != nil && (v.NativeMergeability == "UNKNOWN" || *item.Mergeable != (v.NativeMergeability == "MERGEABLE")) {
		return invalidPRObservation()
	}
	if v.TestMerge != nil {
		seen := map[string]bool{}
		for _, row := range v.Head.Contexts {
			seen[row.NodeID] = true
		}
		for _, row := range v.TestMerge.Contexts {
			if seen[row.NodeID] {
				return invalidPRObservation()
			}
		}
	}
	if !reflect.DeepEqual(v.Result, v.Evaluate(item)) {
		return invalidPRObservation()
	}
	// Recompute the complete classification; callers cannot supply a different
	// terminal result, required identity or evaluated commit.
	return nil
}
