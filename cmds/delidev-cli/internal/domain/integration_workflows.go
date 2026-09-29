package domain

import "reflect"

// Workflow jobs independently attribute original CheckRun nodes to one attempt.
// An aggregate runAttempt alone cannot authorize reuse of an older result.
type CIWorkflowJob struct {
	NodeID           string  `json:"node_id"`
	NativeStatus     string  `json:"native_status"`
	NativeConclusion *string `json:"native_conclusion,omitempty"`
}

type CIRequiredWorkflowRun struct {
	NodeID           string                     `json:"node_id"`
	RunID            string                     `json:"run_id"`
	SuiteNodeID      string                     `json:"suite_node_id"`
	CommitSHA        string                     `json:"commit_sha"`
	Event            string                     `json:"event"`
	Attempt          string                     `json:"attempt"`
	NativeStatus     string                     `json:"native_status"`
	NativeConclusion *string                    `json:"native_conclusion,omitempty"`
	Source           *RequiredWorkflowReference `json:"source,omitempty"`
	SourceFileNodeID string                     `json:"source_file_node_id,omitempty"`
	SourceBlobSHA    string                     `json:"source_blob_sha,omitempty"`
	Jobs             []CIWorkflowJob            `json:"jobs"`
}

func (v CIRequiredWorkflowRun) Validate(sha string) error {
	if v.Source != nil && (Text(v.SourceFileNodeID, "source file identity", 256, true) != nil || !repositorySHA(v.SourceBlobSHA)) || v.Source == nil && (v.SourceFileNodeID != "" || v.SourceBlobSHA != "") {
		return invalidPRObservation()
	}
	if !repositorySHA(sha) || v.CommitSHA != sha || !PositiveDecimal(v.RunID) || !ciWorkflowCount(v.Attempt) || Text(v.NodeID, "workflow run", 256, true) != nil || Text(v.SuiteNodeID, "workflow suite", 256, true) != nil || Text(v.Event, "workflow event", 100, true) != nil || Text(v.NativeStatus, "workflow status", 64, true) != nil || v.NativeConclusion != nil && Text(*v.NativeConclusion, "workflow conclusion", 64, true) != nil || v.Source != nil && (v.Source.Validate() != nil || v.Source.SHA == nil || v.Source.Ref != nil) || v.Jobs == nil || len(v.Jobs) > MaxCIContexts {
		return invalidPRObservation()
	}
	seen := map[string]bool{}
	for _, job := range v.Jobs {
		if Text(job.NodeID, "workflow job", 256, true) != nil || seen[job.NodeID] || Text(job.NativeStatus, "job status", 64, true) != nil || job.NativeConclusion != nil && Text(*job.NativeConclusion, "job conclusion", 64, true) != nil {
			return invalidPRObservation()
		}
		seen[job.NodeID] = true
	}
	return nil
}

func workflowRequirement(rule ActiveRepositoryRule, ref RequiredWorkflowReference, ci PullRequestCI, source EvaluatedCommitSource) CIRequirementResult {
	r := CIRequirementResult{RulesetID: rule.RulesetID, Context: ref.Path, Workflow: &ref, State: CIUnknown, Reason: CIWorkflowUnverified, ResultNodeIDs: []string{}}
	if ref.SHA == nil || rule.RequiredWorkflows.UnknownParameters || ci.WorkflowRuns == nil || source != CITestMergeCommit || ci.TestMerge == nil {
		return r
	}
	var candidates []CIRequiredWorkflowRun
	for _, run := range ci.WorkflowRuns {
		// An inaccessible original source cannot be dismissed as unrelated.
		if run.Source == nil {
			return r
		}
		if run.Source.RepositoryID == ref.RepositoryID && run.Source.Path == ref.Path {
			candidates = append(candidates, run)
		}
	}
	if len(candidates) != 1 {
		return r
	}
	run := candidates[0]
	if run.Source.SHA == nil || *run.Source.SHA != *ref.SHA || run.Event != "pull_request" || run.CommitSHA != ci.TestMerge.CommitSHA || len(run.Jobs) == 0 {
		return r
	}
	contexts := map[string]CIContext{}
	for _, ctx := range ci.TestMerge.Contexts {
		contexts[ctx.NodeID] = ctx
	}
	state := CINonFailing
	pending := false
	for _, job := range run.Jobs {
		ctx, exists := contexts[job.NodeID]
		if !exists || !ctx.Required || ctx.Kind != CICheckRun || ctx.Application == nil || ctx.Application.ID != "15368" || ctx.Evidence == nil || ctx.Evidence.SuiteNodeID != run.SuiteNodeID || ctx.Evidence.Workflow == nil || ctx.Evidence.Workflow.NodeID != run.NodeID || ctx.Evidence.Workflow.ObservedAttempt != run.Attempt || ctx.WorkflowEvent == nil || *ctx.WorkflowEvent != run.Event || ctx.NativeStatus != job.NativeStatus || !reflect.DeepEqual(ctx.NativeConclusion, job.NativeConclusion) {
			return r
		}
		current := ciResultState(ctx)
		pending = pending || current == CIPending
		if ciPriority(current) > ciPriority(state) {
			state = current
		}
		r.ResultNodeIDs = append(r.ResultNodeIDs, ctx.NodeID)
	}
	// The original current run's lifecycle and its independently attributed jobs
	// must agree. Never turn an older retained failure into a new attempt failure.
	current := ciResultState(CIContext{Kind: CICheckRun, NativeStatus: run.NativeStatus, NativeConclusion: run.NativeConclusion})
	if current == CIPending && state != CIUnknown {
		r.State, r.Reason = CIPending, CIObserved
		return r
	}
	if current != state || pending {
		r.ResultNodeIDs = []string{}
		return r
	}
	r.State, r.Reason = state, CIObserved
	if state == CIUnknown {
		r.Reason = CIUnknownNativeResult
	}
	return r
}
