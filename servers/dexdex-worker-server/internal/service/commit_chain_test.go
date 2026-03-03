package service

import (
	"errors"
	"testing"
)

func commit(index int64, sha string, parent *string) CommitMetadata {
	parents := make([]string, 0, 1)
	if parent != nil {
		parents = append(parents, *parent)
	}

	return CommitMetadata{
		SHA:               sha,
		Parents:           parents,
		Message:           "commit",
		AuthoredAtUnixNS:  1000 + index,
		CommittedAtUnixNS: 2000 + index,
	}
}

func stringPointer(value string) *string {
	return &value
}

func TestValidateCommitChainAcceptsOrderedRealCommitChain(t *testing.T) {
	chain := []CommitMetadata{
		commit(1, "sha-1", nil),
		commit(2, "sha-2", stringPointer("sha-1")),
		commit(3, "sha-3", stringPointer("sha-2")),
	}

	if err := ValidateCommitChain(chain); err != nil {
		t.Fatalf("ValidateCommitChain returned error: %v", err)
	}
}

func TestValidateCommitChainRejectsEmptyChain(t *testing.T) {
	err := ValidateCommitChain(nil)
	if err == nil {
		t.Fatal("expected error but got nil")
	}

	var validationError *CommitChainValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("expected CommitChainValidationError, got=%T", err)
	}
	if validationError.Code != CommitChainValidationErrorCodeEmptyCommitChain {
		t.Fatalf("unexpected error code: got=%v want=%v", validationError.Code, CommitChainValidationErrorCodeEmptyCommitChain)
	}
}

func TestValidateCommitChainRejectsMissingParentLink(t *testing.T) {
	chain := []CommitMetadata{
		commit(1, "sha-1", nil),
		commit(2, "sha-2", stringPointer("sha-x")),
	}

	err := ValidateCommitChain(chain)
	if err == nil {
		t.Fatal("expected error but got nil")
	}

	var validationError *CommitChainValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("expected CommitChainValidationError, got=%T", err)
	}
	if validationError.Code != CommitChainValidationErrorCodeMissingParentLink {
		t.Fatalf("unexpected error code: got=%v want=%v", validationError.Code, CommitChainValidationErrorCodeMissingParentLink)
	}
	if validationError.Index != 1 {
		t.Fatalf("unexpected error index: got=%d want=1", validationError.Index)
	}
	if validationError.ExpectedParentSHA != "sha-1" {
		t.Fatalf("unexpected expected parent sha: got=%q want=%q", validationError.ExpectedParentSHA, "sha-1")
	}
}

func TestValidateCommitChainRejectsNonMonotonicCommitTime(t *testing.T) {
	second := commit(2, "sha-2", stringPointer("sha-1"))
	second.CommittedAtUnixNS = 1900

	chain := []CommitMetadata{
		commit(1, "sha-1", nil),
		second,
	}

	err := ValidateCommitChain(chain)
	if err == nil {
		t.Fatal("expected error but got nil")
	}

	var validationError *CommitChainValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("expected CommitChainValidationError, got=%T", err)
	}
	if validationError.Code != CommitChainValidationErrorCodeNonMonotonicCommitTime {
		t.Fatalf("unexpected error code: got=%v want=%v", validationError.Code, CommitChainValidationErrorCodeNonMonotonicCommitTime)
	}
	if validationError.Index != 1 {
		t.Fatalf("unexpected error index: got=%d want=1", validationError.Index)
	}
}
