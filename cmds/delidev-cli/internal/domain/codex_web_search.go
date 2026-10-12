// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
	"slices"
)

const CodexWebSearchV1 WorkerCapability = "codex-web-search-v1"

type WebSearchMode string

const (
	WebSearchDisabled WebSearchMode = "disabled"
	WebSearchCached   WebSearchMode = "cached"
	WebSearchLive     WebSearchMode = "live"
)

func (m WebSearchMode) Valid() bool {
	return slices.Contains([]WebSearchMode{WebSearchDisabled, WebSearchCached, WebSearchLive}, m)
}

type CodexWebSearchActionKind string

const (
	CodexWebSearchActionSearch CodexWebSearchActionKind = "search"
	CodexWebSearchActionOpen   CodexWebSearchActionKind = "openPage"
	CodexWebSearchActionFind   CodexWebSearchActionKind = "findInPage"
	CodexWebSearchActionOther  CodexWebSearchActionKind = "other"
)

// Optional native fields remain optional; the query on the item is independent
// of the action's query/queries. None of these values authorize another search.
type CodexWebSearchAction struct {
	Type    CodexWebSearchActionKind `json:"type"`
	Query   *string                  `json:"query,omitempty"`
	Queries *[]string                `json:"queries,omitempty"`
	URL     *string                  `json:"url,omitempty"`
	Pattern *string                  `json:"pattern,omitempty"`
}

func (a CodexWebSearchAction) Validate() error {
	for _, p := range []*string{a.Query, a.URL, a.Pattern} {
		if p != nil && Text(*p, "native web action", 16<<10, false) != nil {
			return invalidTool()
		}
	}
	if a.Queries != nil {
		if *a.Queries == nil || len(*a.Queries) > 128 {
			return invalidTool()
		}
		for _, q := range *a.Queries {
			if Text(q, "native web query", 16<<10, false) != nil {
				return invalidTool()
			}
		}
	}
	switch a.Type {
	case CodexWebSearchActionSearch:
		if a.URL != nil || a.Pattern != nil {
			return invalidTool()
		}
	case CodexWebSearchActionOpen:
		if a.Query != nil || a.Queries != nil || a.Pattern != nil {
			return invalidTool()
		}
	case CodexWebSearchActionFind:
		if a.Query != nil || a.Queries != nil {
			return invalidTool()
		}
	case CodexWebSearchActionOther:
		if a.Query != nil || a.Queries != nil || a.URL != nil || a.Pattern != nil {
			return invalidTool()
		}
	default:
		return invalidTool()
	}
	return nil
}

// Native results stay exact JSON text. Parsing into renderer numbers would lose
// original precision; no unknown result field is interpreted as a citation.
type CodexWebSearchObservation struct {
	Query   string                `json:"query"`
	Action  *CodexWebSearchAction `json:"action"`
	Results *string               `json:"results"`
	Mode    WebSearchMode         `json:"mode"`
	Source  string                `json:"source"`
}

func (o CodexWebSearchObservation) Validate() error {
	if Text(o.Query, "native web query", 16<<10, false) != nil || !slices.Contains([]WebSearchMode{WebSearchCached, WebSearchLive}, o.Mode) || Text(o.Source, "native web provider", 256, true) != nil {
		return invalidTool()
	}
	if o.Action != nil && o.Action.Validate() != nil {
		return invalidTool()
	}
	if o.Results != nil {
		var results []json.RawMessage
		if Text(*o.Results, "native web results", 384<<10, false) != nil || DecodeBounded([]byte(*o.Results), &results, 384<<10) != nil || results == nil || len(results) > 1024 {
			return invalidTool()
		}
	}
	return nil
}

func (a *CodexWebSearchAction) UnmarshalJSON(raw []byte) error {
	type plain CodexWebSearchAction
	var value plain
	if Decode(raw, &value) != nil {
		return invalidTool()
	}
	var fields map[string]json.RawMessage
	if Decode(raw, &fields) != nil {
		return invalidTool()
	}
	allowed := []string{"type"}
	switch value.Type {
	case CodexWebSearchActionSearch:
		allowed = append(allowed, "query", "queries")
	case CodexWebSearchActionOpen:
		allowed = append(allowed, "url")
	case CodexWebSearchActionFind:
		allowed = append(allowed, "url", "pattern")
	case CodexWebSearchActionOther:
	default:
		return invalidTool()
	}
	for key, v := range fields {
		if !slices.Contains(allowed, key) || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return invalidTool()
		}
	}
	*a = CodexWebSearchAction(value)
	return a.Validate()
}

// Durable snapshots use the same required/nullable native evidence envelope.
func (o *CodexWebSearchObservation) UnmarshalJSON(raw []byte) error {
	type plain CodexWebSearchObservation
	var value plain
	var fields map[string]json.RawMessage
	if Decode(raw, &value) != nil || Decode(raw, &fields) != nil {
		return invalidTool()
	}
	for _, key := range []string{"query", "action", "results", "mode", "source"} {
		v, ok := fields[key]
		if !ok || key != "action" && key != "results" && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return invalidTool()
		}
	}
	*o = CodexWebSearchObservation(value)
	return o.Validate()
}
