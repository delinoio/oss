// SPDX-License-Identifier: Apache-2.0
package domain

import "slices"

const NativeWebSearchV1 WorkerCapability = "native-web-search-v1"

// WebSearchMode is the closed native Codex `web_search` configuration value.
// The empty value means "inherit"; it is never sent to the native process.
type WebSearchMode string

const (
	WebSearchDisabled WebSearchMode = "disabled"
	WebSearchCached   WebSearchMode = "cached"
	WebSearchLive     WebSearchMode = "live"
)

// Validate accepts the three native modes only.
func (m WebSearchMode) Validate() error {
	if !slices.Contains([]WebSearchMode{WebSearchDisabled, WebSearchCached, WebSearchLive}, m) {
		return Fail(InvalidArgument, "Unsupported native web search mode.", "Use disabled, cached or live.")
	}
	return nil
}

// WebSearchActionKind is the closed native web search action union.
type WebSearchActionKind string

const (
	WebSearchActionSearch     WebSearchActionKind = "search"
	WebSearchActionOpenPage   WebSearchActionKind = "openPage"
	WebSearchActionFindInPage WebSearchActionKind = "findInPage"
	WebSearchActionOther      WebSearchActionKind = "other"
)

// WebSearchObservation retains one original native search call. Fields absent
// on the native wire stay nil; no query, URL or result is synthesized. Mode is
// the frozen execution mode that was actually in force for the call.
type WebSearchObservation struct {
	Mode    WebSearchMode       `json:"mode"`
	Query   string              `json:"query"`
	Kind    WebSearchActionKind `json:"kind"`
	Search  *string             `json:"search,omitempty"`
	Queries []string            `json:"queries,omitempty"`
	URL     *string             `json:"url,omitempty"`
	Pattern *string             `json:"pattern,omitempty"`
}

const (
	maxWebSearchText    = 64 << 10
	maxWebSearchQueries = 64
)

// Validate bounds every retained native field and checks that fields belong to
// the declared action kind. A disabled mode never carries an observation.
func (o WebSearchObservation) Validate() error {
	if o.Mode.Validate() != nil || o.Mode == WebSearchDisabled || !slices.Contains([]WebSearchActionKind{WebSearchActionSearch, WebSearchActionOpenPage, WebSearchActionFindInPage, WebSearchActionOther}, o.Kind) || len(o.Queries) > maxWebSearchQueries {
		return invalidArtifact()
	}
	if Text(o.Query, "native web search query", maxWebSearchText, false) != nil {
		return invalidArtifact()
	}
	for _, value := range append([]string{}, o.Queries...) {
		if Text(value, "native web search query", maxWebSearchText, false) != nil {
			return invalidArtifact()
		}
	}
	for _, value := range []*string{o.Search, o.URL, o.Pattern} {
		if value != nil && Text(*value, "native web search field", maxWebSearchText, false) != nil {
			return invalidArtifact()
		}
	}
	switch o.Kind {
	case WebSearchActionSearch:
		if o.URL != nil || o.Pattern != nil {
			return invalidArtifact()
		}
	case WebSearchActionOpenPage:
		if o.Search != nil || len(o.Queries) != 0 || o.Pattern != nil {
			return invalidArtifact()
		}
	case WebSearchActionFindInPage:
		if o.Search != nil || len(o.Queries) != 0 {
			return invalidArtifact()
		}
	case WebSearchActionOther:
		if o.Search != nil || len(o.Queries) != 0 || o.URL != nil || o.Pattern != nil {
			return invalidArtifact()
		}
	}
	return nil
}
