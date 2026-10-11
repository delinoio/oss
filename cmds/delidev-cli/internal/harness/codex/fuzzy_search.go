// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type fuzzyMatchType string

const (
	fuzzyFile      fuzzyMatchType = "file"
	fuzzyDirectory fuzzyMatchType = "directory"
)

// Private wire records retain field presence and ordering until validation ends.
// They never become product results or path-read authority.
type fuzzyFileRecord struct {
	Root            *string         `json:"root"`
	Path            *string         `json:"path"`
	MatchType       *fuzzyMatchType `json:"match_type"`
	LegacyMatchType *fuzzyMatchType `json:"matchType"`
	FileName        *string         `json:"file_name"`
	Score           *uint32         `json:"score"`
	Indices         json.RawMessage `json:"indices"`
}
type fuzzySearchObservation struct {
	SessionID *string         `json:"sessionId"`
	Query     *string         `json:"query"`
	Files     json.RawMessage `json:"files"`
}

func decodeFuzzySearch(native nativewire.Event, frameLimit int) (fuzzySearchObservation, error) {
	var observation fuzzySearchObservation
	fail := func() (fuzzySearchObservation, error) { return fuzzySearchObservation{}, incompatible() }
	if !fuzzyFields(native.Params, frameLimit, "sessionId", "query", "files") || domain.DecodeBounded(native.Params, &observation, frameLimit) != nil || observation.SessionID == nil || domain.Text(*observation.SessionID, "native search session", 1024, true) != nil {
		return fail()
	}
	if native.Method == "fuzzyFileSearch/sessionCompleted" {
		if observation.Query != nil || len(observation.Files) != 0 {
			return fail()
		}
		// A present null query must not acquire the completed envelope.
		var completed struct {
			SessionID *string `json:"sessionId"`
		}
		if domain.DecodeBounded(native.Params, &completed, frameLimit) != nil {
			return fail()
		}
		return observation, nil
	}
	if native.Method != "fuzzyFileSearch/sessionUpdated" || observation.Query == nil || domain.Text(*observation.Query, "native search query", 4096, false) != nil {
		return fail()
	}
	var records []json.RawMessage
	if domain.DecodeBounded(observation.Files, &records, frameLimit) != nil || records == nil || len(records) > 1000 {
		return fail()
	}
	for _, raw := range records {
		var file fuzzyFileRecord
		if !fuzzyFields(raw, frameLimit, "root", "path", "file_name", "score", "indices", "match_type", "matchType") || domain.DecodeBounded(raw, &file, frameLimit) != nil || file.Root == nil || file.Path == nil || file.FileName == nil || file.Score == nil {
			return fail()
		}
		for _, value := range []*string{file.Root, file.Path, file.FileName} {
			if domain.Text(*value, "native search descriptor", 4096, false) != nil {
				return fail()
			}
		}
		// Pinned official schemas use match_type. The earlier admitted fixture uses
		// matchType; accept one spelling only until that compatibility profile retires.
		var fields map[string]json.RawMessage
		if domain.DecodeBounded(raw, &fields, frameLimit) != nil {
			return fail()
		}
		_, official := fields["match_type"]
		_, legacy := fields["matchType"]
		if official == legacy {
			return fail()
		}
		kind := file.MatchType
		if legacy {
			kind = file.LegacyMatchType
		}
		if kind == nil || (*kind != fuzzyFile && *kind != fuzzyDirectory) {
			return fail()
		}
		if len(file.Indices) != 0 && string(file.Indices) != "null" {
			var indices []*uint32
			if domain.DecodeBounded(file.Indices, &indices, frameLimit) != nil || indices == nil || len(indices) > 4096 {
				return fail()
			}
			for _, index := range indices {
				if index == nil {
					return fail()
				}
			}
		}
	}
	return observation, nil
}

func fuzzyFields(raw json.RawMessage, limit int, allowed ...string) bool {
	var fields map[string]json.RawMessage
	if domain.DecodeBounded(raw, &fields, limit) != nil || fields == nil {
		return false
	}
	for key := range fields {
		if !slices.Contains(allowed, key) {
			return false
		}
	}
	return true
}
