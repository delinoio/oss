// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExecutionStartupFailureSeparatesDeliveryAndCleanup(t *testing.T) {
	base := ExecutionStartupObservation{State: StartupFailed, Phase: StartupInitialize, Harness: Codex, NativeVersion: "0.150.9", ProblemCode: Unsupported, CorrelationID: NewID(), InputDelivery: StartupNotSent, Cleanup: StartupCleanupConfirmed}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"delivery", "cleanup", "secret", "phase", "protocol", "code", "digest"} {
		o := base
		switch fault {
		case "delivery":
			o.InputDelivery = StartupClaimed
		case "cleanup":
			o.Cleanup = StartupCleanupUncertain
		case "secret":
			o.NativeVersion = "raw/native/secret"
		case "phase":
			o.Phase = 99
		case "protocol":
			o.Protocol = GrokACP
		case "code":
			o.ProblemCode = "native-text"
		case "digest":
			o.ExecutableSHA256 = strings.Repeat("A", 64)
		}
		if o.Validate() == nil {
			t.Fatalf("accepted contradictory %s", fault)
		}
	}
	base.State, base.InputDelivery, base.Cleanup = StartupUncertain, StartupClaimed, StartupCleanupConfirmed
	if base.Validate() != nil {
		t.Fatal("confirmed cleanup erased uncertain input delivery")
	}
	base.InputDelivery, base.Cleanup = StartupNotSent, StartupCleanupUncertain
	if base.Validate() != nil {
		t.Fatal("no-send erased uncertain cleanup")
	}
	base.State = StartupReady
	if base.Validate() == nil {
		t.Fatal("failure facts granted readiness")
	}
}

func TestImageStartupFailureKindRequiresSettledOriginalInput(t *testing.T) {
	base := ExecutionStartupObservation{State: StartupFailed, Phase: StartupInput, Harness: Codex, ProblemCode: Unsupported, CorrelationID: NewID(), InputDelivery: StartupNotSent, Cleanup: StartupCleanupConfirmed, FailureKind: StartupImageInputRejected}
	if base.Validate() != nil {
		t.Fatal("valid provenance rejected")
	}
	for _, change := range []func(*ExecutionStartupObservation){func(o *ExecutionStartupObservation) { o.FailureKind = 2 }, func(o *ExecutionStartupObservation) { o.State = StartupUncertain }, func(o *ExecutionStartupObservation) { o.Phase = StartupSettings }, func(o *ExecutionStartupObservation) { o.Harness = ClaudeCode }, func(o *ExecutionStartupObservation) { o.ProblemCode = Unavailable }, func(o *ExecutionStartupObservation) { o.InputDelivery = StartupClaimed }, func(o *ExecutionStartupObservation) { o.Cleanup = StartupCleanupUncertain }} {
		o := base
		change(&o)
		if o.Validate() == nil {
			t.Fatal("mixed provenance accepted")
		}
	}
	base.FailureKind = 0
	raw, err := json.Marshal(base)
	if err != nil || strings.Contains(string(raw), "failure_kind") {
		t.Fatal("legacy observation acquired a new field", err)
	}
}
