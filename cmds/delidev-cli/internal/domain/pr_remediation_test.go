package domain

import (
	"strings"
	"testing"
	"time"
)

func remediationDomainProblem(t *testing.T, kind PRProblemKind) (PRProblem, RepositoryQueryResult, RemediationPolicy) {
	t.Helper()
	observed := conflictObservationFixture()
	policy := DefaultRemediationPolicy()
	policy.CIFailure, policy.ReviewFeedback, policy.MergeConflict = true, true, true
	target, _ := PRTargetFromObservation(observed)
	p := PRProblem{Version: 1, Type: PRProblemEvidenceRecord, SetID: NewID(), Kind: kind, Target: target, Observation: PRProblemObservation{BaseSHA: observed.Items[0].BaseSHA, HeadSHA: observed.Items[0].HeadSHA, ObservedAt: observed.ObservedAt}, Current: true, State: PRProblemUnhandled}
	switch kind {
	case PRFeedbackProblem:
		_, reviewers := reviewerDomainFixture()
		observed.Query.Operation, observed.Reviewers = RepositoryReviewers, &reviewers
		entry := reviewers.Feedback.Entries[0]
		provider, err := FeedbackProviderState(entry, reviewers.Feedback.Threads)
		if err != nil {
			t.Fatal(err)
		}
		p.Feedback, p.OriginalProvider, p.LatestProvider, p.ContentVersion = &entry, &provider, &provider, entry.ContentVersion
		policy.ReviewerSelectors = []ReviewerSelector{{Kind: ReviewerBot, ID: entry.Author.ID, NodeID: entry.Author.NodeID}}
	case PRCIProblem:
		observed = ciProblemObservationFixture()
		proof, err := NewPRCIObservation(p.SetID, observed)
		if err != nil {
			t.Fatal(err)
		}
		contexts, err := proof.FailureContexts()
		if err != nil || len(contexts) != 1 {
			t.Fatal("failure fixture", err)
		}
		p.CI = &PRCIProblemEvidence{ObservationID: NewID(), Context: contexts[0], Source: proof.CI.Result.Source, RulesDigest: proof.CI.Rules.Digest}
		p.ContentVersion = contexts[0].Version()
	case PRMergeConflictProblem:
		conflict, err := ObservePRConflict(nil, observed)
		if err != nil {
			t.Fatal(err)
		}
		p.Conflict, p.ContentVersion = conflict.Active, conflict.Active.ContentVersion()
	}
	if p.Validate() != nil || observed.Validate() != nil {
		t.Fatal("invalid fixture", p.Validate(), observed.Validate())
	}
	return p, observed, policy
}

func TestPRRemediationPrerequisitesAreIndependentAndFresh(t *testing.T) {
	for _, kind := range []PRProblemKind{PRFeedbackProblem, PRCIProblem, PRMergeConflictProblem} {
		t.Run(string(kind), func(t *testing.T) {
			p, o, policy := remediationDomainProblem(t, kind)
			decision, err := EvaluatePRRemediation(p, o, policy, PRRemediationAutomatic, o.ObservedAt)
			if err != nil || decision != PRRemediationEligible {
				t.Fatal(decision, err)
			}
			// No CI observation is supplied or needed for feedback/conflicts.
			for _, age := range []time.Duration{31 * time.Second, -time.Second} {
				d, err := EvaluatePRRemediation(p, o, policy, PRRemediationAutomatic, o.ObservedAt.Add(age))
				if err != nil || d != PRRemediationStale {
					t.Fatal("stale proof", d, err)
				}
			}
			policy = DefaultRemediationPolicy()
			d, err := EvaluatePRRemediation(p, o, policy, PRRemediationAutomatic, o.ObservedAt)
			if err != nil || d != PRRemediationDisabled {
				t.Fatal("off defaults", d, err)
			}
		})
	}
}

func TestPRRemediationRechecksOriginalReviewVersionIdentityAndORPolicy(t *testing.T) {
	for _, mode := range []string{"approved", "dismissed", "permission-unavailable", "permission-required", "empty-selectors", "edited", "changed-author", "deleted-author"} {
		t.Run(mode, func(t *testing.T) {
			p, o, policy := remediationDomainProblem(t, PRFeedbackProblem)
			want := PRRemediationEligible
			switch mode {
			case "dismissed":
				o.Reviewers.Feedback.Entries[0].NativeState = "DISMISSED"
				o.Reviewers.Feedback.Entries[1].ReviewState = "DISMISSED"
			case "permission-unavailable":
				o.Reviewers.Actors[0].Permission = ReviewerPermission{Access: IntegrationAccessDenied}
			case "permission-required":
				o.Reviewers.Actors[0].Permission = ReviewerPermission{Access: IntegrationAccessDenied}
				policy.ReviewerSelectors = []ReviewerSelector{{Kind: ReviewerMinimumPermission, Permission: PermissionWrite}}
				want = PRRemediationReviewerUnknown
			case "empty-selectors":
				policy.ReviewerSelectors = nil
				want = PRRemediationReviewerDenied
			case "edited":
				o.Reviewers.Feedback.Entries[0].Body = "Edited original"
				o.Reviewers.Feedback.Entries[0].ContentVersion = o.Reviewers.Feedback.Entries[0].Version()
				o.Reviewers.Applications[0].ContentVersion = o.Reviewers.Feedback.Entries[0].ContentVersion
				want = PRRemediationVersionChanged
			case "changed-author":
				copy := *p.Feedback.Author
				copy.ID = "17"
				copy.NodeID = "OTHER_AUTHOR"
				p.Feedback.Author = &copy
				want = PRRemediationReviewerUnknown
			case "deleted-author":
				p.Feedback.Author = nil
				provider := *p.OriginalProvider
				provider.AuthorPresent = false
				p.OriginalProvider = &provider
				want = PRRemediationReviewerUnknown
			}
			d, err := EvaluatePRRemediation(p, o, policy, PRRemediationAutomatic, o.ObservedAt)
			if err != nil || d != want {
				t.Fatal(d, want, err)
			}
		})
	}
	// Explicit manual handling remains available for original unhandled content
	// even after an edit or deletion; it never invents reviewer verification.
	p, o, policy := remediationDomainProblem(t, PRFeedbackProblem)
	p.Current = false
	o.Query.Operation, o.Reviewers = RepositoryDetail, nil
	o.Items[0].Mergeable = nil
	d, err := EvaluatePRRemediation(p, o, policy, PRRemediationManual, o.ObservedAt)
	if err != nil || d != PRRemediationEligible {
		t.Fatal("manual feedback", d, err)
	}
}

func TestPRRemediationCINeedsCurrentRequiredResultAndConflictNeedsConfirmedTransition(t *testing.T) {
	for _, mode := range []string{"unknown", "passing", "pending", "changed-result", "optional"} {
		t.Run(mode, func(t *testing.T) {
			p, o, policy := remediationDomainProblem(t, PRCIProblem)
			want := PRRemediationNoCIFailure
			switch mode {
			case "unknown":
				o.CI.InMergeQueue = true
				want = PRRemediationCIUnknown
			case "passing":
				value := "SUCCESS"
				o.CI.Head.Contexts[0].NativeConclusion = &value
			case "pending":
				o.CI.Head.Contexts[0].NativeStatus = "IN_PROGRESS"
				o.CI.Head.Contexts[0].NativeConclusion = nil
			case "changed-result":
				value := "New original output"
				o.CI.Head.Contexts[0].Evidence.Text = &value
			case "optional":
				o.CI.Rules.Rules = []ActiveRepositoryRule{}
				o.CI.Rules.Digest = ActiveRulesDigest(nil)
			}
			o.CI.Result = o.CI.Evaluate(o.Items[0])
			d, err := EvaluatePRRemediation(p, o, policy, PRRemediationAutomatic, o.ObservedAt)
			if err != nil || d != want {
				t.Fatal(d, want, err)
			}
		})
	}
	for _, mode := range []string{"unknown", "resolved", "head", "base", "transition"} {
		t.Run(mode, func(t *testing.T) {
			p, o, policy := remediationDomainProblem(t, PRMergeConflictProblem)
			want := PRRemediationVersionChanged
			switch mode {
			case "unknown":
				o.Items[0].Mergeable = nil
				want = PRRemediationConflictUnknown
			case "resolved":
				yes := true
				o.Items[0].Mergeable = &yes
				want = PRRemediationNoConflict
			case "head":
				o.Items[0].HeadSHA = strings.Repeat("c", 40)
			case "base":
				o.Items[0].BaseRef = "release"
			case "transition":
				p.Current = false
			}
			d, err := EvaluatePRRemediation(p, o, policy, PRRemediationAutomatic, o.ObservedAt)
			if err != nil || d != want {
				t.Fatal(d, want, err)
			}
		})
	}
}

func TestPRRemediationRejectsForeignMalformedAndHandledEvidence(t *testing.T) {
	for _, mode := range []string{"foreign", "missing", "wrong-kind", "closed", "dismissed"} {
		t.Run(mode, func(t *testing.T) {
			p, o, policy := remediationDomainProblem(t, PRMergeConflictProblem)
			switch mode {
			case "foreign":
				o.Items[0].ID = "999"
			case "missing":
				o.Items = nil
			case "wrong-kind":
				o.Query.Operation = RepositoryList
				o.Query.Number = ""
				o.Items = []RepositoryItem{}
			case "closed":
				o.Items[0].State = RepositoryItemClosed
			case "dismissed":
				p.State = PRProblemDismissed
				p.Dismissal = &PRProblemDismissal{RequestID: NewID(), ActorType: OwnerDevice, At: o.ObservedAt}
			}
			d, err := EvaluatePRRemediation(p, o, policy, PRRemediationAutomatic, o.ObservedAt)
			if mode == "closed" {
				if err != nil || d != PRRemediationClosed {
					t.Fatal(d, err)
				}
			} else if mode == "dismissed" {
				if err != nil || d != PRRemediationAlreadyHandled {
					t.Fatal(d, err)
				}
			} else if err == nil {
				t.Fatal("foreign/malformed accepted", d)
			}
		})
	}
}
