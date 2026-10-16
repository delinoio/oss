// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// WebSearch is one original native web search, open-page or find-in-page call.
// There is no DeliDev search implementation: only items the native process
// itself reports are published, and results are never synthesized.
type WebSearch struct {
	ID          string
	Observation domain.WebSearchObservation
}

// configureWebSearch freezes the native web search mode before launch. The
// empty mode inherits native configuration and sends nothing. A custom API
// binding keeps standalone search disabled because provider support is not
// established, so only Disabled is accepted there. Restricted Sidechat and the
// private title profile pin their own disabled mode.
func configureWebSearch(config *Config) error {
	if config.WebSearchMode == "" {
		return nil
	}
	if config.WebSearchMode.Validate() != nil || config.Mode != ThreadProtocol || config.Sidechat != "" || config.ManagedTitle || config.ModelObservation {
		return incompatible()
	}
	if config.API != nil && config.WebSearchMode != domain.WebSearchDisabled {
		return domain.Fail(domain.Unsupported, "The selected provider cannot run native web search.", "Use Disabled, or an original supported managed OpenAI account; no search fallback is available.")
	}
	config.Process.Args = append(config.Process.Args, "-c", `web_search="`+string(config.WebSearchMode)+`"`)
	return nil
}

// decodeWebSearch strictly decodes the native `webSearch` thread item. Unknown
// fields or action types are incompatible rather than coerced.
func decodeWebSearch(raw json.RawMessage, mode domain.WebSearchMode) (*WebSearch, error) {
	var item struct {
		Type   string  `json:"type"`
		ID     string  `json:"id"`
		Query  *string `json:"query"`
		Action *struct {
			Type    domain.WebSearchActionKind `json:"type"`
			Query   *string                    `json:"query"`
			Queries []string                   `json:"queries"`
			URL     *string                    `json:"url"`
			Pattern *string                    `json:"pattern"`
		} `json:"action"`
	}
	if domain.DecodeBounded(raw, &item, 1<<20) != nil || item.Type != "webSearch" || domain.Text(item.ID, "native web search identity", 1024, true) != nil || item.Query == nil {
		return nil, incompatible()
	}
	observation := domain.WebSearchObservation{Mode: mode, Query: *item.Query, Kind: domain.WebSearchActionOther}
	if a := item.Action; a != nil {
		observation.Kind, observation.Search, observation.Queries, observation.URL, observation.Pattern = a.Type, a.Query, a.Queries, a.URL, a.Pattern
	}
	if observation.Validate() != nil {
		return nil, incompatible()
	}
	return &WebSearch{ID: item.ID, Observation: observation}, nil
}

func (c *Client) observeWebSearch(native nativewire.Event, turnID domain.ID, raw json.RawMessage) (Event, error) {
	// A disabled or unknown mode must send no search; an item that arrives
	// anyway contradicts the frozen profile and is not published.
	if c.webSearchMode == "" || c.webSearchMode == domain.WebSearchDisabled || c.sidechat != "" || c.api != nil {
		return Event{}, domain.Fail(domain.Unsupported, "Native web search is not enabled for this execution.", "Select Cached or Live on a supported account; no search fallback is available.")
	}
	turn, known := c.execution.turns[turnID]
	if !known || turn.Turn.Status.terminal() || c.execution.active != turnID {
		return Event{}, incompatible()
	}
	search, err := decodeWebSearch(raw, c.webSearchMode)
	if err != nil {
		return Event{}, err
	}
	kind := WebSearchStartedEvent
	if native.Method == "item/completed" {
		kind = WebSearchCompletedEvent
	}
	return Event{Kind: kind, ThreadID: c.thread, TurnID: turnID, ItemID: search.ID, WebSearch: search, Correlated: true}, nil
}
