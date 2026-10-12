// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestNativeWebSearchActionsKeepOriginalModeAndContent(t *testing.T) {
	for _, mode := range []domain.WebSearchMode{domain.WebSearchCached, domain.WebSearchLive} {
		for _, action := range []any{nil, map[string]any{"type": "search", "query": "Original", "queries": []string{"First", "Second"}}, map[string]any{"type": "openPage", "url": "https://fixture.invalid"}, map[string]any{"type": "findInPage", "url": "https://fixture.invalid", "pattern": "Original"}, map[string]any{"type": "other"}} {
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

func TestNativeWebSearchAcceptsOmittedResults(t *testing.T) {
	c, turn := observationClient()
	c.execution.settings.WebSearch = domain.WebSearchLive
	c.execution.settings.Provider = "openai"
	params := map[string]any{"threadId": c.thread, "turnId": turn, "startedAtMs": 0, "item": map[string]any{"type": "webSearch", "id": "original-search", "query": "Original", "action": nil}}
	event, err := observeFixture(c, "item/started", params)
	if err != nil || event.Tool == nil || event.Tool.WebSearch == nil || event.Tool.WebSearch.Results != nil {
		t.Fatal("omitted native results were not retained as unavailable", event, err)
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
	for _, policy := range []string{`{"requirements":null}`, `{"requirements":{"allowedWebSearchModes":null}}`, `{"requirements":{"allowedWebSearchModes":["live","cached"]}}`} {
		if err := validateWebSearchProfile(domain.WebSearchLive, config, []byte(policy)); err != nil {
			t.Fatal("proven native mode rejected", err)
		}
	}
	for _, policy := range []string{`{"requirements":{"allowedWebSearchModes":["cached"]}}`, `{"requirements":{"allowedWebSearchModes":[]}}`, `{"requirements":{"allowedWebSearchModes":["live","live"]}}`, `{"requirements":{"allowedWebSearchModes":["unknown"]}}`, `{"requirements":{"allowedWebSearchModes":"live"}}`, `{"requirements":{"allowed_web_search_modes":["cached"]}}`, `{}`} {
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

func TestWebSearchSettledHistoryRemainsExactAcrossContinuationContextAndFork(t *testing.T) {
	c, capture, checkpoint, page := continuationFixture(t, "web-search")
	turn := page["data"].([]any)[0].(map[string]any)
	item := map[string]any{"type": "webSearch", "id": "original-search", "query": "Original", "action": map[string]any{"type": "openPage", "url": "https://fixture.invalid"}, "results": json.RawMessage(`[{"integer":9007199254740993}]`)}
	turn["items"] = append(turn["items"].([]any), item)
	page["nextCursor"], page["backwardsCursor"] = nil, nil
	fixtureSignal(t, c, "history", map[string]any{"page": page})
	if _, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ContinueAfterSuccess); err != nil {
		t.Fatal("search blocked continuation", err)
	}
	contextHistory, err := c.compactionTurnsLocked(context.Background())
	if err != nil {
		t.Fatal("search blocked complete context", err)
	}
	c.managedForkHistory = true
	forkHistory, err := c.forkTurnsLocked(context.Background(), c.thread)
	if err != nil || len(forkHistory) != 1 || !bytes.Equal(forkHistory[0], contextHistory[0]) || !bytes.Contains(forkHistory[0], []byte(`"integer":9007199254740993`)) {
		t.Fatal("settled search blocked Fork or changed original evidence", err)
	}
	for _, method := range []string{"turn/start", "turn/steer", "thread/fork", "thread/revert"} {
		if len(requestsOf(t, capture, method)) != 0 {
			t.Fatal("passive history scheduled native work", method)
		}
	}
}

// The synthetic native process proves original CLI/config/policy translation;
// it has no account credential and does not establish real account acceptance.
func (f *threadFixture) handleWebSearchProfile(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if os.Getenv("DELIDEV_CODEX_WEB_SEARCH_FIXTURE") != "1" {
		return false
	}
	var selected string
	for _, arg := range os.Args {
		if v, ok := strings.CutPrefix(arg, "web_search="); ok {
			selected, _ = strconv.Unquote(v)
		}
	}
	switch method {
	case "config/read":
		write(id, map[string]any{"config": map[string]any{"web_search": selected, "cli_auth_credentials_store": "file", "model_provider": "openai", "forced_login_method": "chatgpt", "model_providers": map[string]any{}}, "origins": map[string]any{}, "layers": nil})
		return true
	case "configRequirements/read":
		write(id, map[string]any{"requirements": map[string]any{"allowedWebSearchModes": []string{selected}}})
		return true
	}
	return false
}
func TestWebSearchRevertLostResponsePreservesFrozenModes(t *testing.T) {
	for _, mode := range []domain.WebSearchMode{domain.WebSearchDisabled, domain.WebSearchCached, domain.WebSearchLive} {
		t.Run(string(mode), func(t *testing.T) {
			c, capture, source, ids, inputs := revertFixtureConfigured(t, "lost", func(config *Config) {
				config.WebSearch = mode
				config.ManagedAuthentication = true
				config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_WEB_SEARCH_FIXTURE=1")
			})
			var intent RevertIntent
			if _, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[1], ids[1], func(v RevertIntent) error { intent = v; return nil }); err == nil {
				t.Fatal("lost response released input")
			}
			// As in the existing Revert fixture, clear only per-process failure to
			// exercise observation of retained native state without another Revert.
			c.problem = nil
			c.execution.paused = false
			proof, err := c.ReconcileRevert(context.Background(), intent)
			if err != nil || proof.Source.Effective.WebSearch != mode || len(requestsOf(t, capture, "thread/revert")) != 1 {
				t.Fatal("recovery changed the frozen mode or repeated Revert", err)
			}
			if len(requestsOf(t, capture, "turn/start")) != 0 || len(requestsOf(t, capture, "turn/steer")) != 0 {
				t.Fatal("read-only reconciliation sent native input")
			}
			if _, err := os.Stat(filepath.Join(c.home, "auth.json")); !os.IsNotExist(err) {
				t.Fatal("fixture unexpectedly acquired an account credential", err)
			}
		})
	}
}
