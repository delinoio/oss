package domain

import (
	"encoding/json"
	"testing"
)

func TestRemediationPolicyReplacementDoesNotInheritAuthority(t *testing.T) {
	defaults := DefaultRemediationPolicy()
	if defaults.CIFailure || defaults.ReviewFeedback || defaults.MergeConflict || defaults.AttemptLimit != 3 || defaults.ConflictStrategy != MergeConflictStrategy || defaults.SessionStrategy != ReuseSession {
		t.Fatal(defaults)
	}
	defaults.CIFailure, defaults.ReviewFeedback, defaults.MergeConflict = true, true, true
	defaults.AgentID, defaults.MachineID = NewID(), NewID()
	defaults.ReviewerSelectors = []ReviewerSelector{{Kind: ReviewerUser, ID: "9007199254740993", NodeID: "U_original"}}
	repo := Repository{}
	inherited, err := repo.EffectiveRemediation(defaults)
	if err != nil || !inherited.ReviewFeedback || inherited.AgentID != defaults.AgentID {
		t.Fatal(inherited, err)
	}
	inherited.ReviewerSelectors[0].ID = "1"
	if defaults.ReviewerSelectors[0].ID != "9007199254740993" {
		t.Fatal("snapshot changed source selectors")
	}
	override := DefaultRemediationPolicy()
	repo.Remediation = &override
	effective, err := repo.EffectiveRemediation(defaults)
	if err != nil || effective.CIFailure || effective.ReviewFeedback || effective.MergeConflict || effective.AgentID != "" || effective.MachineID != "" || len(effective.ReviewerSelectors) != 0 {
		t.Fatal("override inherited authority", effective, err)
	}
	repo.Remediation.AttemptLimit = 0
	if _, err = repo.EffectiveRemediation(defaults); err == nil {
		t.Fatal("invalid override fell back")
	}
}

func TestRemediationPolicyValidatesExactSelectorsAndClosedChoices(t *testing.T) {
	for _, change := range []func(*RemediationPolicy){
		func(p *RemediationPolicy) { p.ConflictStrategy = "force" },
		func(p *RemediationPolicy) { p.SessionStrategy = "latest" },
		func(p *RemediationPolicy) { p.AttemptLimit = 0 },
		func(p *RemediationPolicy) { p.AttemptLimit = 101 },
		func(p *RemediationPolicy) { p.AgentID = "guess" },
		func(p *RemediationPolicy) { p.MachineID = "local" },
		func(p *RemediationPolicy) {
			p.ReviewerSelectors = []ReviewerSelector{{Kind: ReviewerUser, ID: "octocat"}}
		},
		func(p *RemediationPolicy) {
			p.ReviewerSelectors = []ReviewerSelector{{Kind: ReviewerUser, ID: "42", NodeID: "U_42"}, {Kind: ReviewerUser, ID: "42", NodeID: "U_other"}}
		},
		func(p *RemediationPolicy) {
			p.ReviewerSelectors = []ReviewerSelector{{Kind: ReviewerMinimumPermission, Permission: PermissionNone}}
		},
	} {
		policy := DefaultRemediationPolicy()
		change(&policy)
		if policy.Validate() == nil {
			t.Fatalf("accepted %+v", policy)
		}
		settings := DefaultSettings()
		settings.Remediation = policy
		if settings.Validate() == nil {
			t.Fatal("settings bypass")
		}
		repo := Repository{Name: "fixture", Checkouts: []Checkout{{MachineID: NewID(), Path: "/fixture"}}, Remediation: &policy}
		if repo.Validate() == nil {
			t.Fatal("repository bypass")
		}
	}
	// Missing selectors and execution choices remain a valid saved draft; they
	// never authorize review matching or creation of an execution session.
	policy := DefaultRemediationPolicy()
	policy.ReviewFeedback = true
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	policy.ReviewerSelectors = []ReviewerSelector{{Kind: ReviewerBot, ID: "9007199254740993", NodeID: "BOT_exact"}, {Kind: ReviewerApp, ID: "42", NodeID: "A_exact"}, {Kind: ReviewerMinimumPermission, Permission: PermissionMaintain}}
	raw, _ := json.Marshal(policy)
	var roundtrip RemediationPolicy
	if err := Decode(raw, &roundtrip); err != nil || roundtrip.Validate() != nil || roundtrip.ReviewerSelectors[0].ID != "9007199254740993" {
		t.Fatal(string(raw), err)
	}
}
