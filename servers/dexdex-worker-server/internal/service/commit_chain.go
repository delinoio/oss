package service

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type CommitMetadata struct {
	SHA               string
	Parents           []string
	Message           string
	AuthoredAtUnixNS  int64
	CommittedAtUnixNS int64
}

type CommitChainValidationErrorCode uint8

const (
	CommitChainValidationErrorCodeEmptyCommitChain CommitChainValidationErrorCode = iota + 1
	CommitChainValidationErrorCodeMissingSHA
	CommitChainValidationErrorCodeMissingMessage
	CommitChainValidationErrorCodeInvalidTimestamp
	CommitChainValidationErrorCodeNonMonotonicCommitTime
	CommitChainValidationErrorCodeMissingParentLink
)

type CommitChainValidationError struct {
	Code              CommitChainValidationErrorCode
	Index             int
	ExpectedParentSHA string
}

func (e *CommitChainValidationError) Error() string {
	if e == nil {
		return "commit chain validation error"
	}

	switch e.Code {
	case CommitChainValidationErrorCodeEmptyCommitChain:
		return "empty commit chain"
	case CommitChainValidationErrorCodeMissingSHA:
		return "missing sha"
	case CommitChainValidationErrorCodeMissingMessage:
		return "missing message"
	case CommitChainValidationErrorCodeInvalidTimestamp:
		return "invalid timestamp"
	case CommitChainValidationErrorCodeNonMonotonicCommitTime:
		return "non-monotonic commit time"
	case CommitChainValidationErrorCodeMissingParentLink:
		return "missing parent link"
	default:
		return "unknown commit chain validation error"
	}
}

func ValidateCommitChain(commitChain []CommitMetadata) error {
	if len(commitChain) == 0 {
		return &CommitChainValidationError{Code: CommitChainValidationErrorCodeEmptyCommitChain}
	}

	for index, commit := range commitChain {
		if strings.TrimSpace(commit.SHA) == "" {
			return &CommitChainValidationError{
				Code:  CommitChainValidationErrorCodeMissingSHA,
				Index: index,
			}
		}

		if strings.TrimSpace(commit.Message) == "" {
			return &CommitChainValidationError{
				Code:  CommitChainValidationErrorCodeMissingMessage,
				Index: index,
			}
		}

		if commit.AuthoredAtUnixNS <= 0 || commit.CommittedAtUnixNS <= 0 {
			return &CommitChainValidationError{
				Code:  CommitChainValidationErrorCodeInvalidTimestamp,
				Index: index,
			}
		}

		if index == 0 {
			continue
		}

		previous := commitChain[index-1]
		if commit.CommittedAtUnixNS < previous.CommittedAtUnixNS {
			return &CommitChainValidationError{
				Code:  CommitChainValidationErrorCodeNonMonotonicCommitTime,
				Index: index,
			}
		}

		if !containsParent(commit.Parents, previous.SHA) {
			return &CommitChainValidationError{
				Code:              CommitChainValidationErrorCodeMissingParentLink,
				Index:             index,
				ExpectedParentSHA: previous.SHA,
			}
		}
	}

	return nil
}

func containsParent(parents []string, expectedParentSHA string) bool {
	for _, parent := range parents {
		if parent == expectedParentSHA {
			return true
		}
	}

	return false
}

// ExtractCommitChain extracts commit metadata from a worktree directory for all
// commits after the given baseSHA. Commits are returned in chronological order
// (oldest first). If baseSHA is empty, all commits are returned.
func ExtractCommitChain(ctx context.Context, worktreePath, baseSHA string) ([]CommitMetadata, error) {
	// Format: SHA<sep>parents<sep>message<sep>author_timestamp<sep>commit_timestamp
	const separator = "<<SEP>>"
	format := fmt.Sprintf("%%H%s%%P%s%%s%s%%at%s%%ct", separator, separator, separator, separator)

	var args []string
	if baseSHA != "" {
		args = []string{"git", "-C", worktreePath, "log", "--format=" + format, baseSHA + "..HEAD", "--reverse"}
	} else {
		args = []string{"git", "-C", worktreePath, "log", "--format=" + format, "--reverse"}
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	output := strings.TrimSpace(string(out))
	if output == "" {
		return nil, nil // no new commits
	}

	lines := strings.Split(output, "\n")
	commits := make([]CommitMetadata, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, separator, 5)
		if len(parts) != 5 {
			continue
		}

		sha := parts[0]
		parentStr := parts[1]
		message := parts[2]
		authorTimestamp := parts[3]
		commitTimestamp := parts[4]

		var parents []string
		if parentStr != "" {
			parents = strings.Fields(parentStr)
		}

		authorUnix, err := strconv.ParseInt(authorTimestamp, 10, 64)
		if err != nil {
			authorUnix = 0
		}
		commitUnix, err := strconv.ParseInt(commitTimestamp, 10, 64)
		if err != nil {
			commitUnix = 0
		}

		commits = append(commits, CommitMetadata{
			SHA:               sha,
			Parents:           parents,
			Message:           message,
			AuthoredAtUnixNS:  authorUnix * 1_000_000_000,
			CommittedAtUnixNS: commitUnix * 1_000_000_000,
		})
	}

	return commits, nil
}
