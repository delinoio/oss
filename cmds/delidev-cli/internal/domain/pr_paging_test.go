// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strconv"
	"testing"
)

// TestRepositoryQueryPageSizeCeilings pins the PULL_REQUEST_PAGING_50_V1 limits:
// only pull request List/Search reach 50, every other paged query stays at 20.
func TestRepositoryQueryPageSizeCeilings(t *testing.T) {
	type tc struct {
		name  string
		query RepositoryQuery
		max   uint32
	}
	cases := []tc{
		{"pr list", RepositoryQuery{Kind: RepositoryPullRequest, Operation: RepositoryList, State: RepositoryItemOpen, Page: 1}, 50},
		{"pr search", RepositoryQuery{Kind: RepositoryPullRequest, Operation: RepositorySearch, State: RepositoryItemsAll, Search: "fix", Page: 1}, 50},
		{"issue list", RepositoryQuery{Kind: RepositoryIssue, Operation: RepositoryList, State: RepositoryItemOpen, Page: 1}, 20},
		{"issue search", RepositoryQuery{Kind: RepositoryIssue, Operation: RepositorySearch, State: RepositoryItemsAll, Search: "fix", Page: 1}, 20},
		{"pr checks", RepositoryQuery{Kind: RepositoryPullRequest, Operation: RepositoryChecks, Number: "7", Page: 1}, 20},
		{"pr statuses", RepositoryQuery{Kind: RepositoryPullRequest, Operation: RepositoryStatuses, Number: "7", Page: 1}, 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, size := range []uint32{1, 20, c.max} {
				q := c.query
				q.PageSize = size
				if err := q.Validate(); err != nil {
					t.Fatalf("size %d rejected: %v", size, err)
				}
			}
			for _, size := range []uint32{0, c.max + 1, 51, 1000} {
				q := c.query
				q.PageSize = size
				if q.Validate() == nil {
					t.Fatalf("size %d accepted", size)
				}
			}
		})
	}
	if MaxPullRequestListPageSize(RepositoryIssue) != 20 || MaxPullRequestListPageSize(RepositoryPullRequest) != 50 {
		t.Fatal("kind-dependent ceiling changed")
	}
}

// TestPullRequestSearchCeilingAtFiftyKeepsThousandMatchBound proves that the
// provider's 1,000-match window still rejects pages past the ceiling at size 50.
func TestPullRequestSearchCeilingAtFiftyKeepsThousandMatchBound(t *testing.T) {
	q := RepositoryQuery{Kind: RepositoryPullRequest, Operation: RepositorySearch, State: RepositoryItemsAll, Search: "fix", Page: 20, PageSize: 50}
	if err := q.Validate(); err != nil {
		t.Fatal("last in-window page rejected", err)
	}
	q.Page = 21
	if q.Validate() == nil {
		t.Fatal("page past the 1,000-match ceiling accepted")
	}
}

func workspaceWithSeeds(count int) PRWorkspace {
	w := workspaceNodeFixture()
	base := w.Nodes[0]
	w.Seeds, w.Nodes = nil, nil
	for n := 1; n <= count; n++ {
		number := strconv.Itoa(n)
		node := base
		node.Item.Number, node.Item.ID, node.Item.NodeID = number, strconv.Itoa(1000+n), "PR_"+number
		node.Item.URL = RepositoryItemURL(w.Repository.Owner, w.Repository.Name, RepositoryPullRequest, number)
		w.Seeds = append(w.Seeds, number)
		w.Nodes = append(w.Nodes, node)
	}
	return w
}

func TestPRWorkspaceSeedCeiling(t *testing.T) {
	for _, count := range []int{1, 20, 50} {
		if !workspaceWithSeeds(count).Validate() {
			t.Fatalf("%d distinct seeds rejected", count)
		}
	}
	if workspaceWithSeeds(51).Validate() {
		t.Fatal("51 seeds accepted")
	}
	w := workspaceWithSeeds(50)
	w.Seeds[49] = w.Seeds[0]
	if w.Validate() {
		t.Fatal("duplicate seed accepted")
	}
}

func TestPRWorkspaceWithFiftySeedsStaysUnderResponseCap(t *testing.T) {
	raw, err := json.Marshal(workspaceWithSeeds(50))
	if err != nil || len(raw) >= 1<<20 {
		t.Fatal("body-free fifty-seed workspace exceeds the response cap", len(raw), err)
	}
}
