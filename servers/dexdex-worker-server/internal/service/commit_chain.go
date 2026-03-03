package service

import "strings"

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
