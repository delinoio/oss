// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type fuzzySearchMatchType string

const (
	fuzzySearchFile      fuzzySearchMatchType = "file"
	fuzzySearchDirectory fuzzySearchMatchType = "directory"
)

type fuzzySearchResult struct {
	Root      *string               `json:"root"`
	Path      *string               `json:"path"`
	MatchType *fuzzySearchMatchType `json:"match_type"`
	FileName  *string               `json:"file_name"`
	Score     *uint32               `json:"score"`
	// Raw presence distinguishes omitted indices from explicit null. Neither
	// representation grants path access or a highlight/result owner.
	Indices json.RawMessage `json:"indices"`
}

// Check exact field spelling as well as the closed typed decoder. Go's JSON
// struct matching alone accepts case variants that the native schema does not.
func fuzzySearchFields(raw []byte, allowed ...string) bool {
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

func (c *Client) observeFuzzySearchSessionLocked(native nativewire.Event) (Event, error) {
	if native.Method == "fuzzyFileSearch/sessionCompleted" {
		var completed struct {
			SessionID *string `json:"sessionId"`
		}
		if !fuzzySearchFields(native.Params, "sessionId") || domain.Decode(native.Params, &completed) != nil || completed.SessionID == nil || domain.Text(*completed.SessionID, "native search session", 1024, true) != nil {
			return Event{}, incompatible()
		}
	} else {
		var updated struct {
			SessionID *string           `json:"sessionId"`
			Query     *string           `json:"query"`
			Files     []json.RawMessage `json:"files"`
		}
		if !fuzzySearchFields(native.Params, "sessionId", "query", "files") || domain.Decode(native.Params, &updated) != nil || updated.SessionID == nil || updated.Query == nil || domain.Text(*updated.SessionID, "native search session", 1024, true) != nil || domain.Text(*updated.Query, "native search query", 4096, false) != nil || updated.Files == nil || len(updated.Files) > 1000 {
			return Event{}, incompatible()
		}
		for _, raw := range updated.Files {
			var result fuzzySearchResult
			if !fuzzySearchFields(raw, "root", "path", "match_type", "file_name", "score", "indices") || domain.Decode(raw, &result) != nil || result.Root == nil || result.Path == nil || result.FileName == nil || result.MatchType == nil || result.Score == nil || !slices.Contains([]fuzzySearchMatchType{fuzzySearchFile, fuzzySearchDirectory}, *result.MatchType) {
				return Event{}, incompatible()
			}
			for _, value := range []*string{result.Root, result.Path, result.FileName} {
				if domain.Text(*value, "native search result", 4096, false) != nil {
					return Event{}, incompatible()
				}
			}
			if len(result.Indices) != 0 && !bytes.Equal(bytes.TrimSpace(result.Indices), []byte("null")) {
				var indices []*uint32
				if domain.Decode(result.Indices, &indices) != nil || indices == nil || len(indices) > 4096 {
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
	// No search operation was admitted by this adapter. Validate and discard
	// all sessions/query generations, including completion before an update.
	// Future consumers need their own original request and generation proof.
	return c.metadata(FuzzySearchSessionDiscarded), nil
}
