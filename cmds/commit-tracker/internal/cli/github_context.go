package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type githubPullRequestEvent struct {
	PullRequest *githubPullRequest `json:"pull_request"`
}

type githubPullRequest struct {
	Number int64                 `json:"number"`
	Base   *githubPullRequestRef `json:"base"`
}

type githubPullRequestRef struct {
	SHA string `json:"sha"`
}

func resolvePullRequestContext(pullRequest int64, baseCommit string) (int64, string, error) {
	resolvedPullRequest := pullRequest
	resolvedBaseCommit := strings.TrimSpace(baseCommit)

	needsEventContext := resolvedPullRequest <= 0 || resolvedBaseCommit == ""
	if !needsEventContext {
		return resolvedPullRequest, resolvedBaseCommit, nil
	}

	eventPath := strings.TrimSpace(os.Getenv("GITHUB_EVENT_PATH"))
	if eventPath != "" {
		eventPullRequest, eventBaseCommit, err := loadPullRequestContextFromEvent(eventPath)
		if err != nil {
			return 0, "", err
		}
		if resolvedPullRequest <= 0 {
			resolvedPullRequest = eventPullRequest
		}
		if resolvedBaseCommit == "" {
			resolvedBaseCommit = eventBaseCommit
		}
	}

	if resolvedPullRequest <= 0 {
		return 0, "", errors.New("pull request is required (--pull-request or GITHUB_EVENT_PATH pull_request.number)")
	}
	if resolvedBaseCommit == "" {
		return 0, "", errors.New("base commit is required (--base-commit or GITHUB_EVENT_PATH pull_request.base.sha)")
	}

	return resolvedPullRequest, resolvedBaseCommit, nil
}

func loadPullRequestContextFromEvent(eventPath string) (int64, string, error) {
	if strings.TrimSpace(eventPath) == "" {
		return 0, "", errors.New("GITHUB_EVENT_PATH is not set")
	}

	payload, err := readFile(filepath.Clean(eventPath))
	if err != nil {
		return 0, "", fmt.Errorf("read GITHUB_EVENT_PATH: %w", err)
	}

	var event githubPullRequestEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return 0, "", fmt.Errorf("parse GITHUB_EVENT_PATH JSON: %w", err)
	}
	if event.PullRequest == nil {
		return 0, "", errors.New("pull_request payload is missing in GITHUB_EVENT_PATH")
	}
	if event.PullRequest.Number <= 0 {
		return 0, "", errors.New("pull_request.number is missing in GITHUB_EVENT_PATH")
	}
	if event.PullRequest.Base == nil || strings.TrimSpace(event.PullRequest.Base.SHA) == "" {
		return 0, "", errors.New("pull_request.base.sha is missing in GITHUB_EVENT_PATH")
	}

	return event.PullRequest.Number, strings.TrimSpace(event.PullRequest.Base.SHA), nil
}
