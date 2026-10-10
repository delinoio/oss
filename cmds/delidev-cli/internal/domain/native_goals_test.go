// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeGoalSnapshotPreservesExactCountersAndNativeStates(t *testing.T) {
	budget := "9223372036854775807"
	g := NativeGoalSnapshot{Objective: strings.Repeat("한", 4000), Status: NativeGoalBudgetLimited, TokenBudget: &budget, TokensUsed: budget, TimeUsedSeconds: "0", CreatedAt: "1", UpdatedAt: "2"}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*NativeGoalSnapshot){func(v *NativeGoalSnapshot) { v.TokensUsed = "01" }, func(v *NativeGoalSnapshot) { v.TokensUsed = "9223372036854775808" }, func(v *NativeGoalSnapshot) { v.UpdatedAt = "0" }, func(v *NativeGoalSnapshot) { v.Status = "succeeded" }, func(v *NativeGoalSnapshot) { v.Objective += "a" }} {
		copy := g
		mutate(&copy)
		if copy.Validate() == nil {
			t.Fatalf("invalid goal accepted: %q", copy.Status)
		}
	}
}
func TestNativeGoalSetBudgetTriState(t *testing.T) {
	objective := "Retain the original objective."
	for _, raw := range []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage("1"), json.RawMessage("9223372036854775807")} {
		if (NativeGoalSet{Objective: &objective, TokenBudget: raw}).Validate() != nil {
			t.Fatalf("valid budget rejected: %s", raw)
		}
	}
	for _, raw := range []json.RawMessage{json.RawMessage("0"), json.RawMessage("-1"), json.RawMessage("1.5"), json.RawMessage("9223372036854775808"), json.RawMessage(`"1"`)} {
		if (NativeGoalSet{Objective: &objective, TokenBudget: raw}).Validate() == nil {
			t.Fatalf("invalid budget accepted: %s", raw)
		}
	}
	if (NativeGoalSet{}).Validate() == nil {
		t.Fatal("empty native set accepted")
	}
}
func TestNativeGoalActionRetainsOriginalClaimScope(t *testing.T) {
	a := NativeGoalActionInput{Version: 1, RequestID: NewID(), ExecutionJobID: NewID(), ExecutionID: NewID(), NativeThreadID: NewID(), Command: NativeGoalClearCommand}
	if a.Validate() != nil {
		t.Fatal("valid clear rejected")
	}
	a.Set = &NativeGoalSet{}
	if a.Validate() == nil {
		t.Fatal("clear accepted set payload")
	}
	a.Set = nil
	a.ClaimID = "not-an-original-id"
	if a.Validate() == nil {
		t.Fatal("invalid claim accepted")
	}
}
