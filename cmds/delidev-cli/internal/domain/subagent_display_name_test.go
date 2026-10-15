// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
)

func TestSubagentDisplayNameBounds(t *testing.T) {
	for name, valid := range map[string]bool{
		"Atlas":                     true,
		strings.Repeat("가", 256):    true,
		strings.Repeat("가", 257):    false,
		"":                          false,
		" \t ":                      false,
		"a\x00b":                    false,
		"line\nbreak":               false,
		string([]byte{0xff, 0xfe}):  false,
		"Unicode 🚀 and combining é": true,
	} {
		if ValidSubagentDisplayName(name) != valid {
			t.Fatalf("name %q validity changed", name)
		}
	}
}

func TestSubagentDisplayNamesJoinOnlyTheirBatch(t *testing.T) {
	a, b := SubagentObservation{ID: NewID()}, SubagentObservation{ID: NewID()}
	batch := []SubagentObservation{a, b}
	if err := ValidateSubagentDisplayNames(batch, []SubagentDisplayName{{ChildID: a.ID, Name: "Same"}, {ChildID: b.ID, Name: "Same"}}); err != nil {
		t.Fatal("equal names for separate children rejected", err)
	}
	for name, names := range map[string][]SubagentDisplayName{
		"duplicate": {{ChildID: a.ID, Name: "x"}, {ChildID: a.ID, Name: "y"}},
		"foreign":   {{ChildID: NewID(), Name: "x"}},
		"oversized": {{ChildID: a.ID, Name: "x"}, {ChildID: b.ID, Name: "y"}, {ChildID: NewID(), Name: "z"}},
		"invalid":   {{ChildID: a.ID, Name: " "}},
		"bad id":    {{ChildID: "native", Name: "x"}},
	} {
		if ValidateSubagentDisplayNames(batch, names) == nil {
			t.Fatalf("%s metadata accepted", name)
		}
	}
	event := ExecutionEvent{Kind: ExecutionInputAccepted, ChildNames: []SubagentDisplayName{{ChildID: a.ID, Name: "x"}}}
	if event.Validate() == nil {
		t.Fatal("names accepted on another event kind")
	}
}
