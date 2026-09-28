package domain

import "testing"

func TestPRObservationQueryFamiliesAndNoEmptyPassingAggregate(t *testing.T) {
	for _, operation := range []RepositoryQueryOperation{RepositoryDiff, RepositoryChecks, RepositoryStatuses} {
		q := RepositoryQuery{Kind: RepositoryPullRequest, Operation: operation, Number: "17"}
		if operation != RepositoryDiff {
			q.Page = 1
			q.PageSize = 20
		}
		if q.Validate() != nil {
			t.Fatal("valid PR query rejected")
		}
		q.Kind = RepositoryIssue
		if q.Validate() == nil {
			t.Fatal("issue gained PR observation")
		}
	}
	item := RepositoryItem{HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	value := PullRequestCommitStatuses{HeadSHA: item.HeadSHA, NativeState: "success", State: CommitStatusSuccess, TotalCount: "0", Contexts: []PullRequestCommitStatus{}}
	if value.Validate(item, 20) == nil {
		t.Fatal("empty statuses became passing")
	}
	value.NativeState = "pending"
	value.State = CommitStatusPending
	if value.Validate(item, 20) != nil {
		t.Fatal("empty pending statuses rejected")
	}
	value.NativeState = "future-native-state"
	value.State = CommitStatusUnknown
	if value.Validate(item, 20) != nil {
		t.Fatal("unknown source state lost")
	}
}
