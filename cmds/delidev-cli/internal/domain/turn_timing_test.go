// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTurnTimingNeverEntersNativeAssignmentProjection(t *testing.T) {
	accepted := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	terminal := accepted.Add(12 * time.Second)
	original := ExecutionProgress{ExecutionID: NewID(), InputID: NewID(), JobID: NewID(), LastSequence: 9, NativeThreadID: "original-thread", NativeTurnID: "original-turn", Outcome: ExecutionSucceeded, CleanupVerified: true, TurnTiming: &TurnTiming{AcceptedAt: accepted, TerminalAt: &terminal}}
	native := original.NativePublication()
	expected := original
	expected.TurnTiming = nil
	if !reflect.DeepEqual(native, expected) || original.TurnTiming == nil {
		t.Fatal("native projection changed original timing or publication proof")
	}
	for _, value := range []any{native, ExecutionContinuation{Previous: native}, ForkJobInput{Progress: native}} {
		raw, err := json.Marshal(value)
		if err != nil || strings.Contains(string(raw), "turn_timing") {
			t.Fatal("display timing entered a closed older-Worker native shape", err)
		}
	}
	raw, _ := json.Marshal(original)
	var retained ExecutionProgress
	if Decode(raw, &retained) != nil || !reflect.DeepEqual(retained.TurnTiming, original.TurnTiming) {
		t.Fatal("resource timing failed to retain its original observations")
	}
}
