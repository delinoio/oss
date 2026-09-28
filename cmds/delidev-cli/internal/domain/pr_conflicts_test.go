package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func conflictObservationFixture() RepositoryQueryResult {
	at := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	no := false
	body := "Original PR"
	item := RepositoryItem{Provider: GitHubCom, Kind: RepositoryPullRequest, IdentitySource: RepositoryPullRequestIdentity, ID: "53", NodeID: "PR_53", Number: "17", Title: "Original title", State: RepositoryItemOpen, CreatedAt: at, UpdatedAt: at, URL: "https://github.com/fixture-owner/repo/pull/17", Body: &body, Draft: &no, Merged: &no, Mergeable: &no, BaseRef: "main", BaseSHA: strings.Repeat("a", 40), HeadRef: "feature", HeadSHA: strings.Repeat("b", 40)}
	return RepositoryQueryResult{RepositoryID: NewID(), RepositoryRevision: "1", ProfileID: NewID(), GenerationID: NewID(), ObservedAt: at, Identity: GitHubIdentity{ID: "13", NodeID: "U_13", Login: "fixture-user"}, Repository: RemoteRepository{Provider: GitHubCom, ID: "37", NodeID: "R_37", Owner: "fixture-owner", Name: "repo"}, Query: RepositoryQuery{Kind: RepositoryPullRequest, Operation: RepositoryDetail, Number: "17"}, Items: []RepositoryItem{item}}
}

func TestPRConflictVersionSurvivesUnknownAndRestoration(t *testing.T) {
	input := conflictObservationFixture()
	first, err := ObservePRConflict(nil, input)
	if err != nil || first.State != PRConflictPresent || first.Active == nil {
		t.Fatal("verified conflict", err)
	}
	original := first.Active.ContentVersion()
	var previous PRConflictObservation
	raw, _ := json.Marshal(first)
	if Decode(raw, &previous) != nil || previous.Validate() != nil {
		t.Fatal("restore")
	}
	input.ObservedAt = input.ObservedAt.Add(time.Minute)
	unknownInput := input
	unknownInput.Items = append([]RepositoryItem{}, input.Items...)
	unknownInput.Items[0].Mergeable = nil
	unknown, err := ObservePRConflict(&previous, unknownInput)
	if err != nil || unknown.State != PRConflictUnknown || unknown.Active == nil || unknown.Active.ContentVersion() != original {
		t.Fatal("unknown reset a retained transition", err)
	}
	next, err := ObservePRConflict(&unknown, input)
	if err != nil || next.Active.ContentVersion() != original {
		t.Fatal("unchanged conflict was resurrected", err)
	}
	next.Active.BaseRef = "mutated copy"
	if previous.Active.BaseRef != "main" || unknown.Active.BaseRef != "main" {
		t.Fatal("aliased retained snapshots")
	}
	unknownOnly, err := ObservePRConflict(nil, unknownInput)
	if err != nil || unknownOnly.Active != nil || unknownOnly.State != PRConflictUnknown {
		t.Fatal("unknown manufactured conflict", err)
	}
}

func TestPRConflictVersionChangesOnlyAfterResolutionOrNewScope(t *testing.T) {
	for _, mode := range []string{"resolved", "closed", "head", "base", "base-ref", "head-ref"} {
		t.Run(mode, func(t *testing.T) {
			input := conflictObservationFixture()
			first, err := ObservePRConflict(nil, input)
			if err != nil {
				t.Fatal(err)
			}
			original := first.Active.ContentVersion()
			previous := first
			switch mode {
			case "resolved", "closed":
				if mode == "resolved" {
					yes := true
					input.Items[0].Mergeable = &yes
				} else {
					input.Items[0].State = RepositoryItemClosed
				}
				previous, err = ObservePRConflict(&previous, input)
				if err != nil || previous.Active != nil {
					t.Fatal("resolution retained active scope", err)
				}
				no := false
				input.Items[0].State = RepositoryItemOpen
				input.Items[0].Mergeable = &no
			case "head":
				input.Items[0].HeadSHA = strings.Repeat("c", 40)
			case "base":
				input.Items[0].BaseSHA = strings.Repeat("c", 40)
			case "base-ref":
				input.Items[0].BaseRef = "release"
			case "head-ref":
				input.Items[0].HeadRef = "other-feature"
			}
			next, err := ObservePRConflict(&previous, input)
			if err != nil || next.Active == nil || next.Active.ContentVersion() == original {
				t.Fatal("distinct conflict lost", err)
			}
		})
	}
}

func TestPRConflictRejectsMixedObservationsAndContradictoryState(t *testing.T) {
	input := conflictObservationFixture()
	first, err := ObservePRConflict(nil, input)
	if err != nil {
		t.Fatal(err)
	}
	first.State = PRConflictUnknown
	if first.Validate() == nil {
		t.Fatal("forged unknown classification")
	}
	input.CI = &PullRequestCI{}
	if _, err := ObservePRConflict(nil, input); err == nil {
		t.Fatal("mixed CI authority accepted")
	}
	input = conflictObservationFixture()
	*input.Items[0].Merged = true
	value, err := ObservePRConflict(nil, input)
	if err != nil || value.State != PRConflictNotApplicable || value.Active != nil {
		t.Fatal("merged PR conflict", err)
	}
}
