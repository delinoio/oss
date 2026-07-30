package main

import (
	"errors"
	"testing"
)

func TestSafeFailureReasonUsesClosedClassification(t *testing.T) {
	cause := errors.New("credential-shaped provider detail")
	err := classifyFailure(failureDatabase, cause)
	if got := safeFailureReason(err); got != failureDatabase {
		t.Fatalf("safe failure reason = %q", got)
	}
	if !errors.Is(err, cause) {
		t.Fatal("classified failure did not preserve its cause")
	}
	if got := safeFailureReason(cause); got != failureUnexpected {
		t.Fatalf("unclassified failure reason = %q", got)
	}
}
