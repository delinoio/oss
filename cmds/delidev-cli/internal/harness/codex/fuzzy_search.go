// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type fuzzyMatchType string

const (
	fuzzyFile      fuzzyMatchType = "file"
	fuzzyDirectory fuzzyMatchType = "directory"
)

// Private wire values retain native spelling, result order and indices presence.
// This type has no filesystem reader, native sender or product publisher.
type fuzzyFileRecord struct {
	Root      *string         `json:"root"`
	Path      *string         `json:"path"`
	MatchType fuzzyMatchType  `json:"match_type"`
	FileName  *string         `json:"file_name"`
	Score     *uint32         `json:"score"`
	Indices   json.RawMessage `json:"indices"`
}
type fuzzySearchObservation struct {
	SessionID string
	Query     *string
	Files     []fuzzyFileRecord
}

// Reject case aliases too: encoding/json otherwise accepts differently cased
// names even with DisallowUnknownFields. Native wire spelling is exact here.
func fuzzyFields(raw json.RawMessage, required []string, optional ...string) bool {
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil || fields == nil {
		return false
	}
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, key := range required {
		allowed[key] = true
		if _, ok := fields[key]; !ok {
			return false
		}
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key := range fields {
		if !allowed[key] {
			return false
		}
	}
	return true
}
func decodeFuzzySearch(method string, raw json.RawMessage) (fuzzySearchObservation, error) {
	var result fuzzySearchObservation
	if method == "fuzzyFileSearch/sessionCompleted" {
		var params struct {
			SessionID *string `json:"sessionId"`
		}
		if !fuzzyFields(raw, []string{"sessionId"}) || domain.Decode(raw, &params) != nil || params.SessionID == nil || domain.Text(*params.SessionID, "native search identity", 1024, true) != nil {
			return result, incompatible()
		}
		result.SessionID = *params.SessionID
		return result, nil
	}
	if method != "fuzzyFileSearch/sessionUpdated" {
		return result, incompatible()
	}
	var params struct {
		SessionID *string           `json:"sessionId"`
		Query     *string           `json:"query"`
		Files     []json.RawMessage `json:"files"`
	}
	if !fuzzyFields(raw, []string{"sessionId", "query", "files"}) || domain.Decode(raw, &params) != nil || params.SessionID == nil || params.Query == nil || params.Files == nil || len(params.Files) > 1000 || domain.Text(*params.SessionID, "native search identity", 1024, true) != nil || domain.Text(*params.Query, "native search query", 4096, false) != nil {
		return result, incompatible()
	}
	result.SessionID, result.Query = *params.SessionID, params.Query
	result.Files = make([]fuzzyFileRecord, 0, len(params.Files))
	for _, rawFile := range params.Files {
		var file fuzzyFileRecord
		if !fuzzyFields(rawFile, []string{"root", "path", "match_type", "file_name", "score"}, "indices") || domain.Decode(rawFile, &file) != nil || file.Root == nil || file.Path == nil || file.FileName == nil || file.Score == nil || (file.MatchType != fuzzyFile && file.MatchType != fuzzyDirectory) {
			return fuzzySearchObservation{}, incompatible()
		}
		for _, text := range []*string{file.Root, file.Path, file.FileName} {
			if domain.Text(*text, "native search file metadata", 4096, false) != nil {
				return fuzzySearchObservation{}, incompatible()
			}
		}
		if len(file.Indices) != 0 && !bytes.Equal(bytes.TrimSpace(file.Indices), []byte("null")) {
			var indices []*uint32
			if domain.Decode(file.Indices, &indices) != nil || indices == nil || len(indices) > 4096 {
				return fuzzySearchObservation{}, incompatible()
			}
			for _, index := range indices {
				if index == nil {
					return fuzzySearchObservation{}, incompatible()
				}
			}
		}
		result.Files = append(result.Files, file)
	}
	return result, nil
}
