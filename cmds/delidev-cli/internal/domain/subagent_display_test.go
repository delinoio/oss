// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
)

func TestValidSubagentDisplayName(t *testing.T) {
	for name, want := range map[string]bool{
		"Atlas":                  true,
		"아틀라스":                   true,
		"a b":                    true,
		strings.Repeat("가", 256): true,
		strings.Repeat("가", 257): false,
		"":                       false,
		"  \t":                   false,
		"a\nb":                   false,
		"a\x00b":                 false,
		"\xff":                   false,
	} {
		if got := ValidSubagentDisplayName(name); got != want {
			t.Fatalf("%q accepted = %v, want %v", name, got, want)
		}
	}
}

func TestSubagentDisplayNamesJoinOriginalChildren(t *testing.T) {
	a, b := SubagentObservation{ID: NewID()}, SubagentObservation{ID: NewID()}
	batch := []SubagentObservation{a, b}
	if err := ValidateSubagentDisplayNames(nil, batch); err != nil {
		t.Fatal("historical nameless batch rejected", err)
	}
	if err := ValidateSubagentDisplayNames([]SubagentDisplayName{{ID: a.ID, Name: "Same"}, {ID: b.ID, Name: "Same"}}, batch); err != nil {
		t.Fatal("equal names must remain separate children", err)
	}
	for name, names := range map[string][]SubagentDisplayName{
		"foreign":   {{ID: NewID(), Name: "Atlas"}},
		"duplicate": {{ID: a.ID, Name: "A"}, {ID: a.ID, Name: "B"}},
		"blank":     {{ID: a.ID, Name: " "}},
		"unbounded": {{ID: a.ID, Name: "A"}, {ID: b.ID, Name: "B"}, {ID: NewID(), Name: "C"}},
	} {
		if ValidateSubagentDisplayNames(names, batch) == nil {
			t.Fatalf("%s names accepted", name)
		}
	}
	event := ExecutionEvent{Kind: ExecutionTurnFinished, SubagentNames: []SubagentDisplayName{{ID: a.ID, Name: "Atlas"}}}
	if event.Validate() == nil {
		t.Fatal("names on a non-child event accepted")
	}
}
