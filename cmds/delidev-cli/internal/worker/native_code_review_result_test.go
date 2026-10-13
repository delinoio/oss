// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func nativeReviewResultFixture(t *testing.T, action domain.ID) domain.NativeCodeReviewResult {
	t.Helper()
	thread, turn, provider := domain.NewID(), domain.NewID(), domain.NewID()
	target := domain.NativeCodeReviewTarget{Kind: domain.ReviewUncommitted, RepositoryID: domain.NewID(), DiffRevision: strings.Repeat("a", 64)}
	result := domain.NativeCodeReviewResult{
		Version: 1, ActionID: action,
		Selection:     domain.NativeCodeReviewSelection{Target: target, HeadCommit: strings.Repeat("b", 40), ContentDigest: strings.Repeat("c", 64)},
		ThreadID:      domain.NativeIdentity(thread),
		TurnID:        domain.NativeIdentity(turn),
		EnteredItemID: "entered", ExitedItemID: "exited",
		Findings:    []domain.NativeCodeReviewFinding{{Title: "Keep identity", Body: "The finding body.", Confidence: 0.7, Priority: 1, Path: "src/main.go", StartLine: 1, EndLine: 2}},
		Explanation: "Bounded explanation.", Correctness: "patch is incorrect",
		RolloutDigest: strings.Repeat("e", 64), CleanupVerified: true,
		UsageRecords: []domain.ResponseUsageRecord{{Purpose: domain.NativeCodeReviewUsage, SessionID: domain.NewID(), ExecutionID: action, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: provider, ModelID: (domain.ModelIdentity{ProviderID: provider, NativeID: "fixture"}).Key(), Harness: domain.Codex, ThreadID: string(thread), TurnID: string(turn), Sequence: 1, Usage: domain.NativeResponseUsage{ResponseDigest: strings.Repeat("a", 64), CostEvidence: domain.UsageCostMissing}}},
	}
	if err := result.Validate(); err != nil {
		t.Fatal("fixture rejected", err)
	}
	return result
}

func nativeReviewJobRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "jobs", string(domain.NewID()))
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestNativeReviewResultJustUnderCeilingIsWrittenAndReturned(t *testing.T) {
	result := nativeReviewResultFixture(t, domain.NewID())
	// Escaped characters count at their encoded size: each bracket is six bytes.
	for len(result.Findings) < 64 {
		result.Findings = append(result.Findings, domain.NativeCodeReviewFinding{Title: "Keep identity", Body: strings.Repeat("<", 2000), Confidence: 0.7, Priority: 1, Path: "src/main.go", StartLine: 1, EndLine: 2})
	}
	encoded, _ := json.Marshal(result)
	if len(encoded) >= domain.MaxWorkerJobOutputBytes || len(encoded) < domain.MaxWorkerJobOutputBytes/2 {
		t.Fatalf("fixture must be a large result under the ceiling, got %d bytes", len(encoded))
	}
	jobRoot := nativeReviewJobRoot(t)
	output, err := finalizeNativeReviewResult(context.Background(), slog.New(slog.DiscardHandler), jobRoot, result)
	if err != nil {
		t.Fatal("result under the ceiling rejected", err)
	}
	retained, err := os.ReadFile(filepath.Join(jobRoot, "review-result.json"))
	if err != nil || !bytes.Equal(retained, output) || !bytes.Equal(output, encoded) {
		t.Fatal("reported bytes were not the exact retained result", err)
	}
	var decoded domain.NativeCodeReviewResult
	if domain.Decode(output, &decoded) != nil || len(decoded.Findings) != 64 || len(decoded.UsageRecords) != 1 {
		t.Fatal("findings or usage were lost")
	}
}

func TestNativeReviewResultOverCeilingIsRejectedBeforeRetention(t *testing.T) {
	result := nativeReviewResultFixture(t, domain.NewID())
	for len(result.Findings) < 64 {
		result.Findings = append(result.Findings, domain.NativeCodeReviewFinding{Title: strings.Repeat("<", 1024), Body: strings.Repeat("<", 8192), Confidence: 0.7, Priority: 1, Path: "src/main.go", StartLine: 1, EndLine: 2})
	}
	jobRoot := nativeReviewJobRoot(t)
	output, err := finalizeNativeReviewResult(context.Background(), slog.New(slog.DiscardHandler), jobRoot, result)
	if err == nil || output != nil || domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatalf("over-limit result was not rejected as resource exhausted: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(jobRoot, "review-result.json")); !os.IsNotExist(statErr) {
		t.Fatal("over-limit result was retained")
	}
}
