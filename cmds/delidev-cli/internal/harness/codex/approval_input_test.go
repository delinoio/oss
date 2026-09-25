package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestApprovalResponsePreparationPreservesNativeWireMeaning(t *testing.T) {
	for _, fixture := range []struct{ name, original, input, expected string }{
		{"execpolicy", `{"kind":"command","started_at_ms":1,"command":{"kind":"command","proposed_execpolicy":["printf",""],"available_decisions":[{"kind":"acceptWithExecpolicyAmendment","execpolicy":["printf",""]}]}}`, `{"decision":{"kind":"acceptWithExecpolicyAmendment","execpolicy":["printf",""]}}`, `{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["printf",""]}}`},
		{"network", `{"kind":"command","started_at_ms":1,"command":{"kind":"command","proposed_network_policy":[{"host":"fixture.invalid","action":"deny"}],"available_decisions":[{"kind":"applyNetworkPolicyAmendment","network_policy":{"host":"fixture.invalid","action":"deny"}}]}}`, `{"decision":{"kind":"applyNetworkPolicyAmendment","network_policy":{"host":"fixture.invalid","action":"deny"}}}`, `{"applyNetworkPolicyAmendment":{"network_policy_amendment":{"host":"fixture.invalid","action":"deny"}}}`},
		{"file", `{"kind":"file-change","started_at_ms":1,"file":{}}`, `{"decision":{"kind":"acceptForSession"}}`, `"acceptForSession"`},
		{"permissions", `{"kind":"permissions","started_at_ms":1,"permissions":{"cwd":"/fixture","permissions":{"network":{"enabled":true},"file_system":{"read":[],"write":["/fixture"],"entries":[{"access":"deny","path":{"type":"glob_pattern","pattern":"**/.env"}}],"glob_scan_max_depth":2}}}}`, `{"grant":{"permissions":{"network":{"enabled":false},"file_system":{"read":["/fixture"],"write":[],"entries":[{"access":"deny","path":{"type":"glob_pattern","pattern":"**/.env"}}],"glob_scan_max_depth":2}},"scope":"turn","strict_auto_review":true}}`, `{"permissions":{"network":{"enabled":false},"fileSystem":{"read":["/fixture"],"write":[],"entries":[{"access":"deny","path":{"type":"glob_pattern","pattern":"**/.env"}}],"globScanMaxDepth":2}},"scope":"turn","strictAutoReview":true}`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var original domain.CodexApprovalRequest
			var input domain.ApprovalResponseInput
			if err := domain.Decode([]byte(fixture.original), &original); err != nil {
				t.Fatal(err)
			}
			if err := domain.Decode([]byte(fixture.input), &input); err != nil {
				t.Fatal(err)
			}
			prepared, err := PrepareApprovalResponse(&domain.ApprovalRequest{Harness: domain.Codex, Version: SupportedVersion, Codex: &original}, input)
			if err != nil {
				t.Fatal(err)
			}
			var wire []byte
			if prepared.Decision != nil {
				wire, err = json.Marshal(prepared.Decision)
			} else {
				wire, err = json.Marshal(prepared.Grant)
			}
			if err != nil || string(wire) != fixture.expected {
				t.Fatalf("native response meaning changed: got %s, error %v", wire, err)
			}
			hidden, _ := json.Marshal(prepared)
			if string(hidden) != "{}" {
				t.Fatal("private prepared response entered ordinary JSON")
			}
		})
	}
}

func TestApprovalResponsePreparationRefusesOversizedOriginalChoice(t *testing.T) {
	prefix := []string{"printf"}
	for range 70 {
		prefix = append(prefix, strings.Repeat("x", 4096))
	}
	decision := domain.CodexApprovalDecision{Kind: domain.CodexApprovalExecpolicy, Execpolicy: prefix}
	original := &domain.ApprovalRequest{Harness: domain.Codex, Version: SupportedVersion, Codex: &domain.CodexApprovalRequest{Kind: domain.CodexCommandApproval, Command: &domain.CodexCommandApprovalRequest{Kind: domain.CodexExecuteCommandApproval, ProposedExecpolicy: prefix, AvailableDecisions: []domain.CodexApprovalDecision{decision}}}}
	if err := original.Validate(); err != nil {
		t.Fatal("fixture is not an original valid choice", err)
	}
	if _, err := PrepareApprovalResponse(original, domain.ApprovalResponseInput{Decision: &decision}); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("oversized response reached native preparation", err)
	}
}
