package domain

import (
	"strings"
	"testing"
)

func pinnedWorkflowCI() (RepositoryItem, PullRequestCI) {
	item, ci := ciFixture()
	sha := strings.Repeat("d", 40)
	ref := RequiredWorkflowReference{RepositoryID: "11", Path: ".github/workflows/required.yml", SHA: &sha}
	ci.Rules.Rules = []ActiveRepositoryRule{{Type: "workflows", RulesetID: "19", NativeSourceKind: "Organization", SourceKind: RulesetOrganization, Source: "fixture", Digest: strings.Repeat("e", 64), RequiredWorkflows: &RequiredRuleWorkflows{Workflows: []RequiredWorkflowReference{ref}}}}
	ci.Rules.Digest = ActiveRulesDigest(ci.Rules.Rules)
	check := ci.Head.Contexts[0]
	check.CommitSHA = strings.Repeat("c", 40)
	check.NodeID = "CHECK_merge"
	check.Evidence = ciCheckEvidence()
	check.Evidence.SuiteNodeID = "SUITE_merge"
	check.Evidence.Workflow.NodeID = "RUN_merge"
	ci.TestMerge = &CIRollup{CommitSHA: check.CommitSHA, TotalCount: "1", Contexts: []CIContext{check}}
	ci.WorkflowRuns = []CIRequiredWorkflowRun{{NodeID: check.Evidence.Workflow.NodeID, RunID: "91", SuiteNodeID: check.Evidence.SuiteNodeID, CommitSHA: check.CommitSHA, Event: "pull_request", Attempt: check.Evidence.Workflow.ObservedAttempt, NativeStatus: check.NativeStatus, NativeConclusion: check.NativeConclusion, Source: &ref, SourceFileNodeID: "FILE_1", SourceBlobSHA: strings.Repeat("f", 40), Jobs: []CIWorkflowJob{{NodeID: check.NodeID, NativeStatus: check.NativeStatus, NativeConclusion: check.NativeConclusion}}}}
	ci.Result = ci.Evaluate(item)
	return item, ci
}

func TestPinnedRequiredWorkflowEvaluatesOnlyOriginalCurrentAttempt(t *testing.T) {
	item, ci := pinnedWorkflowCI()
	if ci.Result.State != CITerminalFailure || ci.Result.Source != CITestMergeCommit || ci.Validate(item) != nil {
		t.Fatal("pinned original failure not proved", ci.Result, ci.Validate(item))
	}
	for _, state := range []CIState{CINonFailing, CIPending} {
		item, ci := pinnedWorkflowCI()
		ctx := &ci.TestMerge.Contexts[0]
		if state == CINonFailing {
			conclusion := "SUCCESS"
			ctx.NativeConclusion = &conclusion
		} else {
			ctx.NativeStatus = "IN_PROGRESS"
			ctx.NativeConclusion = nil
		}
		run := &ci.WorkflowRuns[0]
		run.NativeStatus, run.NativeConclusion = ctx.NativeStatus, ctx.NativeConclusion
		run.Jobs[0].NativeStatus, run.Jobs[0].NativeConclusion = ctx.NativeStatus, ctx.NativeConclusion
		ci.Result = ci.Evaluate(item)
		if ci.Result.State != state || ci.Validate(item) != nil {
			t.Fatal("current outcome not retained", state, ci.Result, ci.Validate(item))
		}
	}
}

func TestPinnedWorkflowRejectsAmbiguousSourceCommitEventAndAttempts(t *testing.T) {
	for _, mode := range []string{"missing-sha", "wrong-repository", "wrong-path", "wrong-sha", "display-name", "reusable", "head", "base", "old-merge", "target", "queue-event", "in-queue", "missing-required", "source-inaccessible", "partial-inventory", "competing-run", "newer-running-attempt", "stale-job", "unrecognized-parameters"} {
		t.Run(mode, func(t *testing.T) {
			item, ci := pinnedWorkflowCI()
			run := &ci.WorkflowRuns[0]
			switch mode {
			case "missing-sha":
				ci.Rules.Rules[0].RequiredWorkflows.Workflows[0].SHA = nil
			case "wrong-repository":
				run.Source.RepositoryID = "12"
			case "wrong-path":
				run.Source.Path = ".github/workflows/other.yml"
			case "wrong-sha":
				sha := strings.Repeat("f", 40)
				run.Source.SHA = &sha
			case "display-name":
				ci.TestMerge.Contexts[0].Name = run.Source.Path
				run.Source = nil
			case "reusable":
				run.Source.Path = ".github/workflows/reusable.yml"
			case "head":
				run.CommitSHA = item.HeadSHA
			case "base":
				run.CommitSHA = item.BaseSHA
			case "old-merge":
				run.CommitSHA = strings.Repeat("f", 40)
			case "target":
				run.Event = "pull_request_target"
			case "queue-event":
				run.Event = "merge_group"
			case "in-queue":
				ci.InMergeQueue = true
			case "missing-required":
				ci.TestMerge.Contexts[0].Required = false
			case "source-inaccessible":
				run.Source = nil
			case "partial-inventory":
				ci.WorkflowRuns = nil
			case "competing-run":
				other := *run
				other.NodeID = "RUN_competing"
				other.SuiteNodeID = "SUITE_competing"
				other.Attempt = "2"
				other.NativeStatus = "IN_PROGRESS"
				other.NativeConclusion = nil
				ci.WorkflowRuns = append(ci.WorkflowRuns, other)
			case "newer-running-attempt":
				run.Attempt = "2"
				run.NativeStatus = "IN_PROGRESS"
				run.NativeConclusion = nil
			case "stale-job":
				run.Jobs[0].NodeID = "OLD_JOB"
			case "unrecognized-parameters":
				ci.Rules.Rules[0].RequiredWorkflows.UnknownParameters = true
			}
			if result := ci.Evaluate(item); result.State != CIUnknown {
				t.Fatal("unproved workflow accepted", mode, result)
			}
		})
	}
}

func TestWorkflowProofCannotForgeResultOrInvalidateHistoricalStatusChecks(t *testing.T) {
	item, ci := pinnedWorkflowCI()
	ci.WorkflowRuns[0].Jobs[0].NativeStatus = "IN_PROGRESS"
	if ci.Validate(item) == nil {
		t.Fatal("forged terminal classification accepted")
	}
	item, ci = ciFixture()
	ci.Result = ci.Evaluate(item)
	if ci.Validate(item) != nil || ci.Result.State != CITerminalFailure {
		t.Fatal("historical status-check proof changed")
	}
}

func TestRunningWorkflowCannotPublishTerminalFailureFromCurrentJobs(t *testing.T) {
	item, ci := pinnedWorkflowCI()
	other := ci.TestMerge.Contexts[0]
	other.NodeID, other.NativeStatus, other.NativeConclusion = "CHECK_running", "IN_PROGRESS", nil
	other.Evidence = ciCheckEvidence()
	other.Evidence.SuiteNodeID = "SUITE_merge"
	other.Evidence.Workflow.NodeID = "RUN_merge"
	other.Evidence.CompletedAt = nil
	ci.TestMerge.Contexts = append(ci.TestMerge.Contexts, other)
	ci.TestMerge.TotalCount = "2"
	ci.WorkflowRuns[0].Jobs = append(ci.WorkflowRuns[0].Jobs, CIWorkflowJob{NodeID: other.NodeID, NativeStatus: other.NativeStatus})
	ci.WorkflowRuns[0].NativeStatus, ci.WorkflowRuns[0].NativeConclusion = "IN_PROGRESS", nil
	ci.Result = ci.Evaluate(item)
	if ci.Result.State != CIPending || ci.Validate(item) != nil {
		t.Fatal("unfinished workflow became a failure", ci.Result, ci.Validate(item))
	}
	conclusion := "FAILURE"
	ci.WorkflowRuns[0].NativeStatus, ci.WorkflowRuns[0].NativeConclusion = "COMPLETED", &conclusion
	ci.Result = ci.Evaluate(item)
	if ci.Result.State != CIUnknown {
		t.Fatal("contradictory terminal workflow used unfinished jobs", ci.Result)
	}
}
