// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
)

func TestResponseUsageVersionIsOptionalObservedMetadata(t *testing.T) {
	record := ResponseUsageRecord{SessionID: NewID(), ExecutionID: NewID(), AccountID: NewID(), ConnectionID: NewID(), ProviderID: NewID(), ModelID: NewID(), Harness: Codex, ThreadID: string(NewID()), TurnID: string(NewID()), Sequence: 1, Usage: NativeResponseUsage{ResponseDigest: strings.Repeat("a", 64), CostEvidence: UsageCostMissing}}
	record.ModelID = (ModelIdentity{ProviderID: record.ProviderID, NativeID: "fixture"}).Key()
	for _, version := range []string{"", "0.150.9", "999.0.0", "invalid/version", "raw content"} {
		t.Run(version, func(t *testing.T) {
			record.Version = version
			valid := version == "" || ValidNativeVersionMetadata(version)
			if (record.Validate() == nil) != valid {
				t.Fatal("usage version replaced observed metadata with a readiness gate")
			}
		})
	}
}
