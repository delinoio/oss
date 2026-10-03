// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJobValidateAllowsBoundedCompactionEnvelope(t *testing.T) {
	payload, err := json.Marshal(strings.Repeat("x", 1200000))
	if err != nil {
		t.Fatal(err)
	}
	compaction := Job{Type: CompactSessionJob, State: JobQueued, MachineID: NewID(), Input: payload}
	if err := compaction.Validate(); err != nil {
		t.Fatalf("compaction envelope should fit its dedicated bound: %v", err)
	}
	regular := Job{Type: ExecuteSessionJob, State: JobQueued, MachineID: NewID(), Input: payload}
	if err := regular.Validate(); err == nil {
		t.Fatal("regular job unexpectedly accepted an oversized input")
	}
}
