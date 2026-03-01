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
	Head   *githubPullRequestRef `json:"head"`
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
	event, err := loadPullRequestEvent(eventPath)
	if err != nil {
		return 0, "", err
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

func loadPullRequestHeadFromEvent(eventPath string) (string, error) {
	event, err := loadPullRequestEvent(eventPath)
	if err != nil {
		return "", err
	}
	if event.PullRequest == nil {
		return "", errors.New("pull_request payload is missing in GITHUB_EVENT_PATH")
	}
	if event.PullRequest.Head == nil || strings.TrimSpace(event.PullRequest.Head.SHA) == "" {
		return "", errors.New("pull_request.head.sha is missing in GITHUB_EVENT_PATH")
	}
	return strings.TrimSpace(event.PullRequest.Head.SHA), nil
}

func resolveHeadCommit(headCommit string) (string, error) {
	resolvedHead := strings.TrimSpace(headCommit)
	if resolvedHead != "" {
		return resolvedHead, nil
	}

	eventPath := strings.TrimSpace(os.Getenv("GITHUB_EVENT_PATH"))
	if eventPath != "" {
		eventHeadCommit, err := loadPullRequestHeadFromEvent(eventPath)
		if err == nil {
			return eventHeadCommit, nil
		}
	}

	resolvedHead = strings.TrimSpace(os.Getenv("GITHUB_SHA"))
	if resolvedHead != "" {
		return resolvedHead, nil
	}

	return "", errors.New("head commit is required (--head-commit, GITHUB_EVENT_PATH pull_request.head.sha, or GITHUB_SHA)")
}

func loadPullRequestEvent(eventPath string) (*githubPullRequestEvent, error) {
	if strings.TrimSpace(eventPath) == "" {
		return nil, errors.New("GITHUB_EVENT_PATH is not set")
	}

	payload, err := readFile(filepath.Clean(eventPath))
	if err != nil {
		return nil, fmt.Errorf("read GITHUB_EVENT_PATH: %w", err)
	}

	var event githubPullRequestEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, fmt.Errorf("parse GITHUB_EVENT_PATH JSON: %w", err)
	}

	return &event, nil
}
