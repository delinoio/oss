package domain

import (
	"strings"
	"testing"
)

func queueCI(item RepositoryItem, v PullRequestCI) PullRequestCI {
	entry := CIMergeQueueEntry{NodeID: "ENTRY_E", PullRequestNodeID: item.NodeID, PullRequestNumber: item.Number, Position: "1", BaseSHA: item.BaseSHA, HeadSHA: strings.Repeat("f", 40), State: "UNMERGEABLE"}
	run := v.Head.Contexts[0]
	run.NodeID, run.CommitSHA = "QUEUE_CHECK", entry.HeadSHA
	event := "merge_group"
	run.WorkflowEvent, run.Evidence = &event, ciCheckEvidence()
	run.Evidence.Workflow.SuiteNodeID, run.Evidence.Workflow.CommitSHA = run.Evidence.SuiteNodeID, entry.HeadSHA
	v.InMergeQueue = true
	v.MergeQueue = &CIMergeQueue{NodeID: "QUEUE_Q", RepositoryNodeID: "R_37", Strategy: CIQueueAllGreen, Entry: entry, TotalCount: "1", Entries: []CIMergeQueueEntry{entry}, Rollup: &CIRollup{CommitSHA: entry.HeadSHA, TotalCount: "1", Contexts: []CIContext{run}}}
	v.Result = v.Evaluate(item)
	return v
}

func TestALLGREENRequiredCISelectsOnlyOriginalEntryCommit(t *testing.T) {
	for _, mode := range []string{"failure", "success", "pending", "HEADGREEN", "no-config", "no-entry", "no-commit", "PR-head", "old-commit", "wrong-app", "wrong-event", "missing-workflow", "wrong-workflow-suite", "wrong-workflow-commit", "optional", "no-failed-check", "external-app", "status-with-app", "status-without-app", "unknown-rule"} {
		t.Run(mode, func(t *testing.T) {
			item, v := ciFixture()
			item.NodeID, item.Number = "PR_17", "17"
			v = queueCI(item, v)
			want := CITerminalFailure
			run := &v.MergeQueue.Rollup.Contexts[0]
			switch mode {
			case "success":
				x := "SUCCESS"
				run.NativeConclusion = &x
				want = CINonFailing
			case "pending":
				run.NativeStatus, run.NativeConclusion = "IN_PROGRESS", nil
				want = CIPending
			case "HEADGREEN":
				v.MergeQueue.Strategy = CIQueueHeadGreen
				want = CIUnknown
			case "no-config":
				v.MergeQueue.Strategy = CIQueueUnknownStrategy
				want = CIUnknown
			case "no-entry":
				v.MergeQueue = nil
				want = CIUnknown
			case "no-commit":
				v.MergeQueue.Entry.HeadSHA = ""
				v.MergeQueue.Entries[0] = v.MergeQueue.Entry
				v.MergeQueue.Rollup = nil
				want = CIUnknown
			case "PR-head", "old-commit", "no-failed-check":
				v.MergeQueue.Rollup.Contexts, v.MergeQueue.Rollup.TotalCount = []CIContext{}, "0"
				want = CIUnknown
			case "wrong-app":
				run.Application = &CheckApplication{ID: "99", NodeID: "APP_99", Slug: "fixture"}
				want = CIUnknown
			case "wrong-event":
				x := "pull_request"
				run.WorkflowEvent = &x
				want = CIUnknown
			case "missing-workflow":
				run.WorkflowEvent, run.Evidence.Workflow = nil, nil
				want = CIUnknown
			case "wrong-workflow-suite":
				run.Evidence.Workflow.SuiteNodeID = ""
				run.Evidence.Workflow.CommitSHA = ""
				want = CIUnknown
			case "wrong-workflow-commit":
				run.Evidence.Workflow.SuiteNodeID = ""
				run.Evidence.Workflow.CommitSHA = ""
				want = CIUnknown
			case "optional":
				run.Required = false
				want = CIUnknown
			case "external-app":
				id := "99"
				run.Application = &CheckApplication{ID: id, NodeID: "APP_99", Slug: "fixture"}
				run.WorkflowEvent, run.Evidence.Workflow = nil, nil
				v.Rules.Rules[0].RequiredChecks.Checks[0].IntegrationID = &id
			case "status-with-app", "status-without-app":
				*run = CIContext{Kind: CICommitStatus, NodeID: "STATUS_G", Name: run.Name, CommitSHA: run.CommitSHA, Required: true, NativeStatus: "FAILURE", Evidence: &CIContextEvidence{CreatedAt: ciCheckEvidence().StartedAt, UpdatedAt: ciCheckEvidence().CompletedAt}}
				if mode == "status-without-app" {
					v.Rules.Rules[0].RequiredChecks.Checks[0].IntegrationID = nil
				} else {
					want = CIUnknown
				}
			case "unknown-rule":
				v.Rules.Rules = append(v.Rules.Rules, ActiveRepositoryRule{Type: "workflows", RulesetID: "38", SourceKind: RulesetRepository, NativeSourceKind: "Repository", Source: "fixture-owner/repo", Digest: strings.Repeat("d", 64)})
				want = CIUnknown
			}
			v.Rules.Digest = ActiveRulesDigest(v.Rules.Rules)
			v.Result = v.Evaluate(item)
			if v.Result.State != want || v.Validate(item) != nil {
				t.Fatal(mode, v.Result, v.Validate(item))
			}
			if want == CITerminalFailure && (v.Result.Source != CIMergeQueueCommit || v.Result.EvaluatedSHA != v.MergeQueue.Entry.HeadSHA) {
				t.Fatal("queue failure attributed to ordinary head")
			}
		})
	}
}

func TestALLGREENRejectsForgedMembershipAndWorkflowBinding(t *testing.T) {
	for _, mode := range []string{"foreign-pr", "missing-entry", "partial-entries", "reordered", "forged-workflow"} {
		t.Run(mode, func(t *testing.T) {
			item, ci := ciFixture()
			item.NodeID, item.Number = "PR_17", "17"
			ci = queueCI(item, ci)
			switch mode {
			case "foreign-pr":
				ci.MergeQueue.Entry.PullRequestNodeID = "FOREIGN"
			case "missing-entry":
				ci.MergeQueue.Entries = []CIMergeQueueEntry{}
				ci.MergeQueue.TotalCount = "0"
			case "partial-entries":
				ci.MergeQueue.TotalCount = "2"
			case "reordered":
				ci.MergeQueue.Entries[0].Position = "2"
			case "forged-workflow":
				ci.MergeQueue.Rollup.Contexts[0].Evidence.Workflow.CommitSHA = item.HeadSHA
			}
			ci.Result = ci.Evaluate(item)
			if ci.Validate(item) == nil {
				t.Fatal("mixed queue proof accepted")
			}
		})
	}
}

func TestHistoricalQueueFailureCannotAuthorizeRemovedOrReplacementEntry(t *testing.T) {
	p, o, policy := remediationDomainProblem(t, PRCIProblem)
	o.CI = func() *PullRequestCI {
		ci := queueCI(o.Items[0], *o.CI)
		ci.MergeQueue.RepositoryNodeID = o.Repository.NodeID
		return &ci
	}()
	proof, err := NewPRCIObservation(p.SetID, o)
	if err != nil {
		t.Fatal(err)
	}
	contexts, err := proof.FailureContexts()
	if err != nil || len(contexts) != 1 {
		t.Fatal(contexts, err)
	}
	p.CI = &PRCIProblemEvidence{ObservationID: NewID(), Context: contexts[0], Source: CIMergeQueueCommit, RulesDigest: proof.CI.Rules.Digest, QueueNodeID: proof.CI.MergeQueue.NodeID, QueueEntryNodeID: proof.CI.MergeQueue.Entry.NodeID}
	p.ContentVersion = contexts[0].Version()
	if !p.CI.Matches(proof) || proof.Summary().Validate() != nil {
		t.Fatal("queue proof not retained")
	}
	if got, err := EvaluatePRRemediation(p, o, policy, PRRemediationAutomatic, o.ObservedAt); err != nil || got != PRRemediationEligible {
		t.Fatal(got, err)
	}
	o.CI.MergeQueue.Entry.NodeID = "REPLACEMENT_E"
	o.CI.MergeQueue.Entries[0] = o.CI.MergeQueue.Entry
	o.CI.Result = o.CI.Evaluate(o.Items[0])
	if got, err := EvaluatePRRemediation(p, o, policy, PRRemediationAutomatic, o.ObservedAt); err != nil || got != PRRemediationNoCIFailure {
		t.Fatal("replacement entry inherited historical failure", got, err)
	}
	o.CI.InMergeQueue, o.CI.MergeQueue = false, nil
	o.CI.Result = o.CI.Evaluate(o.Items[0])
	if got, err := EvaluatePRRemediation(p, o, policy, PRRemediationAutomatic, o.ObservedAt); err != nil || got != PRRemediationNoCIFailure {
		t.Fatal("removed entry inherited head failure", got, err)
	}
	if !p.CI.Matches(proof) {
		t.Fatal("historical proof was erased")
	}
}
