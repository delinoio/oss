// SPDX-License-Identifier: Apache-2.0
package rpc

import (
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
