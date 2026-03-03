package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	v1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

type GitHubCLI struct{}

type PullRequestRef struct {
	Repository string
	Number     int64
}

func NewGitHubCLI() *GitHubCLI {
	return &GitHubCLI{}
}

func ParsePRTrackingID(prTrackingID string) (PullRequestRef, error) {
	parts := strings.Split(strings.TrimSpace(prTrackingID), "#")
	if len(parts) != 2 {
		return PullRequestRef{}, fmt.Errorf("pr_tracking_id must be formatted as owner/repo#number")
	}
	number, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil || number <= 0 {
		return PullRequestRef{}, fmt.Errorf("invalid pull request number in pr_tracking_id")
	}

	repository := strings.TrimSpace(parts[0])
	if repository == "" || !strings.Contains(repository, "/") {
		return PullRequestRef{}, fmt.Errorf("invalid repository in pr_tracking_id")
	}

	return PullRequestRef{Repository: repository, Number: number}, nil
}

func (g *GitHubCLI) GetPullRequest(ctx context.Context, prTrackingID string) (*v1.PullRequestRecord, error) {
	ref, err := ParsePRTrackingID(prTrackingID)
	if err != nil {
		return nil, err
	}

	output, err := runCommand(ctx, "gh", "api", fmt.Sprintf("repos/%s/pulls/%d", ref.Repository, ref.Number))
	if err != nil {
		return nil, err
	}

	payload := struct {
		State  string `json:"state"`
		Merged bool   `json:"merged"`
	}{}
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("decode pull request payload: %w", err)
	}

	status := v1.PrStatus_PR_STATUS_OPEN
	switch {
	case payload.Merged:
		status = v1.PrStatus_PR_STATUS_MERGED
	case strings.EqualFold(payload.State, "closed"):
		status = v1.PrStatus_PR_STATUS_CLOSED
	case strings.EqualFold(payload.State, "open"):
		status = v1.PrStatus_PR_STATUS_OPEN
	}

	return &v1.PullRequestRecord{
		PrTrackingId: prTrackingID,
		Status:       status,
		Repository:   ref.Repository,
		Number:       ref.Number,
	}, nil
}

func (g *GitHubCLI) ListReviewComments(ctx context.Context, prTrackingID string) ([]*v1.ReviewComment, error) {
	ref, err := ParsePRTrackingID(prTrackingID)
	if err != nil {
		return nil, err
	}

	output, err := runCommand(ctx, "gh", "api", fmt.Sprintf("repos/%s/pulls/%d/comments", ref.Repository, ref.Number))
	if err != nil {
		return nil, err
	}

	payload := make([]struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}, 0)
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("decode review comments payload: %w", err)
	}

	comments := make([]*v1.ReviewComment, 0, len(payload))
	for _, item := range payload {
		comments = append(comments, &v1.ReviewComment{
			ReviewCommentId: strconv.FormatInt(item.ID, 10),
			Body:            item.Body,
		})
	}

	return comments, nil
}

func (g *GitHubCLI) ListReviewAssistItems(ctx context.Context, prTrackingID string) ([]*v1.ReviewAssistItem, error) {
	ref, err := ParsePRTrackingID(prTrackingID)
	if err != nil {
		return nil, err
	}

	output, err := runCommand(ctx, "gh", "api", fmt.Sprintf("repos/%s/pulls/%d/reviews", ref.Repository, ref.Number))
	if err != nil {
		return nil, err
	}

	payload := make([]struct {
		ID    int64  `json:"id"`
		State string `json:"state"`
		Body  string `json:"body"`
	}, 0)
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("decode review assist payload: %w", err)
	}

	items := make([]*v1.ReviewAssistItem, 0, len(payload))
	for _, review := range payload {
		body := strings.TrimSpace(review.Body)
		if body == "" {
			body = fmt.Sprintf("state=%s", review.State)
		}
		items = append(items, &v1.ReviewAssistItem{
			ReviewAssistId: strconv.FormatInt(review.ID, 10),
			Body:           body,
		})
	}

	return items, nil
}

func runCommand(ctx context.Context, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run %s %s: %w", command, strings.Join(args, " "), err)
	}
	return output, nil
}
