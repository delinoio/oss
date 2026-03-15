package polling

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// GitHubPR holds PR metadata from the GitHub API.
type GitHubPR struct {
	Number    int    `json:"number"`
	State     string `json:"state"` // "open", "closed"
	Merged    bool   `json:"merged"`
	MergedAt  string `json:"merged_at"` // empty if not merged
	Title     string `json:"title"`
	HTMLURL   string `json:"html_url"`
	StatusURL string `json:"statuses_url"`
	Head      struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

// GitHubReview holds a single review.
type GitHubReview struct {
	State string `json:"state"` // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED
}

// GitHubCheckRun holds a single check run.
type GitHubCheckRun struct {
	Status     string `json:"status"`     // queued, in_progress, completed
	Conclusion string `json:"conclusion"` // success, failure, neutral, cancelled, timed_out, action_required
}

// GitHubCheckRunsResponse holds the check runs response.
type GitHubCheckRunsResponse struct {
	CheckRuns []GitHubCheckRun `json:"check_runs"`
}

// GitHubClient wraps the `gh` CLI for GitHub API access.
type GitHubClient struct {
	logger *slog.Logger
}

// NewGitHubClient creates a new GitHub client.
func NewGitHubClient(logger *slog.Logger) *GitHubClient {
	return &GitHubClient{logger: logger}
}

// GetPullRequest fetches a PR by owner, repo, and number.
func (c *GitHubClient) GetPullRequest(ctx context.Context, owner, repo string, number int) (*GitHubPR, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, number)
	out, err := c.ghAPI(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	var pr GitHubPR
	if err := json.Unmarshal(out, &pr); err != nil {
		return nil, fmt.Errorf("parse PR response: %w", err)
	}
	return &pr, nil
}

// ListPullRequestReviews fetches reviews for a PR.
func (c *GitHubClient) ListPullRequestReviews(ctx context.Context, owner, repo string, number int) ([]GitHubReview, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, repo, number)
	out, err := c.ghAPI(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	var reviews []GitHubReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return nil, fmt.Errorf("parse reviews response: %w", err)
	}
	return reviews, nil
}

// GetCheckRuns fetches check runs for a commit SHA.
func (c *GitHubClient) GetCheckRuns(ctx context.Context, owner, repo, sha string) ([]GitHubCheckRun, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/commits/%s/check-runs", owner, repo, sha)
	out, err := c.ghAPI(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	var resp GitHubCheckRunsResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parse check runs response: %w", err)
	}
	return resp.CheckRuns, nil
}

// GitHubReviewComment holds a single review comment with file/line context.
type GitHubReviewComment struct {
	Body     string `json:"body"`
	Path     string `json:"path"`
	Line     int32  `json:"line"`
	Side     string `json:"side"` // "LEFT" or "RIGHT"
	DiffHunk string `json:"diff_hunk"`
}

// ListPullRequestComments fetches review comments for a PR.
func (c *GitHubClient) ListPullRequestComments(ctx context.Context, owner, repo string, number int) ([]GitHubReviewComment, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/comments", owner, repo, number)
	out, err := c.ghAPI(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	var comments []GitHubReviewComment
	if err := json.Unmarshal(out, &comments); err != nil {
		return nil, fmt.Errorf("parse review comments response: %w", err)
	}
	return comments, nil
}

// CreatePullRequest creates a new PR via the gh CLI.
func (c *GitHubClient) CreatePullRequest(ctx context.Context, owner, repo, head, base, title, body string) (*GitHubPR, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls", owner, repo)
	payload := fmt.Sprintf(`{"title":%q,"head":%q,"base":%q,"body":%q}`, title, head, base, body)

	cmd := exec.CommandContext(ctx, "gh", "api", endpoint, "--method", "POST", "--input", "-")
	cmd.Stdin = strings.NewReader(payload)
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		c.logger.Warn("gh api create PR failed",
			"endpoint", endpoint,
			"error", err,
			"stderr", stderr,
		)
		return nil, fmt.Errorf("gh api create PR: %w", err)
	}

	var pr GitHubPR
	if err := json.Unmarshal(out, &pr); err != nil {
		return nil, fmt.Errorf("parse create PR response: %w", err)
	}
	return &pr, nil
}

func (c *GitHubClient) ghAPI(ctx context.Context, endpoint string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", "api", endpoint)
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		c.logger.Warn("gh api call failed",
			"endpoint", endpoint,
			"error", err,
			"stderr", stderr,
		)
		return nil, fmt.Errorf("gh api %s: %w", endpoint, err)
	}
	return out, nil
}
