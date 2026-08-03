package database

import (
	"fmt"
	"testing"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
)

func TestWidgetSnapshotPullRequestItemsAreBounded(t *testing.T) {
	t.Parallel()
	snapshots := make([]*deckv1.PullRequestResult, widgetSnapshotPullRequestLimit+1)
	for index := range snapshots {
		snapshots[index] = &deckv1.PullRequestResult{
			Repository: &deckv1.RepositoryReference{Owner: "acme", Name: "widgets"},
			Number:     uint64(index + 1),
			Title:      fmt.Sprintf("pull request %d", index+1),
		}
	}

	items := widgetSnapshotPullRequestItems(
		deckv1.WidgetPrivacy_WIDGET_PRIVACY_REPOSITORY_AND_TITLES, snapshots)
	if len(items) != widgetSnapshotPullRequestLimit {
		t.Fatalf("detailed widget items = %d, want %d",
			len(items), widgetSnapshotPullRequestLimit)
	}
	if items[len(items)-1].GetNumber() != widgetSnapshotPullRequestLimit {
		t.Fatalf("last detailed widget item = %d, want %d",
			items[len(items)-1].GetNumber(), widgetSnapshotPullRequestLimit)
	}
	if items[0].GetRepository() == snapshots[0].GetRepository() {
		t.Fatal("detailed widget item aliases the retained snapshot")
	}
	if items := widgetSnapshotPullRequestItems(
		deckv1.WidgetPrivacy_WIDGET_PRIVACY_COUNTS_ONLY, snapshots); len(items) != 0 {
		t.Fatalf("counts-only widget retained %d detailed items", len(items))
	}
}
