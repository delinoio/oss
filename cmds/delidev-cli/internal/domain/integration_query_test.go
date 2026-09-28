package domain

import "testing"

func TestRepositoryQueryRejectsAuthoritySyntaxAndMixedOperations(t *testing.T) {
	base := RepositoryQuery{Kind: RepositoryIssue, Operation: RepositorySearch, State: RepositoryItemsAll, Search: "fix OR 오류", Page: 1, PageSize: 20}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"", " repo", "repo:other/private", `x" OR repo:other/private`, "fix\nrepo", "(fix)", "foo*"} {
		value := base
		value.Search = text
		if value.Validate() == nil {
			t.Fatal("query syntax accepted", text)
		}
	}
	for _, change := range []func(*RepositoryQuery){func(q *RepositoryQuery) { q.Page = 51 }, func(q *RepositoryQuery) { q.PageSize = 21 }, func(q *RepositoryQuery) { q.Number = "1" }, func(q *RepositoryQuery) { q.Kind = "other" }, func(q *RepositoryQuery) { q.Operation = RepositoryDetail }} {
		value := base
		change(&value)
		if value.Validate() == nil {
			t.Fatal("mixed query accepted", value)
		}
	}
	detail := RepositoryQuery{Kind: RepositoryPullRequest, Operation: RepositoryDetail, Number: "9007199254740993"}
	if detail.Validate() != nil {
		t.Fatal("exact number rejected")
	}
	detail.Number = "01"
	if detail.Validate() == nil {
		t.Fatal("noncanonical number accepted")
	}
}
