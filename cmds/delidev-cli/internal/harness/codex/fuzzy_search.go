// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

const (
	maxFuzzySearchFiles   = 1000
	maxFuzzySearchIndices = 4096
	maxFuzzySearchText    = 4096
)

type fuzzySearchMatchType string

const (
	fuzzySearchFile      fuzzySearchMatchType = "file"
	fuzzySearchDirectory fuzzySearchMatchType = "directory"
)

// Keep absent, explicit null and ordered index arrays distinct while validating
// the private native payload. None of these values is published or retained.
type fuzzySearchIndices struct {
	Present bool
	Null    bool
	Values  []uint32
}

func (v *fuzzySearchIndices) UnmarshalJSON(raw []byte) error {
	*v = fuzzySearchIndices{Present: true}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		v.Null = true
		return nil
	}
	var values []*uint32
	if json.Unmarshal(raw, &values) != nil || values == nil || len(values) > maxFuzzySearchIndices {
		return incompatible()
	}
	v.Values = make([]uint32, len(values))
	for i, value := range values {
		if value == nil {
			return incompatible()
		}
		v.Values[i] = *value
	}
	return nil
}

type fuzzySearchResult struct {
	Root      *string              `json:"root"`
	Path      *string              `json:"path"`
	MatchType fuzzySearchMatchType `json:"match_type"`
	FileName  *string              `json:"file_name"`
	Score     *uint32              `json:"score"`
	Indices   fuzzySearchIndices   `json:"indices"`
}

// encoding/json otherwise matches struct fields case-insensitively. Check the
// exact native key set before typed decoding so aliases cannot gain support.
func fuzzySearchObject(raw []byte, required []string, optional string) error {
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil || fields == nil {
		return incompatible()
	}
	for _, key := range required {
		if value, ok := fields[key]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return incompatible()
		}
	}
	for key := range fields {
		known := optional != "" && key == optional
		for _, expected := range required {
			known = known || key == expected
		}
		if !known {
			return incompatible()
		}
	}
	return nil
}

func (v *fuzzySearchResult) UnmarshalJSON(raw []byte) error {
	if fuzzySearchObject(raw, []string{"root", "path", "match_type", "file_name", "score"}, "indices") != nil {
		return incompatible()
	}
	type wireResult fuzzySearchResult
	return domain.Decode(raw, (*wireResult)(v))
}

type fuzzySearchUpdate struct {
	SessionID *string              `json:"sessionId"`
	Query     *string              `json:"query"`
	Files     *[]fuzzySearchResult `json:"files"`
}

func validFuzzySearchText(value *string, limit int, required bool) bool {
	return value != nil && domain.Text(*value, "native search text", limit, required) == nil
}

func decodeFuzzySearchUpdate(raw []byte) (fuzzySearchUpdate, error) {
	var value fuzzySearchUpdate
	if fuzzySearchObject(raw, []string{"sessionId", "query", "files"}, "") != nil || domain.Decode(raw, &value) != nil ||
		!validFuzzySearchText(value.SessionID, 1024, true) ||
		!validFuzzySearchText(value.Query, maxFuzzySearchText, false) ||
		value.Files == nil || len(*value.Files) > maxFuzzySearchFiles {
		return fuzzySearchUpdate{}, incompatible()
	}
	for _, file := range *value.Files {
		if !validFuzzySearchText(file.Root, maxFuzzySearchText, false) ||
			!validFuzzySearchText(file.Path, maxFuzzySearchText, false) ||
			!validFuzzySearchText(file.FileName, maxFuzzySearchText, false) ||
			file.Score == nil || (file.MatchType != fuzzySearchFile && file.MatchType != fuzzySearchDirectory) {
			return fuzzySearchUpdate{}, incompatible()
		}
	}
	return value, nil
}

func (c *Client) observeFuzzySearchLocked(native nativewire.Event) (Event, error) {
	switch native.Method {
	case "fuzzyFileSearch/sessionUpdated":
		if _, err := decodeFuzzySearchUpdate(native.Params); err != nil {
			return Event{}, err
		}
	case "fuzzyFileSearch/sessionCompleted":
		var value struct {
			SessionID *string `json:"sessionId"`
		}
		if fuzzySearchObject(native.Params, []string{"sessionId"}, "") != nil || domain.Decode(native.Params, &value) != nil || !validFuzzySearchText(value.SessionID, 1024, true) {
			return Event{}, incompatible()
		}
	default:
		return Event{}, incompatible()
	}
	// This adapter has no admitted search-session/query-generation owner.
	// Even well-formed native results, foreign IDs and completion-before-update
	// cannot create one. Discard them without reading paths or changing input.
	return c.metadata(FuzzySearchDiscarded), nil
}
