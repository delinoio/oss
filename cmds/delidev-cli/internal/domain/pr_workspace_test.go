// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
	"time"
)

func workspaceNodeFixture() PRWorkspace {
	remote := RemoteRepository{Provider: GitHubCom, ID: "37", NodeID: "R_37", Owner: "fixture-owner", Name: "repo", DefaultBranch: "main"}
	created := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	draft, merged := false, false
	item := RepositoryItem{
		Provider: GitHubCom, Kind: RepositoryPullRequest, IdentitySource: RepositoryPullRequestIdentity,
		ID: "1001", NodeID: "PR_1", Number: "1", Title: "Fixture", State: RepositoryItemOpen,
		CreatedAt: created, UpdatedAt: created, URL: RepositoryItemURL("fixture-owner", "repo", RepositoryPullRequest, "1"),
		Draft: &draft, Merged: &merged, BaseRef: "main", HeadRef: "feature",
		BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40),
		HeadRepository: &PRHeadRepositoryObservation{State: PRHeadRepositoryAvailable, Repository: &remote},
	}
	scope := PRReadScope{RepositoryID: NewID(), RepositoryRevision: "1", ProfileID: NewID(), GenerationID: NewID(), ObservedAt: created, Repository: remote}
	return PRWorkspace{PRReadScope: scope, State: PRWorkspaceComplete, Reasons: []string{}, Seeds: []string{"1"}, Nodes: []PRWorkspaceNode{{Item: item, Seed: true}}, Edges: []PRWorkspaceEdge{}}
}

func TestPRWorkspaceNodesAreBodyFree(t *testing.T) {
	if !workspaceNodeFixture().Validate() {
		t.Fatal("body-free node rejected")
	}
	for _, body := range []string{"", "description"} {
		w := workspaceNodeFixture()
		w.Nodes[0].Item.Body = &body
		if w.Validate() {
			t.Fatalf("node carrying body %q accepted", body)
		}
	}
	w := workspaceNodeFixture()
	w.Nodes[0].Item.BaseSHA = "short"
	if w.Validate() {
		t.Fatal("body-free validation skipped the Detail rules")
	}
}

func TestRepositoryDetailStillRequiresBodyWithinLimit(t *testing.T) {
	w := workspaceNodeFixture()
	item := w.Nodes[0].Item
	query := RepositoryQuery{Kind: RepositoryPullRequest, Operation: RepositoryDetail, Number: "1"}
	if item.Validate(w.Repository, query) == nil {
		t.Fatal("Detail item without body accepted")
	}
	over := strings.Repeat("x", 128<<10+1)
	item.Body = &over
	if item.Validate(w.Repository, query) == nil {
		t.Fatal("oversize Detail body accepted")
	}
	ok := strings.Repeat("x", 128<<10)
	item.Body = &ok
	if err := item.Validate(w.Repository, query); err != nil {
		t.Fatal("limit-sized Detail body rejected", err)
	}
}
