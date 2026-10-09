package domain

import (
	"encoding/json"
	"slices"
	"time"
)

type ClaudeWebName string
type ClaudeWebDocumentSource string
type ClaudeWebMediaType string
type ClaudeWebProblem string

const (
	ClaudeWebSearch ClaudeWebName = "web_search"
	ClaudeWebFetch  ClaudeWebName = "web_fetch"
)

// These are inert original provider observations, never local tool authority.
// Encrypted search content and binary document bodies remain native-private.
type ClaudeWebBlock struct {
	NativeID string                `json:"native_id"`
	Caller   *ClaudeToolCallerKind `json:"caller"`
	Name     ClaudeWebName         `json:"name"`
	Call     *ClaudeWebCallContent `json:"call,omitempty"`
	Result   *ClaudeWebResult      `json:"result,omitempty"`
}
type ClaudeWebCallContent struct {
	InitialInput string  `json:"initial_input"`
	InputDelta   *string `json:"input_delta"`
}
type ClaudeWebSearchSource struct {
	URL     string  `json:"url"`
	Title   string  `json:"title"`
	PageAge *string `json:"page_age"`
}
type ClaudeWebDocument struct {
	Source    ClaudeWebDocumentSource `json:"source"`
	Media     ClaudeWebMediaType      `json:"media"`
	Title     *string                 `json:"title"`
	Context   *string                 `json:"context"`
	Citations *bool                   `json:"citations"`
	Text      *string                 `json:"text"`
}
type ClaudeWebFetchContent struct {
	URL         string            `json:"url"`
	RetrievedAt *string           `json:"retrieved_at"`
	Document    ClaudeWebDocument `json:"document"`
}
type ClaudeWebResult struct {
	Problem ClaudeWebProblem        `json:"problem"`
	Search  []ClaudeWebSearchSource `json:"search"`
	Fetch   *ClaudeWebFetchContent  `json:"fetch"`
}

func (w ClaudeWebBlock) Validate(kind ClaudeTextKind) error {
	if w.Caller != nil && *w.Caller != ClaudeDirectToolCaller {
		return invalidClaudeContent()
	}
	if Text(w.NativeID, "native server tool identity", 1024, true) != nil || w.Name != ClaudeWebSearch && w.Name != ClaudeWebFetch {
		return invalidClaudeContent()
	}
	if kind == ClaudeWebCall {
		if w.Call == nil || w.Result != nil || !validClaudeToolJSON(w.Call.InitialInput) || w.Call.InputDelta != nil && Text(*w.Call.InputDelta, "native input fragments", MaxMessageText, false) != nil {
			return invalidClaudeContent()
		}
		return nil
	}
	if w.Call != nil || w.Result == nil || kind != ClaudeWebSearchResult && kind != ClaudeWebFetchResult || (kind == ClaudeWebSearchResult) != (w.Name == ClaudeWebSearch) {
		return invalidClaudeContent()
	}
	r := w.Result
	problems := []ClaudeWebProblem{"invalid_tool_input", "unavailable", "max_uses_exceeded", "too_many_requests", "query_too_long", "request_too_large"}
	if w.Name == ClaudeWebFetch {
		problems = []ClaudeWebProblem{"invalid_tool_input", "url_too_long", "url_not_allowed", "url_not_in_prior_context", "url_not_accessible", "unsupported_content_type", "too_many_requests", "max_uses_exceeded", "unavailable", "content_too_large"}
	}
	if r.Problem != "" {
		if !slices.Contains(problems, r.Problem) || r.Search != nil || r.Fetch != nil {
			return invalidClaudeContent()
		}
		return nil
	}
	if w.Name == ClaudeWebSearch {
		if r.Search == nil || len(r.Search) > 1024 || r.Fetch != nil {
			return invalidClaudeContent()
		}
		for _, s := range r.Search {
			if Text(s.URL, "native source URL", 16<<10, true) != nil || Text(s.Title, "native source title", 16<<10, false) != nil || s.PageAge != nil && Text(*s.PageAge, "native page age", 1024, false) != nil {
				return invalidClaudeContent()
			}
		}
		return nil
	}
	if r.Search != nil || r.Fetch == nil {
		return invalidClaudeContent()
	}
	f := r.Fetch
	if Text(f.URL, "native fetched URL", 16<<10, true) != nil {
		return invalidClaudeContent()
	}
	if f.RetrievedAt != nil {
		if _, err := time.Parse(time.RFC3339Nano, *f.RetrievedAt); err != nil {
			return invalidClaudeContent()
		}
	}
	d := f.Document
	if !slices.Contains([]ClaudeWebDocumentSource{"text", "base64", "url", "file", "content"}, d.Source) || (d.Source == "text" && d.Media != "text/plain") || (d.Source == "base64" && d.Media != "application/pdf") || (d.Source != "text" && d.Source != "base64" && d.Media != "") || d.Text != nil && (d.Source != "text" && d.Source != "content" || Text(*d.Text, "native fetched text", MaxMessageText, false) != nil) || d.Title != nil && Text(*d.Title, "native document title", 16<<10, false) != nil || d.Context != nil && Text(*d.Context, "native document context", MaxMessageText, false) != nil {
		return invalidClaudeContent()
	}
	return nil
}
func cloneClaudeWeb(w *ClaudeWebBlock) *ClaudeWebBlock {
	if w == nil {
		return nil
	}
	raw, _ := json.Marshal(w)
	var v ClaudeWebBlock
	_ = json.Unmarshal(raw, &v)
	return &v
}
func validateClaudeWebHistory(blocks []ClaudeRetainedBlock, closed bool) error {
	calls := map[string]ClaudeWebBlock{}
	callStates := map[string]ClaudeBlockState{}
	local := map[string]bool{}
	for _, b := range blocks {
		if b.Block.Tool != nil {
			local[b.Block.Tool.NativeID] = true
		}
	}
	finished := map[string]bool{}
	open := 0
	for _, b := range blocks {
		w := b.Block.Web
		if w == nil {
			continue
		}
		if local[w.NativeID] {
			return invalidClaudeContent()
		}
		if b.Block.Kind == ClaudeWebCall {
			if _, exists := calls[w.NativeID]; exists {
				return invalidClaudeContent()
			}
			if b.State != ClaudeBlockStreaming {
				raw := w.Call.InitialInput
				if w.Call.InputDelta != nil && *w.Call.InputDelta != "" {
					raw = *w.Call.InputDelta
				}
				var input map[string]json.RawMessage
				var value string
				key := "query"
				if w.Name == ClaudeWebFetch {
					key = "url"
				}
				if Decode([]byte(raw), &input) != nil || len(input) != 1 || json.Unmarshal(input[key], &value) != nil || Text(value, "native server input", MaxMessageText, true) != nil {
					return invalidClaudeContent()
				}
			}
			open++
			if open > 128 {
				return invalidClaudeContent()
			}
			calls[w.NativeID] = *w
			callStates[w.NativeID] = b.State
		} else {
			call, exists := calls[w.NativeID]
			if !exists || call.Name != w.Name || finished[w.NativeID] || callStates[w.NativeID] != ClaudeBlockStopped {
				return invalidClaudeContent()
			}
			finished[w.NativeID] = true
			open--
		}
	}
	if closed {
		for id := range calls {
			if !finished[id] {
				return invalidClaudeContent()
			}
		}
	}
	return nil
}
