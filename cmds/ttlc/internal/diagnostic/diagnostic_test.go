package diagnostic

import (
	"testing"

	"github.com/delinoio/oss/cmds/ttlc/internal/contracts"
)

func TestDeterministicIDStableForSameInput(t *testing.T) {
	issue := Diagnostic{
		Kind:    contracts.DiagnosticKindTypeError,
		Message: "task not found: Build",
		Line:    1,
		Column:  1,
	}

	firstID := issue.DeterministicID("/tmp/project/main.ttl")
	secondID := issue.DeterministicID("/tmp/project/main.ttl")
	if firstID == "" {
		t.Fatal("expected deterministic id to be non-empty")
	}
	if firstID != secondID {
		t.Fatalf("expected deterministic id stability, got first=%s second=%s", firstID, secondID)
	}
}

func TestDeterministicIDChangesWhenSourceChanges(t *testing.T) {
	issue := Diagnostic{
		Kind:    contracts.DiagnosticKindTypeError,
		Message: "task not found: Build",
		Line:    1,
		Column:  1,
	}

	leftID := issue.DeterministicID("/tmp/project/main.ttl")
	rightID := issue.DeterministicID("/tmp/project/lib.ttl")
	if leftID == rightID {
		t.Fatalf("expected deterministic id to differ by source path, got=%s", leftID)
	}
}
