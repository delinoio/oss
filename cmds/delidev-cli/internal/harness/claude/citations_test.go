package claude

import (
	"bytes"
	"encoding/json"
	"testing"
)

func citationFixture(kind CitationKind) map[string]any {
	value := map[string]any{"type": kind, "cited_text": "Exact original cited text."}
	if kind == WebCitation {
		value["url"], value["title"], value["encrypted_index"] = "https://fixture.invalid/source", nil, "private-encrypted-index"
		return value
	}
	if kind == SearchCitation {
		value["search_result_index"], value["source"], value["title"] = 0, "native-source-reference", nil
	} else {
		value["document_index"], value["document_title"], value["file_id"] = 0, nil, nil
	}
	switch kind {
	case CharacterCitation:
		value["start_char_index"], value["end_char_index"] = 0, 27
	case PageCitation:
		value["start_page_number"], value["end_page_number"] = 1, 2
	default:
		value["start_block_index"], value["end_block_index"] = 0, 1
	}
	return value
}

func TestCitationsPreserveSourceKindsAndNativeOptionalFields(t *testing.T) {
	for _, kind := range []CitationKind{CharacterCitation, PageCitation, BlockCitation, SearchCitation, WebCitation} {
		raw, _ := json.Marshal(citationFixture(kind))
		citation, err := decodeCitation(raw)
		if err != nil || citation.Kind != kind || citation.Text != "Exact original cited text." || !bytes.Equal(citation.Native, raw) {
			t.Fatal("citation lost its original location semantics", kind, err)
		}
		published, _ := json.Marshal(citation)
		if bytes.Contains(published, []byte("private-encrypted-index")) {
			t.Fatal("opaque web index escaped")
		}
		raw[0] = '['
		if citation.Native[0] != '{' {
			t.Fatal("caller mutation changed native citation proof")
		}
	}
	for _, raw := range []json.RawMessage{nil, []byte("null"), []byte("[]")} {
		citations, err := decodeCitations(raw)
		if err != nil || (citations == nil) != (raw == nil) || (citations != nil && citations.Null != bytes.Equal(raw, []byte("null"))) || (bytes.Equal(raw, []byte("[]")) && citations.Entries == nil) {
			t.Fatal("missing/null/empty citation state was collapsed", err)
		}
	}
}

func TestCitationsRejectForeignFieldsInvalidRangesAndMissingEvidence(t *testing.T) {
	for _, change := range []string{"missing-text", "null-text", "missing-title", "null-start", "negative-start", "empty-range", "reversed-range", "fractional-index", "cross-kind", "case-alias", "unknown-kind", "zero-page", "missing-source", "null-encrypted", "duplicate-key"} {
		t.Run(change, func(t *testing.T) {
			value := citationFixture(CharacterCitation)
			switch change {
			case "missing-text":
				delete(value, "cited_text")
			case "null-text":
				value["cited_text"] = nil
			case "missing-title":
				delete(value, "document_title")
			case "null-start":
				value["start_char_index"] = nil
			case "negative-start":
				value["start_char_index"] = -1
			case "empty-range":
				value["end_char_index"] = 0
			case "reversed-range":
				value["start_char_index"] = 99
			case "fractional-index":
				value["document_index"] = 0.5
			case "cross-kind":
				value["start_page_number"] = 1
			case "case-alias":
				value["Document_index"] = 0
			case "unknown-kind":
				value["type"] = "unverified"
			case "zero-page":
				value = citationFixture(PageCitation)
				value["start_page_number"] = 0
			case "missing-source":
				value = citationFixture(SearchCitation)
				delete(value, "source")
			case "null-encrypted":
				value = citationFixture(WebCitation)
				value["encrypted_index"] = nil
			}
			raw, _ := json.Marshal(value)
			if change == "duplicate-key" {
				raw = append(raw[:len(raw)-1], []byte(`,"document_index":0}`)...)
			}
			if _, err := decodeCitation(raw); err == nil {
				t.Fatal("unverified citation was accepted")
			}
		})
	}
}

func TestCitationDeltasRequireExactOrderedCompletionAndReleaseBuffers(t *testing.T) {
	for _, change := range []string{"valid", "native-omission", "removed", "changed", "reordered", "extra", "wrong-block"} {
		t.Run(change, func(t *testing.T) {
			b := contentFixture(t)
			contentStart(t, b, "", "msg_citations")
			start := map[string]any{"type": "text", "text": "Cited answer.", "citations": []any{}}
			if change == "wrong-block" {
				start = map[string]any{"type": "thinking", "thinking": "", "signature": ""}
			}
			lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 0, "content_block": start}))
			first, second := citationFixture(CharacterCitation), citationFixture(WebCitation)
			for _, citation := range []any{first, second} {
				observation, err := b.Observe(contentPartial(t, b, "", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "citations_delta", "citation": citation}}))
				if change == "wrong-block" {
					if err == nil {
						t.Fatal("citation became a thinking delta")
					}
					return
				}
				if err != nil || observation.Content[0].DeltaKind != CitationsDelta || observation.Content[0].Citation == nil || observation.Content[0].Delta != nil {
					t.Fatal("citation delta was flattened into text", err)
				}
				observation.Content[0].Citation.Native[0] = '['
			}
			citations := []any{first, second}
			switch change {
			case "native-omission":
				citations = []any{}
			case "removed":
				citations = nil
			case "changed":
				second["encrypted_index"] = "different"
			case "reordered":
				citations = []any{second, first}
			case "extra":
				citations = append(citations, first)
			}
			observation, err := b.Observe(contentCompleted(t, b, "", "msg_citations", map[string]any{"type": "text", "text": "Cited answer.", "citations": citations}))
			if (err == nil) != (change == "valid" || change == "native-omission") {
				t.Fatal("completed citation list contradicted native deltas", err)
			}
			if err == nil {
				wantCount, wantCompletion := 2, CitationsMatched
				if change == "native-omission" {
					wantCount, wantCompletion = 0, CitationsOmittedByNative
				}
				if b.content.bufferedBytes != 0 || len(observation.Content[0].Block.Citations.Entries) != wantCount || observation.Content[0].CitationCompletion != wantCompletion {
					t.Fatal("citation completion fabricated content, lost omission evidence or retained buffers")
				}
			}
		})
	}
}

func TestCitationOmissionCannotDiscardOriginalStartedCitations(t *testing.T) {
	b := contentFixture(t)
	contentStart(t, b, "", "msg_started_citation")
	first := citationFixture(CharacterCitation)
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": "Original answer.", "citations": []any{first}}}))
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "citations_delta", "citation": citationFixture(WebCitation)}}))
	if _, err := b.Observe(contentCompleted(t, b, "", "msg_started_citation", map[string]any{"type": "text", "text": "Original answer.", "citations": []any{}})); err == nil {
		t.Fatal("native profile exception discarded a different original citation shape")
	}
}
