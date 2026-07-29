package service

import (
	"bytes"
	"testing"
	"time"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	deckgithub "github.com/delinoio/oss/servers/devhud-deck/internal/github"
	"github.com/delinoio/oss/servers/devhud-deck/internal/security"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPullRequestDetailPreservesAndRefreshesCompleteRow(t *testing.T) {
	t.Parallel()
	hasher, err := security.NewHasher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	service := &View{dependencies: Dependencies{Hasher: hasher}.withDefaults()}
	reference := &deckv1.PullRequestReference{
		Repository: &deckv1.RepositoryReference{
			Owner: "acme", Name: "widget",
		},
		Number: 7,
	}
	base := &deckv1.PullRequestResult{
		Repository:     reference.Repository,
		Number:         reference.Number,
		Title:          "Stored title",
		Author:         &deckv1.PullRequestAuthor{Login: "stored-author"},
		ReviewDecision: deckv1.ReviewDecision_REVIEW_DECISION_APPROVED,
		Checks: &deckv1.CheckSummary{
			State: deckv1.ChecksState_CHECKS_STATE_SUCCESS,
		},
		UpdatedAt: timestamppb.New(time.Date(
			2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		Assignees: []*deckv1.GitHubUser{{Login: "stored-assignee"}},
		Labels:    []string{"stored-label"},
	}
	currentTime := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	detail := service.pullRequestDetail(
		uuid.MustParse("01900000-0000-7000-8000-000000000001"),
		reference,
		base,
		deckgithub.ActionMetadata{
			Revision:  2,
			Title:     "Current title",
			Author:    deckgithub.User{Login: "current-author"},
			UpdatedAt: currentTime,
			Reviewers: []deckgithub.User{{Login: "reviewer"}},
			ReviewerTeams: []deckgithub.Team{{
				Organization: "acme", Slug: "core",
			}},
			Assignees: []deckgithub.User{{Login: "current-assignee"}},
			Labels:    []string{"current-label"},
			IsOpen:    true,
			Mergeable: true,
			Supported: map[deckgithub.MutationKind]bool{
				deckgithub.MutationAddLabels: true,
			},
			AvailableMethods: map[deckgithub.MergeMethod]bool{
				deckgithub.MergeMethodSquash: true,
			},
		})
	result := detail.Result
	if result.Title != "Current title" ||
		result.Author.GetLogin() != "current-author" ||
		!result.UpdatedAt.AsTime().Equal(currentTime) ||
		result.ReviewDecision != deckv1.ReviewDecision_REVIEW_DECISION_APPROVED ||
		result.Checks.GetState() != deckv1.ChecksState_CHECKS_STATE_SUCCESS ||
		len(result.Reviewers) != 2 ||
		result.Reviewers[0].GetUser().GetLogin() != "reviewer" ||
		result.Reviewers[1].GetTeam().GetSlug() != "core" ||
		len(result.Assignees) != 1 ||
		result.Assignees[0].GetLogin() != "current-assignee" ||
		len(result.Labels) != 1 || result.Labels[0] != "current-label" ||
		result.Revision.GetValue() != 2 ||
		result.LifecycleState !=
			deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_OPEN {
		t.Fatalf("complete mutation row = %#v", result)
	}
	if base.Title != "Stored title" ||
		base.Assignees[0].GetLogin() != "stored-assignee" ||
		base.Labels[0] != "stored-label" {
		t.Fatalf("stored snapshot was mutated: %#v", base)
	}
}
