package domain

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
)

type ClaudeCitationKind string
type ClaudeCitationCompletion string

const (
	ClaudeCharacterCitation    ClaudeCitationKind       = "char_location"
	ClaudePageCitation         ClaudeCitationKind       = "page_location"
	ClaudeContentBlockCitation ClaudeCitationKind       = "content_block_location"
	ClaudeSearchCitation       ClaudeCitationKind       = "search_result_location"
	ClaudeWebCitation          ClaudeCitationKind       = "web_search_result_location"
	ClaudeCitationsMatched     ClaudeCitationCompletion = "matched"
	ClaudeCitationsOmitted     ClaudeCitationCompletion = "omitted-by-native"
)

// Native locations are not local files or transcript offsets. Opaque encrypted
// web indices stay in the native adapter and confer no retrieval authority.
type ClaudeCitation struct {
	Kind     ClaudeCitationKind      `json:"kind"`
	Text     string                  `json:"text"`
	Title    *string                 `json:"title"`
	Document *ClaudeDocumentCitation `json:"document,omitempty"`
	Search   *ClaudeSearchLocation   `json:"search,omitempty"`
	Web      *ClaudeWebLocation      `json:"web,omitempty"`
}
type ClaudeCitationFile struct {
	Value *string `json:"value"`
}
type ClaudeDocumentCitation struct {
	Index ClaudeProgressCount `json:"index"`
	Start ClaudeProgressCount `json:"start"`
	End   ClaudeProgressCount `json:"end"`
	File  *ClaudeCitationFile `json:"file,omitempty"`
}
type ClaudeSearchLocation struct {
	Index  ClaudeProgressCount `json:"index"`
	Start  ClaudeProgressCount `json:"start"`
	End    ClaudeProgressCount `json:"end"`
	Source string              `json:"source"`
}
type ClaudeWebLocation struct {
	URL string `json:"url"`
}
type ClaudeCitationCollection struct {
	Null    bool             `json:"null"`
	Entries []ClaudeCitation `json:"entries"`
}
type ClaudeCitationHistory struct {
	Initial    *ClaudeCitationCollection `json:"initial"`
	Deltas     []ClaudeCitation          `json:"deltas"`
	Completed  *ClaudeCitationCollection `json:"completed"`
	Completion ClaudeCitationCompletion  `json:"completion,omitempty"`
}

func claudeCitationRange(index, start, end ClaudeProgressCount, page bool) bool {
	values := [3]uint64{}
	for i, value := range []ClaudeProgressCount{index, start, end} {
		n, err := strconv.ParseUint(string(value), 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != string(value) {
			return false
		}
		values[i] = n
	}
	return values[2] > values[1] && (!page || values[1] > 0)
}
func (v ClaudeCitation) Validate() error {
	if Text(v.Text, "native cited text", MaxMessageText, false) != nil || v.Title != nil && Text(*v.Title, "native citation title", 16<<10, false) != nil {
		return invalidClaudeContent()
	}
	switch v.Kind {
	case ClaudeCharacterCitation, ClaudePageCitation, ClaudeContentBlockCitation:
		d := v.Document
		if d == nil || v.Search != nil || v.Web != nil || !claudeCitationRange(d.Index, d.Start, d.End, v.Kind == ClaudePageCitation) || d.File != nil && d.File.Value != nil && Text(*d.File.Value, "native citation file reference", 1024, true) != nil {
			return invalidClaudeContent()
		}
	case ClaudeSearchCitation:
		s := v.Search
		if s == nil || v.Document != nil || v.Web != nil || !claudeCitationRange(s.Index, s.Start, s.End, false) || Text(s.Source, "native citation source", 16<<10, true) != nil {
			return invalidClaudeContent()
		}
	case ClaudeWebCitation:
		if v.Web == nil || v.Document != nil || v.Search != nil || Text(v.Web.URL, "native citation URL", 16<<10, true) != nil {
			return invalidClaudeContent()
		}
	default:
		return invalidClaudeContent()
	}
	return nil
}
func (v *ClaudeCitationCollection) Validate() error {
	if v == nil {
		return nil
	}
	if v.Null && v.Entries != nil || !v.Null && v.Entries == nil || len(v.Entries) > 1024 {
		return invalidClaudeContent()
	}
	for _, entry := range v.Entries {
		if entry.Validate() != nil {
			return invalidClaudeContent()
		}
	}
	return nil
}
func claudeCitationFields(raw []byte, required []string, optional ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	for _, key := range required {
		if len(fields[key]) == 0 {
			return false
		}
	}
	for _, key := range optional {
		if bytes.Equal(bytes.TrimSpace(fields[key]), []byte("null")) {
			return false
		}
	}
	return true
}
func (v *ClaudeCitation) UnmarshalJSON(raw []byte) error {
	type plain ClaudeCitation
	var value plain
	if Decode(raw, &value) != nil || !claudeCitationFields(raw, []string{"kind", "text", "title"}, "kind", "text", "document", "search", "web") {
		return invalidClaudeContent()
	}
	*v = ClaudeCitation(value)
	return v.Validate()
}
func (v *ClaudeDocumentCitation) UnmarshalJSON(raw []byte) error {
	type plain ClaudeDocumentCitation
	var value plain
	if Decode(raw, &value) != nil || !claudeCitationFields(raw, []string{"index", "start", "end"}, "file") {
		return invalidClaudeContent()
	}
	*v = ClaudeDocumentCitation(value)
	return nil
}
func (v *ClaudeCitationFile) UnmarshalJSON(raw []byte) error {
	type plain ClaudeCitationFile
	var value plain
	if Decode(raw, &value) != nil || !claudeCitationFields(raw, []string{"value"}) {
		return invalidClaudeContent()
	}
	*v = ClaudeCitationFile(value)
	return nil
}
func (v *ClaudeCitationCollection) UnmarshalJSON(raw []byte) error {
	type plain ClaudeCitationCollection
	var value plain
	if Decode(raw, &value) != nil || !claudeCitationFields(raw, []string{"null", "entries"}, "null") {
		return invalidClaudeContent()
	}
	*v = ClaudeCitationCollection(value)
	return v.Validate()
}
func (v *ClaudeCitationHistory) UnmarshalJSON(raw []byte) error {
	type plain ClaudeCitationHistory
	var value plain
	if Decode(raw, &value) != nil || !claudeCitationFields(raw, []string{"initial", "deltas", "completed"}, "completion", "deltas") {
		return invalidClaudeContent()
	}
	*v = ClaudeCitationHistory(value)
	return nil
}

func claudeCitationEntries(v *ClaudeCitationCollection) []ClaudeCitation {
	if v == nil {
		return nil
	}
	return v.Entries
}
func (h *ClaudeCitationHistory) Validate(state ClaudeBlockState) error {
	if h == nil {
		return nil
	}
	if h.Initial.Validate() != nil || h.Completed.Validate() != nil || h.Deltas == nil || len(claudeCitationEntries(h.Initial))+len(h.Deltas) > 1024 || h.Initial == nil && len(h.Deltas) == 0 && h.Completed == nil {
		return invalidClaudeContent()
	}
	for _, v := range h.Deltas {
		if v.Validate() != nil {
			return invalidClaudeContent()
		}
	}
	if state == ClaudeBlockStreaming {
		if h.Completed != nil || h.Completion != "" {
			return invalidClaudeContent()
		}
		return nil
	}
	if state != ClaudeBlockCompleted && state != ClaudeBlockStopped {
		return invalidClaudeContent()
	}
	want := append(append([]ClaudeCitation{}, claudeCitationEntries(h.Initial)...), h.Deltas...)
	got := claudeCitationEntries(h.Completed)
	switch h.Completion {
	case ClaudeCitationsMatched:
		if len(want) != len(got) {
			return invalidClaudeContent()
		}
		for i := range want {
			if !reflect.DeepEqual(want[i], got[i]) {
				return invalidClaudeContent()
			}
		}
	case ClaudeCitationsOmitted:
		// Pinned Claude 2.1.236 emits empty completed arrays after citation
		// deltas. Keep both observations. Remove when native completion is fixed.
		if len(claudeCitationEntries(h.Initial)) != 0 || len(h.Deltas) == 0 || h.Completed == nil || h.Completed.Null || len(got) != 0 {
			return invalidClaudeContent()
		}
	default:
		return invalidClaudeContent()
	}
	return nil
}

// The pinned native web-citation profile preserves an empty completed array
// through its own persisted history and subsequent provider requests. Product
// deltas remain separate original evidence, never reconstructed native context.
// This candidate still requires independently verified native files/checkpoint.
func (h *ClaudeCitationHistory) ContinuationCandidate() bool {
	if h == nil {
		return true
	}
	if h.Validate(ClaudeBlockStopped) != nil || h.Completion != ClaudeCitationsOmitted || h.Initial == nil || h.Initial.Null || len(h.Initial.Entries) != 0 {
		return false
	}
	for _, citation := range h.Deltas {
		if citation.Kind != ClaudeWebCitation {
			return false
		}
	}
	return true
}
func cloneClaudeCitation(v ClaudeCitation) ClaudeCitation {
	v.Title = copyClaudeText(v.Title)
	if v.Document != nil {
		d := *v.Document
		if d.File != nil {
			d.File = &ClaudeCitationFile{Value: copyClaudeText(d.File.Value)}
		}
		v.Document = &d
	}
	if v.Search != nil {
		s := *v.Search
		v.Search = &s
	}
	if v.Web != nil {
		w := *v.Web
		v.Web = &w
	}
	return v
}
func cloneClaudeCitations(v *ClaudeCitationCollection) *ClaudeCitationCollection {
	if v == nil {
		return nil
	}
	next := &ClaudeCitationCollection{Null: v.Null}
	if v.Entries != nil {
		next.Entries = make([]ClaudeCitation, len(v.Entries))
		for i, c := range v.Entries {
			next.Entries[i] = cloneClaudeCitation(c)
		}
	}
	return next
}
func cloneClaudeCitationHistory(h *ClaudeCitationHistory) *ClaudeCitationHistory {
	if h == nil {
		return nil
	}
	next := &ClaudeCitationHistory{Initial: cloneClaudeCitations(h.Initial), Completed: cloneClaudeCitations(h.Completed), Completion: h.Completion, Deltas: make([]ClaudeCitation, len(h.Deltas))}
	for i, c := range h.Deltas {
		next.Deltas[i] = cloneClaudeCitation(c)
	}
	return next
}
