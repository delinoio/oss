package claude

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type CitationKind string
type NativeCitationCompletion string

const (
	CharacterCitation        CitationKind             = "char_location"
	PageCitation             CitationKind             = "page_location"
	BlockCitation            CitationKind             = "content_block_location"
	SearchCitation           CitationKind             = "search_result_location"
	WebCitation              CitationKind             = "web_search_result_location"
	CitationsDelta           ContentDeltaKind         = "citations_delta"
	CitationsMatched         NativeCitationCompletion = "matched"
	CitationsOmittedByNative NativeCitationCompletion = "omitted-by-native"
)

// NativeCitation locations refer to the native source, not to local files or
// DeliDev transcript positions. No source/index grants retrieval authority.
type NativeCitation struct {
	Kind          CitationKind
	Text          string
	DocumentIndex *uint64
	SearchIndex   *uint64
	Start         *uint64
	End           *uint64
	Title         *string
	FileID        *string
	Source        *string
	URL           *string
	// Encrypted web indices stay opaque. Exact bytes also retain missing/null
	// optional fields independently of display projections.
	Native json.RawMessage `json:"-"`
}

type NativeCitations struct {
	Null    bool
	Entries []NativeCitation
}

func decodeCitations(raw json.RawMessage) (*NativeCitations, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if absentOrNull(raw) {
		return &NativeCitations{Null: true}, nil
	}
	var entries []json.RawMessage
	if domain.Decode(raw, &entries) != nil || entries == nil || len(entries) > 1024 {
		return nil, lifecycleUncertain()
	}
	result := &NativeCitations{Entries: make([]NativeCitation, 0, len(entries))}
	for _, entry := range entries {
		citation, err := decodeCitation(entry)
		if err != nil {
			return nil, err
		}
		result.Entries = append(result.Entries, citation)
	}
	return result, nil
}

func decodeCitation(raw json.RawMessage) (NativeCitation, error) {
	var fields map[string]json.RawMessage
	var kind CitationKind
	if domain.Decode(raw, &fields) != nil || json.Unmarshal(fields["type"], &kind) != nil {
		return NativeCitation{}, lifecycleUncertain()
	}
	allowed := []string{"type", "cited_text"}
	start, end, index, title := "", "", "", "document_title"
	switch kind {
	case CharacterCitation:
		start, end, index = "start_char_index", "end_char_index", "document_index"
	case PageCitation:
		start, end, index = "start_page_number", "end_page_number", "document_index"
	case BlockCitation:
		start, end, index = "start_block_index", "end_block_index", "document_index"
	case SearchCitation:
		start, end, index, title = "start_block_index", "end_block_index", "search_result_index", "title"
		allowed = append(allowed, "source")
	case WebCitation:
		title = "title"
		allowed = append(allowed, "url", "encrypted_index")
	default:
		return NativeCitation{}, lifecycleUncertain()
	}
	allowed = append(allowed, title)
	if index != "" {
		allowed = append(allowed, start, end, index)
	}
	if index == "document_index" {
		allowed = append(allowed, "file_id")
	}
	for key := range fields {
		if !slices.Contains(allowed, key) {
			return NativeCitation{}, lifecycleUncertain()
		}
	}
	result := NativeCitation{Kind: kind, Native: bytes.Clone(raw)}
	var text *string
	if json.Unmarshal(fields["cited_text"], &text) != nil || text == nil || domain.Text(*text, "native cited text", domain.MaxMessageText, false) != nil || fields[title] == nil || json.Unmarshal(fields[title], &result.Title) != nil || (result.Title != nil && domain.Text(*result.Title, "native citation title", 16<<10, false) != nil) {
		return NativeCitation{}, lifecycleUncertain()
	}
	result.Text = *text
	if index != "" {
		var position *uint64
		if json.Unmarshal(fields[index], &position) != nil || position == nil || json.Unmarshal(fields[start], &result.Start) != nil || result.Start == nil || json.Unmarshal(fields[end], &result.End) != nil || result.End == nil || *result.End <= *result.Start || (kind == PageCitation && *result.Start == 0) {
			return NativeCitation{}, lifecycleUncertain()
		}
		if index == "document_index" {
			result.DocumentIndex = position
			if fields["file_id"] != nil && (json.Unmarshal(fields["file_id"], &result.FileID) != nil || (result.FileID != nil && domain.Text(*result.FileID, "native citation file identity", 1024, true) != nil)) {
				return NativeCitation{}, lifecycleUncertain()
			}
		} else {
			result.SearchIndex = position
		}
	}
	for key, target := range map[string]**string{"source": &result.Source, "url": &result.URL} {
		if !slices.Contains(allowed, key) {
			continue
		}
		if json.Unmarshal(fields[key], target) != nil || *target == nil || domain.Text(**target, "native citation source", 16<<10, true) != nil {
			return NativeCitation{}, lifecycleUncertain()
		}
	}
	if kind == WebCitation {
		var encrypted *string
		if json.Unmarshal(fields["encrypted_index"], &encrypted) != nil || encrypted == nil || domain.Text(*encrypted, "native citation index", 128<<10, true) != nil {
			return NativeCitation{}, lifecycleUncertain()
		}
	}
	return result, nil
}

func citationDigests(citations *NativeCitations) ([][sha256.Size]byte, error) {
	if citations == nil || citations.Null {
		return nil, nil
	}
	result := make([][sha256.Size]byte, 0, len(citations.Entries))
	for _, citation := range citations.Entries {
		digest, err := streamReplyDigest(citation.Native)
		if err != nil {
			return nil, err
		}
		result = append(result, digest)
	}
	return result, nil
}
