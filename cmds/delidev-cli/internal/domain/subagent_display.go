// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
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

// ParseSubagentDisplayNameJSON decodes one raw JSON value as an observed native
// name. The raw token must be a JSON string whose bytes are valid UTF-8 and whose
// \u escapes are well-formed: a lone surrogate escape is rejected before
// decoding, because encoding/json would otherwise replace it with U+FFFD and
// the replacement would pass ValidSubagentDisplayName. Any other value, including
// null, is rejected.
func ParseSubagentDisplayNameJSON(raw json.RawMessage) (string, bool) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' || !utf8.Valid(raw) || !wellFormedSurrogateEscapes(raw) {
		return "", false
	}
	var name string
	if json.Unmarshal(raw, &name) != nil || !ValidSubagentDisplayName(name) {
		return "", false
	}
	return name, true
}

// wellFormedSurrogateEscapes reports whether every \uXXXX escape in a JSON
// string token is a scalar value or a high surrogate directly followed by a
// \uXXXX low surrogate escape.
func wellFormedSurrogateEscapes(raw []byte) bool {
	unit := func(at int) (rune, bool) {
		if at+6 > len(raw) || raw[at] != '\\' || raw[at+1] != 'u' {
			return 0, false
		}
		var value rune
		for _, digit := range raw[at+2 : at+6] {
			switch {
			case digit >= '0' && digit <= '9':
				value = value<<4 | rune(digit-'0')
			case digit >= 'a' && digit <= 'f':
				value = value<<4 | rune(digit-'a'+10)
			case digit >= 'A' && digit <= 'F':
				value = value<<4 | rune(digit-'A'+10)
			default:
				return 0, false
			}
		}
		return value, true
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		if i+1 >= len(raw) {
			return false
		}
		if raw[i+1] != 'u' {
			i++
			continue
		}
		value, ok := unit(i)
		if !ok {
			return false
		}
		switch {
		case value >= 0xD800 && value <= 0xDBFF:
			low, ok := unit(i + 6)
			if !ok || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 11
		case value >= 0xDC00 && value <= 0xDFFF:
			return false
		default:
			i += 5
		}
	}
	return true
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
