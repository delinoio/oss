// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"unicode"
	"unicode/utf8"
)

// MaxSubagentDisplayNameRunes bounds an observed native agent name.
const MaxSubagentDisplayNameRunes = 256

// SubagentDisplayName is additive display metadata for one original product
// child. It travels beside, never inside, the closed schema-1 event and
// document JSON, so older readers keep receiving unchanged documents.
type SubagentDisplayName struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
}

// ValidSubagentDisplayName accepts only non-blank, well-formed, control-free text
// of at most MaxSubagentDisplayNameRunes Unicode characters. Names are inert
// display text and never grant identity or control authority.
func ValidSubagentDisplayName(name string) bool {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxSubagentDisplayNameRunes {
		return false
	}
	blank := true
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
		if !unicode.IsSpace(r) {
			blank = false
		}
	}
	return !blank
}

// ValidateSubagentDisplayNames checks the names of one publication against its
// child batch. Entries join by original product child ID, are bounded by the
// batch, and reject duplicate or foreign IDs and invalid names, so nothing is
// retained from a partially valid set.
func ValidateSubagentDisplayNames(names []SubagentDisplayName, batch []SubagentObservation) error {
	if len(names) == 0 {
		return nil
	}
	if len(names) > len(batch) || len(names) > 128 {
		return invalidSubagent()
	}
	children := make(map[ID]struct{}, len(batch))
	for _, child := range batch {
		children[child.ID] = struct{}{}
	}
	seen := make(map[ID]struct{}, len(names))
	for _, entry := range names {
		if _, ok := children[entry.ID]; !ok {
			return invalidSubagent()
		}
		if _, dup := seen[entry.ID]; dup || !ValidSubagentDisplayName(entry.Name) {
			return invalidSubagent()
		}
		seen[entry.ID] = struct{}{}
	}
	return nil
}
