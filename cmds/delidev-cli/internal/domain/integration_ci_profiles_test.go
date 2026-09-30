// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
)

func TestALLGREENCannotBorrowPinnedWorkflowTestMergeProof(t *testing.T) {
	for _, available := range []bool{false, true} {
		item, ci := ciFixture()
		item.NodeID, item.Number = "PR_17", "17"
		ci = queueCI(item, ci)
		_, workflow := pinnedWorkflowCI()
		ci.TestMerge = workflow.TestMerge
		if available {
			ci.WorkflowRuns = workflow.WorkflowRuns
		}
		ci.Rules.Rules = append(ci.Rules.Rules, workflow.Rules.Rules[0], ActiveRepositoryRule{Type: "merge_queue", RulesetID: "20", SourceKind: RulesetRepository, NativeSourceKind: "Repository", Source: "fixture", Digest: strings.Repeat("e", 64)})
		ci.Rules.Digest = ActiveRulesDigest(ci.Rules.Rules)
		ci.Result = ci.Evaluate(item)
		if ci.Validate(item) != nil || ci.Result.Source != CIMergeQueueCommit || ci.Result.EvaluatedSHA != ci.MergeQueue.Entry.HeadSHA || ci.Result.State != CIUnknown || ci.Result.Reason != CIWorkflowUnverified || len(ci.Result.Requirements) != 2 {
			t.Fatal("queue and workflow profiles lost their original commit authority", available, ci.Result, ci.Validate(item))
		}
		if ci.Result.Requirements[0].State != CITerminalFailure || ci.Result.Requirements[1].State != CIUnknown || len(ci.Result.Requirements[1].ResultNodeIDs) != 0 {
			t.Fatal("test-merge workflow proof replaced independent queue evidence", available, ci.Result)
		}
	}
}
