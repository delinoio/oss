// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestNativeWebSearchActionsKeepOriginalModeAndContent(t *testing.T) {
	for _, mode := range []domain.WebSearchMode{domain.WebSearchCached, domain.WebSearchLive} {
		for _, action := range []any{nil, map[string]any{"type": "search", "query": "Original", "queries": []string{"First", "Second"}}, map[string]any{"type": "open_page", "url": "https://fixture.invalid"}, map[string]any{"type": "find_in_page", "url": "https://fixture.invalid", "pattern": "Original"}, map[string]any{"type": "other"}} {
			c, turn := observationClient()
			c.execution.settings.WebSearch = mode
			c.execution.settings.Provider = "openai"
			params := map[string]any{"threadId": c.thread, "turnId": turn, "item": map[string]any{"type": "webSearch", "id": "original-search", "query": "Original", "action": action, "results": []any{map[string]any{"unknown": "original"}}}}
			for _, method := range []string{"item/started", "item/completed"} {
				delete(params, "startedAtMs")
				delete(params, "completedAtMs")
				if method == "item/started" {
					params["startedAtMs"] = 0
				} else {
					params["completedAtMs"] = 0
				}
				event, err := observeFixture(c, method, params)
				if err != nil || event.Tool == nil || event.Tool.WebSearch == nil || event.Tool.WebSearch.Mode != mode || event.Tool.WebSearch.Source != "openai" || event.Tool.WebSearch.Query != "Original" || event.Tool.WebSearch.Results == nil || c.execution.turns[turn].Turn.Status != TurnRunning {
					t.Fatal("native evidence lost or tool completed the turn", err)
				}
			}
			params["turnId"] = domain.NewID()
			delete(params, "completedAtMs")
			params["startedAtMs"] = 0
			if _, err := observeFixture(c, "item/started", params); err == nil {
				t.Fatal("foreign turn accepted")
			}
		}
	}
	c, turn := observationClient()
	c.execution.settings.WebSearch = domain.WebSearchDisabled
	params := map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 0, "item": map[string]any{"type": "webSearch", "id": "original-search", "query": "Original", "action": nil, "results": nil}}
	if _, err := observeFixture(c, "item/started", params); err == nil {
		t.Fatal("disabled search gained authority")
	}
}
func TestWebSearchRejectsUnprovedProviderBeforeLaunch(t *testing.T) {
	for _, mode := range []domain.WebSearchMode{domain.WebSearchCached, domain.WebSearchLive} {
		config := Config{Mode: ThreadProtocol, WebSearch: mode, API: &APIConfig{}}
		if configureWebSearch(&config) == nil || len(config.Process.Args) != 0 {
			t.Fatal("unsupported API provider gained search support")
		}
	}
	config := Config{Mode: ThreadProtocol, WebSearch: domain.WebSearchDisabled}
	if configureWebSearch(&config) != nil || len(config.Process.Args) != 2 || config.Process.Args[1] != `web_search="disabled"` {
		t.Fatal("disabled was not explicitly pinned")
	}
	for _, raw := range []string{`{"type":"webSearch","id":"x","query":"q","action":{"type":"other","url":null},"results":null}`, `{"type":"webSearch","id":"x","query":"q","action":null}`, `{"type":"webSearch","id":"x","query":"q","action":null,"results":{}}`, `{"type":"webSearch","id":"x","query":"q","action":{"type":"replacement"},"results":null}`} {
		if _, err := decodeWebSearch([]byte(raw)); err == nil {
			t.Fatal("malformed native action accepted", raw)
		}
	}
}

func TestWebSearchProfileRequiresEffectiveModeAndAllowedPolicy(t *testing.T) {
	config := []byte(`{"config":{"web_search":"live"},"origins":{},"layers":null}`)
	for _, policy := range []string{`{"requirements":null}`, `{"requirements":{"allowed_web_search_modes":null}}`, `{"requirements":{"allowed_web_search_modes":["live","cached"]}}`} {
		if err := validateWebSearchProfile(domain.WebSearchLive, config, []byte(policy)); err != nil {
			t.Fatal("proven native mode rejected", err)
		}
	}
	for _, policy := range []string{`{"requirements":{"allowed_web_search_modes":["cached"]}}`, `{"requirements":{"allowed_web_search_modes":[]}}`, `{"requirements":{"allowed_web_search_modes":["live","live"]}}`, `{"requirements":{"allowed_web_search_modes":["unknown"]}}`, `{"requirements":{"allowed_web_search_modes":"live"}}`, `{}`} {
		if err := validateWebSearchProfile(domain.WebSearchLive, config, []byte(policy)); err == nil {
			t.Fatal("denied or malformed policy accepted", policy)
		}
	}
	for _, bad := range []string{`{"config":{"web_search":"cached"},"origins":{}}`, `{"config":{"web_search":null},"origins":{}}`, `{"config":{},"origins":{}}`} {
		if err := validateWebSearchProfile(domain.WebSearchLive, []byte(bad), []byte(`{"requirements":null}`)); err == nil {
			t.Fatal("mode mismatch accepted", bad)
		}
	}
}
