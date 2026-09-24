package domain

import (
	"slices"
	"strings"
	"testing"
)

func TestExecutionInputBindingsPreserveExactOrderedEvidence(t *testing.T) {
	first := BindExecutionInput(NewID(), "Primary 한글 🐦")
	later := BindExecutionInput(NewID(), "Same-turn follow-up")
	for _, inputs := range [][]ExecutionInputBinding{nil, {first}, {first, later}} {
		got, err := CheckedExecutionInputs(first.InputID, first.PromptDigest, inputs)
		if err != nil || got[0] != first || (inputs != nil && !slices.Equal(got, inputs)) {
			t.Fatal("valid input evidence changed", err)
		}
		got[0].InputID = NewID()
		if inputs != nil && inputs[0] != first {
			t.Fatal("caller can mutate retained evidence through result")
		}
	}
	for _, inputs := range [][]ExecutionInputBinding{
		{}, {later, first}, {first, first},
		{first, {InputID: "invalid", PromptDigest: later.PromptDigest}},
		{first, {InputID: later.InputID, PromptDigest: strings.ToUpper(later.PromptDigest)}},
		{first, {InputID: later.InputID, PromptDigest: "ab"}},
	} {
		if _, err := CheckedExecutionInputs(first.InputID, first.PromptDigest, inputs); err == nil {
			t.Fatal("invalid accepted input evidence passed")
		}
	}
	inputs := []ExecutionInputBinding{first}
	for len(inputs) < MaxAcceptedExecutionInputs {
		inputs = append(inputs, BindExecutionInput(NewID(), "Bounded follow-up"))
	}
	if _, err := CheckedExecutionInputs(first.InputID, first.PromptDigest, inputs); err != nil {
		t.Fatal(err)
	}
	inputs = append(inputs, later)
	if _, err := CheckedExecutionInputs(first.InputID, first.PromptDigest, inputs); err == nil {
		t.Fatal("oversized history was silently truncated")
	}
}
