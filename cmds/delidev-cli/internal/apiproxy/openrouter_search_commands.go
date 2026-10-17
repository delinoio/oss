// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"encoding/json"
	"slices"
)

// The closed schema follows Codex rust-v0.159.2 codex-api/src/search.rs.
// Validate without rebuilding the commands, so original integers, references
// and optional fields survive projection into the dedicated tool request.
type searchFieldKind uint8

const (
	searchString searchFieldKind = iota
	searchUnsigned
	searchStrings
)

type searchField struct {
	kind     searchFieldKind
	required bool
	choices  []string
}

func searchText(required bool, choices ...string) searchField {
	return searchField{kind: searchString, required: required, choices: choices}
}
func validNativeSearchCommands(commands map[string]json.RawMessage) bool {
	query := map[string]searchField{"q": searchText(true), "recency": {kind: searchUnsigned}, "domains": {kind: searchStrings}}
	schemas := map[string]map[string]searchField{
		"search_query": query, "image_query": query,
		"open":       {"ref_id": searchText(true), "lineno": {kind: searchUnsigned}},
		"click":      {"ref_id": searchText(true), "id": {kind: searchUnsigned, required: true}},
		"find":       {"ref_id": searchText(true), "pattern": searchText(true)},
		"screenshot": {"ref_id": searchText(true), "pageno": {kind: searchUnsigned, required: true}},
		"finance":    {"ticker": searchText(true), "type": searchText(true, "equity", "fund", "crypto", "index"), "market": searchText(false)},
		"weather":    {"location": searchText(true), "start": searchText(false), "duration": {kind: searchUnsigned}},
		"sports":     {"tool": searchText(false, "sports"), "fn": searchText(true, "schedule", "standings"), "league": searchText(true, "nba", "wnba", "nfl", "nhl", "mlb", "epl", "ncaamb", "ncaawb", "ipl"), "team": searchText(false), "opponent": searchText(false), "date_from": searchText(false), "date_to": searchText(false), "num_games": {kind: searchUnsigned}, "locale": searchText(false)},
		"time":       {"utc_offset": searchText(true)},
	}
	if len(commands) == 0 || len(commands) > len(schemas)+1 {
		return false
	}
	for command, raw := range commands {
		schema, known := schemas[command]
		if command == "response_length" {
			if externalJSON(raw) && !validSearchField(raw, searchText(true, "short", "medium", "long")) {
				return false
			}
			continue
		}
		if !known {
			return false
		}
		// Native Option<Vec<T>> permits null and empty arrays.
		if !externalJSON(raw) {
			continue
		}
		var operations []map[string]json.RawMessage
		if json.Unmarshal(raw, &operations) != nil {
			return false
		}
		for _, operation := range operations {
			if operation == nil {
				return false
			}
			for field, value := range operation {
				rule, ok := schema[field]
				if !ok || !validSearchField(value, rule) {
					return false
				}
			}
			for field, rule := range schema {
				if rule.required && !externalJSON(operation[field]) {
					return false
				}
			}
		}
	}
	return true
}
func validSearchField(raw json.RawMessage, field searchField) bool {
	if !externalJSON(raw) {
		return !field.required
	}
	switch field.kind {
	case searchString:
		var value string
		return json.Unmarshal(raw, &value) == nil && (len(field.choices) == 0 || slices.Contains(field.choices, value))
	case searchUnsigned:
		var value uint64
		return json.Unmarshal(raw, &value) == nil
	case searchStrings:
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return false
		}
		for _, value := range values {
			if !validSearchField(value, searchText(true)) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
