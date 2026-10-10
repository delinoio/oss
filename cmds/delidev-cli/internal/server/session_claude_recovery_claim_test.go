package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

// Assignment format and original-operation claim version are independent.
// Use the production operation selector without changing native opt-in admission.
func claudeRecoveryClaimMatches(input domain.ExecutionJobInput, claim uint32) bool {
	return claim == nativeRecoveryClaimVersion(input)
}
func TestClaudeRecoveryClaimVersionUsesOriginalOperationShape(t *testing.T) {
	for _, test := range []struct {
		name     string
		input    domain.ExecutionJobInput
		expected uint32
	}{
		{"first", domain.ExecutionJobInput{Version: 4}, 1},
		{"continuation", domain.ExecutionJobInput{Version: 4, Continuation: &domain.ExecutionContinuation{}}, 2},
		{"fork", domain.ExecutionJobInput{Version: 4, Fork: &domain.ForkExecution{}}, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			// These are only the operation-shape fields consumed by the fixture check;
			// they do not admit execution or stand in for a validated native assignment.
			if !claudeRecoveryClaimMatches(test.input, test.expected) {
				t.Fatal("valid operation claim confused with assignment format")
			}
			for _, claim := range []uint32{0, 1, 2, 3, 4, 5} {
				if claudeRecoveryClaimMatches(test.input, claim) != (claim == test.expected) {
					t.Fatal("unexpected original operation claim accepted", claim)
				}
			}
		})
	}
}
