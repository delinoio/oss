// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestSleepDurationRetainsUint64AndRejectsMixedToolAuthority(t *testing.T) {
	duration := uint64(18446744073709551615)
	s := ToolSnapshot{Kind: SleepTool, Status: ToolRunning, Sleep: &SleepObservation{DurationMS: &duration}}
	raw, _ := json.Marshal(s)
	if !bytes.Contains(raw, []byte(`"duration_ms":"18446744073709551615"`)) {
		t.Fatal("duration lost exact JSON representation")
	}
	var decoded ToolSnapshot
	if Decode(raw, &decoded) != nil || decoded.Validate() != nil || *decoded.Sleep.DurationMS != duration {
		t.Fatal("duration failed exact round trip")
	}
	for _, mutate := range []func(*ToolSnapshot){func(s *ToolSnapshot) { s.Sleep.DurationMS = nil }, func(s *ToolSnapshot) { s.Kind = CommandTool }, func(s *ToolSnapshot) { s.Status = ToolFailed }, func(s *ToolSnapshot) { s.Command = &CommandObservation{} }, func(s *ToolSnapshot) { s.Changes = []FileChangeObservation{} }} {
		copy := s
		copy.Sleep = &SleepObservation{DurationMS: &duration}
		mutate(&copy)
		if copy.Validate() == nil {
			t.Fatal("sleep accepted mixed ownership")
		}
	}
}
