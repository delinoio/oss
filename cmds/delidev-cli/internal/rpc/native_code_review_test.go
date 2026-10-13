// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestNativeReviewUsageObservedAt(t *testing.T) {
	usage := domain.NativeResponseUsage{ResponseDigest: strings.Repeat("a", 64), CostEvidence: domain.UsageCostMissing}
	observed := time.Date(2026, 10, 13, 1, 2, 3, 4_000_000, time.UTC)
	wire := NativeReviewUsageWire(usage, 1, domain.CodexProtocolVersion, observed)
	if got, err := NativeReviewUsageObservedAt(wire); err != nil || !got.Equal(observed) {
		t.Fatal("observation time did not round-trip", got, err)
	}
	if got, err := NativeReviewUsageObservedAt(NativeReviewUsageWire(usage, 1, domain.CodexProtocolVersion, time.Time{})); err != nil || !got.IsZero() {
		t.Fatal("older Workers report no observation time", got, err)
	}
	for _, invalid := range []int64{-1, 1, 1 << 60} {
		if _, err := NativeReviewUsageObservedAt(&pb.NativeCodeReviewResponseUsage{ObservedAtUnixMs: invalid}); err == nil {
			t.Fatal("invalid observation time accepted", invalid)
		}
	}
}

func TestNativeReviewPublicOutputDropsUsageReceipts(t *testing.T) {
	result := domain.NativeCodeReviewResult{
		Version: 1, ActionID: domain.NewID(), Findings: []domain.NativeCodeReviewFinding{}, Correctness: "patch is correct",
		Explanation: "kept", RolloutDigest: strings.Repeat("e", 64), CleanupVerified: true,
		UsageRecords: []domain.ResponseUsageRecord{{AccountID: domain.NewID(), ConnectionID: domain.NewID(), ModelID: "private-model", ThreadID: "private-thread", TurnID: "private-turn", Usage: domain.NativeResponseUsage{ResponseDigest: strings.Repeat("d", 64)}}},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	public, ok := nativeReviewPublicOutput(raw)
	if !ok {
		t.Fatal("successful review output was refused")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(public, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["usage_records"]; exists {
		t.Fatal("public output kept usage receipts")
	}
	for _, private := range []string{"private-model", "private-thread", "private-turn", "account_id", "connection_id", strings.Repeat("d", 64)} {
		if strings.Contains(string(public), private) {
			t.Fatal("public output leaked private usage identity", private)
		}
	}
	var kept domain.NativeCodeReviewResult
	if err := json.Unmarshal(public, &kept); err != nil || kept.ActionID != result.ActionID || kept.Explanation != "kept" || kept.RolloutDigest != result.RolloutDigest || !kept.CleanupVerified || kept.Findings == nil {
		t.Fatal("redaction changed non-private result fields", err)
	}
	if !bytes.Contains(raw, []byte("usage_records")) {
		t.Fatal("redaction mutated the persisted output")
	}
}

func TestNativeReviewPublicOutputPassesProofsThrough(t *testing.T) {
	action := domain.NewID()
	for name, proof := range map[string]any{
		"rejected": domain.NativeCodeReviewRejectedProof{Version: 1, ActionID: action, NoSend: true, CleanupVerified: true},
		"stale":    domain.NativeCodeReviewStaleProof{Version: 1, ActionID: action, Stale: true, CleanupVerified: true},
	} {
		raw, _ := json.Marshal(proof)
		public, ok := nativeReviewPublicOutput(raw)
		if !ok || !bytes.Equal(public, raw) {
			t.Fatal(name, "proof changed by projection", string(public))
		}
	}
}

func TestNativeReviewPublicOutputFailsClosed(t *testing.T) {
	for name, raw := range map[string][]byte{
		"not-object": []byte(`"usage_records"`),
		"malformed":  []byte(`{"usage_records":[`),
		"oversized":  append([]byte(`{"usage_records":[],"x":"`), append(bytes.Repeat([]byte("a"), 1<<20), '"', '}')...),
	} {
		if public, ok := nativeReviewPublicOutput(raw); ok || public != nil {
			t.Fatal(name, "output was not refused")
		}
	}
}
