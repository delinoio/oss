package domain

import (
	"strings"
	"testing"
)

func ciProblemObservationFixture() RepositoryQueryResult {
	value := conflictObservationFixture()
	value.Query.Operation = RepositoryCI
	_, ci := ciFixture()
	yes := true
	value.Items[0].Mergeable = &yes
	value.CI = &ci
	return value
}

func TestRetainedCIProofSelectsOnlyRequiredEvaluatedFailures(t *testing.T) {
	input := ciProblemObservationFixture()
	optional := input.CI.Head.Contexts[0]
	optional.NodeID = "OPTIONAL"
	optional.Name = "Optional failure"
	optional.Required = false
	input.CI.Head.Contexts = append(input.CI.Head.Contexts, optional)
	input.CI.Head.TotalCount = "2"
	duplicateRule := input.CI.Rules.Rules[0]
	duplicateRule.RulesetID = "38"
	input.CI.Rules.Rules = append(input.CI.Rules.Rules, duplicateRule)
	input.CI.Rules.Digest = ActiveRulesDigest(input.CI.Rules.Rules)
	input.CI.Result = input.CI.Evaluate(input.Items[0])
	proof, err := NewPRCIObservation(NewID(), input)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := proof.FailureContexts()
	if err != nil || len(entries) != 1 || entries[0].NodeID != "CHECK_1" {
		t.Fatal("optional/duplicate failure selected", entries, err)
	}
	evidence := PRCIProblemEvidence{ObservationID: NewID(), Context: entries[0], Source: proof.CI.Result.Source, RulesDigest: proof.CI.Rules.Digest}
	if !evidence.Matches(proof) {
		t.Fatal("original failure did not match")
	}
	// Changed caller storage cannot rewrite the copied complete proof.
	input.CI.Head.Contexts[0].Application.ID = "99"
	if !evidence.Matches(proof) {
		t.Fatal("proof aliased a later provider response")
	}
	evidence.RulesDigest = strings.Repeat("e", 64)
	if evidence.Matches(proof) {
		t.Fatal("foreign rule digest accepted")
	}
}

func TestRetainedCIProofNeverUsesOldHeadOrUnknownRulesAsFailureAuthority(t *testing.T) {
	for _, mode := range []string{"test-merge-success", "unknown-rules", "unknown-commit", "pending", "not-required"} {
		t.Run(mode, func(t *testing.T) {
			input := ciProblemObservationFixture()
			switch mode {
			case "test-merge-success":
				run := input.CI.Head.Contexts[0]
				run.NodeID = "MERGE_CHECK"
				run.CommitSHA = input.CI.TestMerge.CommitSHA
				pass := "SUCCESS"
				run.NativeConclusion = &pass
				input.CI.TestMerge.Contexts = []CIContext{run}
				input.CI.TestMerge.TotalCount = "1"
			case "unknown-rules":
				rule := input.CI.Rules.Rules[0]
				rule.Type = "workflows"
				rule.RequiredChecks = nil
				input.CI.Rules.Rules = append(input.CI.Rules.Rules, rule)
				input.CI.Rules.Digest = ActiveRulesDigest(input.CI.Rules.Rules)
			case "unknown-commit":
				input.CI.InMergeQueue = true
			case "pending":
				input.CI.Head.Contexts[0].NativeStatus = "IN_PROGRESS"
				input.CI.Head.Contexts[0].NativeConclusion = nil
			case "not-required":
				input.CI.Rules.Rules = []ActiveRepositoryRule{}
				input.CI.Rules.Digest = ActiveRulesDigest(nil)
			}
			input.CI.Result = input.CI.Evaluate(input.Items[0])
			proof, err := NewPRCIObservation(NewID(), input)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := proof.FailureContexts()
			if err != nil || len(entries) != 0 {
				t.Fatal("unknown/optional/old head became a durable failure", err)
			}
		})
	}
}

func TestRetainedCIProofRejectsForgedClassificationAndKeepsOriginalOutput(t *testing.T) {
	input := ciProblemObservationFixture()
	original := "Original native output"
	input.CI.Head.Contexts[0].Evidence.Text = &original
	proof, err := NewPRCIObservation(NewID(), input)
	if err != nil {
		t.Fatal(err)
	}
	digest := proof.Digest()
	if digest == "" || *proof.CI.Head.Contexts[0].Evidence.Text != original {
		t.Fatal("lost original output")
	}
	replaced := "Changed native output"
	proof.CI.Head.Contexts[0].Evidence.Text = &replaced
	if proof.Digest() == digest {
		t.Fatal("proof did not bind output")
	}
	proof.CI.Result.Requirements[0].ResultNodeIDs = []string{"FOREIGN"}
	if proof.Validate() == nil {
		t.Fatal("forged failure references accepted")
	}
	if _, err := PRTargetFromObservation(RepositoryQueryResult{}); err == nil {
		t.Fatal("missing PR target accepted")
	}
}
