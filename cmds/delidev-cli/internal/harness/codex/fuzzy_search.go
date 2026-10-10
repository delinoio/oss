// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type fuzzyMatchType string

const (
	fuzzyFile       fuzzyMatchType = "file"
	fuzzyDirectory  fuzzyMatchType = "directory"
	maxFuzzyFiles                  = 1024
	maxFuzzyIndices                = 4096
)

type fuzzyFileWire struct {
	Root      *string         `json:"root"`
	Path      *string         `json:"path"`
	MatchType *fuzzyMatchType `json:"match_type"`
	FileName  *string         `json:"file_name"`
	Score     *uint32         `json:"score"`
	// Keep omitted, null and ordered arrays distinct until private discard.
	Indices json.RawMessage `json:"indices,omitempty"`
}

func (c *Client) observeFuzzySearchLocked(native nativewire.Event) (Event, error) {
	if native.Method == "fuzzyFileSearch/sessionCompleted" {
		if !fuzzyFields(native.Params, "sessionId") {
			return Event{}, incompatible()
		}
		var params *struct {
			SessionID *string `json:"sessionId"`
		}
		if domain.Decode(native.Params, &params) != nil || params == nil || params.SessionID == nil || domain.Text(*params.SessionID, "native search identity", 1024, true) != nil {
			return Event{}, incompatible()
		}
	} else {
		if !fuzzyFields(native.Params, "sessionId", "query", "files") {
			return Event{}, incompatible()
		}
		var params *struct {
			SessionID *string           `json:"sessionId"`
			Query     *string           `json:"query"`
			Files     []json.RawMessage `json:"files"`
		}
		if domain.Decode(native.Params, &params) != nil || params == nil || params.SessionID == nil || domain.Text(*params.SessionID, "native search identity", 1024, true) != nil || params.Query == nil || domain.Text(*params.Query, "native search query", 4096, false) != nil || params.Files == nil || len(params.Files) > maxFuzzyFiles {
			return Event{}, incompatible()
		}
		for _, raw := range params.Files {
			var file *fuzzyFileWire
			if !fuzzyFields(raw, "root", "path", "match_type", "file_name", "score", "indices") || domain.Decode(raw, &file) != nil {
				return Event{}, incompatible()
			}
			if file == nil || file.Root == nil || file.Path == nil || file.FileName == nil || file.Score == nil || file.MatchType == nil || (*file.MatchType != fuzzyFile && *file.MatchType != fuzzyDirectory) || domain.Text(*file.Root, "native search root", 4096, true) != nil || domain.Text(*file.Path, "native search path", 4096, true) != nil || domain.Text(*file.FileName, "native search name", 4096, true) != nil {
				return Event{}, incompatible()
			}
			if len(file.Indices) != 0 && strings.TrimSpace(string(file.Indices)) != "null" {
				var indices []*uint32
				if domain.Decode(file.Indices, &indices) != nil || indices == nil || len(indices) > maxFuzzyIndices {
					return Event{}, incompatible()
				}
				for _, index := range indices {
					if index == nil {
						return Event{}, incompatible()
					}
				}
			}
		}
	}
	// No product search operation admits a native session/query owner. Validate
	// and discard every result, including foreign/stale identities, without
	// reading paths, indexing conversation content or changing the original turn.
	return c.metadata(FuzzySearchDiscarded), nil
}

// encoding/json accepts case aliases for tagged struct fields. The native
// profile accepts only the official spelling before typed decoding.
func fuzzyFields(raw []byte, allowed ...string) bool {
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil || fields == nil {
		return false
	}
	for key := range fields {
		if !slices.Contains(allowed, key) {
			return false
		}
	}
	return true
}
