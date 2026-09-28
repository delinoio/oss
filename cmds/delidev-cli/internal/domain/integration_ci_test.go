package domain

import (
	"strings"
	"testing"
)

func ciFixture() (RepositoryItem, PullRequestCI) {
	no := false
	item := RepositoryItem{State: RepositoryItemOpen, Merged: &no, BaseRef: "main", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	id := "15368"
	conclusion := "FAILURE"
	event := "pull_request"
	rules := []ActiveRepositoryRule{{Type: "required_status_checks", RulesetID: "37", SourceKind: RulesetRepository, NativeSourceKind: "Repository", Source: "fixture-owner/repo", Digest: strings.Repeat("d", 64), RequiredChecks: &RequiredRuleChecks{Checks: []RequiredRuleCheck{{Context: "CI Result", IntegrationID: &id}}}}}
	v := PullRequestCI{NativeMergeability: "MERGEABLE", Rules: PullRequestRules{BaseRef: item.BaseRef, BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, Rules: rules, Digest: ActiveRulesDigest(rules)}, Head: CIRollup{CommitSHA: item.HeadSHA, TotalCount: "1", Contexts: []CIContext{{Kind: CICheckRun, NodeID: "CHECK_1", Name: "CI Result", CommitSHA: item.HeadSHA, Required: true, NativeStatus: "COMPLETED", NativeConclusion: &conclusion, Application: &CheckApplication{ID: id, NodeID: "APP_15368", Slug: "github-actions"}, WorkflowEvent: &event}}}, TestMerge: &CIRollup{CommitSHA: strings.Repeat("c", 40), TotalCount: "0", Contexts: []CIContext{}}}
	v.Result = v.Evaluate(item)
	return item, v
}
func TestRequiredCITerminalResultsExcludePassingPendingOptionalAndOtherApp(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*PullRequestCI)
		want   CIState
	}{
		{"required failure", func(*PullRequestCI) {}, CITerminalFailure},
		{"success", func(v *PullRequestCI) { x := "SUCCESS"; v.Head.Contexts[0].NativeConclusion = &x }, CINonFailing},
		{"neutral", func(v *PullRequestCI) { x := "NEUTRAL"; v.Head.Contexts[0].NativeConclusion = &x }, CINonFailing},
		{"skipped", func(v *PullRequestCI) { x := "SKIPPED"; v.Head.Contexts[0].NativeConclusion = &x }, CINonFailing},
		{"running", func(v *PullRequestCI) {
			v.Head.Contexts[0].NativeStatus = "IN_PROGRESS"
			v.Head.Contexts[0].NativeConclusion = nil
		}, CIPending},
		{"optional", func(v *PullRequestCI) { v.Head.Contexts[0].Required = false }, CIMissing},
		{"different app", func(v *PullRequestCI) { v.Head.Contexts[0].Application.ID = "99" }, CIMissing},
		{"no app", func(v *PullRequestCI) { v.Head.Contexts[0].Application = nil }, CIUnknown},
		{"missing workflow evidence", func(v *PullRequestCI) { v.Head.Contexts[0].WorkflowEvent = nil }, CIUnknown},
		{"manual workflow", func(v *PullRequestCI) { x := "workflow_dispatch"; v.Head.Contexts[0].WorkflowEvent = &x }, CIMissing},
		{"future conclusion", func(v *PullRequestCI) { x := "FUTURE"; v.Head.Contexts[0].NativeConclusion = &x }, CIUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item, v := ciFixture()
			tc.mutate(&v)
			v.Result = v.Evaluate(item)
			if v.Result.State != tc.want || v.Validate(item) != nil {
				t.Fatal("wrong CI classification", v.Result)
			}
		})
	}
}
func TestRequiredCIUsesTestMergeAndNeverHeadWhenMergeIsUnknown(t *testing.T) {
	item, v := ciFixture()
	run := v.Head.Contexts[0]
	run.NodeID = "MERGE_CHECK"
	run.CommitSHA = v.TestMerge.CommitSHA
	pass := "SUCCESS"
	run.NativeConclusion = &pass
	v.TestMerge.Contexts = []CIContext{run}
	v.TestMerge.TotalCount = "1"
	v.Result = v.Evaluate(item)
	if v.Result.Source != CITestMergeCommit || v.Result.EvaluatedSHA != v.TestMerge.CommitSHA || v.Result.State != CINonFailing {
		t.Fatal("failed head substituted for evaluated merge", v.Result)
	}
	v.TestMerge = nil
	v.NativeMergeability = "UNKNOWN"
	v.Result = v.Evaluate(item)
	if v.Result.Source != CIUnknownCommit || v.Result.State != CIUnknown {
		t.Fatal("unknown commit fell back to head")
	}
	item, v = ciFixture()
	v.InMergeQueue = true
	v.Result = v.Evaluate(item)
	if v.Result.Source != CIUnknownCommit {
		t.Fatal("merge queue borrowed ordinary head evidence")
	}
}
func TestRequiredCIKeepsClassicOnlyOptionalAndAppBoundStatusUnknown(t *testing.T) {
	item, v := ciFixture()
	v.Rules.Rules = []ActiveRepositoryRule{}
	v.Rules.Digest = ActiveRulesDigest(v.Rules.Rules)
	v.Result = v.Evaluate(item)
	if v.Result.State != CINotRequired || len(v.Result.Requirements) != 0 {
		t.Fatal("classic requirement triggered ruleset CI")
	}
	item, v = ciFixture()
	v.Head.Contexts = append(v.Head.Contexts, CIContext{Kind: CICommitStatus, NodeID: "STATUS_1", Name: "CI Result", CommitSHA: item.HeadSHA, Required: true, NativeStatus: "ERROR"})
	v.Head.TotalCount = "2"
	v.Result = v.Evaluate(item)
	if v.Result.State != CIUnknown || v.Result.Reason != CIAppUnverified {
		t.Fatal("App-less commit status authorized bound requirement")
	}
	v.Rules.Rules[0].RequiredChecks.Checks[0].IntegrationID = nil
	v.Rules.Digest = ActiveRulesDigest(v.Rules.Rules)
	x := "SUCCESS"
	v.Head.Contexts[0].NativeConclusion = &x
	v.Result = v.Evaluate(item)
	if v.Result.State != CITerminalFailure || len(v.Result.Requirements[0].ResultNodeIDs) != 2 {
		t.Fatal("same-name passing check hid failed commit status")
	}
}
func TestRequiredCIRejectsForgedAssessmentAndPartialInventory(t *testing.T) {
	item, v := ciFixture()
	v.Result.State = CINonFailing
	if v.Validate(item) == nil {
		t.Fatal("forged result accepted")
	}
	item, v = ciFixture()
	v.Head.TotalCount = "2"
	v.Result = v.Evaluate(item)
	if v.Validate(item) == nil {
		t.Fatal("partial inventory accepted")
	}
	item, v = ciFixture()
	item.State = RepositoryItemClosed
	v.Result = v.Evaluate(item)
	if v.Result.State != CIUnknown || v.Result.Reason != CIClosedPR {
		t.Fatal("closed PR supplied automatic failure")
	}
}

func TestRequiredCIUnknownRulesAndAmbiguousRunsCannotTrigger(t *testing.T) {
	item, v := ciFixture()
	v.Rules.Rules[0].RequiredChecks.UnknownParameters = true
	v.Rules.Digest = ActiveRulesDigest(v.Rules.Rules)
	v.Result = v.Evaluate(item)
	if v.Result.State != CIUnknown || v.Result.Reason != CIUnsupportedRule || v.Validate(item) != nil {
		t.Fatal("future rule parameters authorized CI", v.Result)
	}
	item, v = ciFixture()
	duplicate := v.Head.Contexts[0]
	duplicate.NodeID = "OTHER_RUN"
	v.Head.Contexts = append(v.Head.Contexts, duplicate)
	v.Head.TotalCount = "2"
	v.Result = v.Evaluate(item)
	if v.Result.State != CIUnknown || v.Validate(item) != nil {
		t.Fatal("ambiguous same-name runs selected by guess", v.Result)
	}
}
