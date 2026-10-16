// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestConfigureWebSearchFreezesNativeMode(t *testing.T) {
	for _, mode := range []domain.WebSearchMode{domain.WebSearchDisabled, domain.WebSearchCached, domain.WebSearchLive} {
		config := Config{Mode: ThreadProtocol, WebSearchMode: mode}
		if err := configureWebSearch(&config); err != nil || len(config.Process.Args) != 2 || config.Process.Args[1] != `web_search="`+string(mode)+`"` {
			t.Fatal("mode not frozen", mode, err, config.Process.Args)
		}
	}
	inherit := Config{Mode: ThreadProtocol}
	if configureWebSearch(&inherit) != nil || len(inherit.Process.Args) != 0 {
		t.Fatal("inherited mode must send nothing")
	}
	for name, config := range map[string]Config{
		"unknown":  {Mode: ThreadProtocol, WebSearchMode: "auto"},
		"probe":    {Mode: ProbeProtocol, WebSearchMode: domain.WebSearchLive},
		"sidechat": {Mode: ThreadProtocol, Sidechat: ReadOnlySidechatV1, WebSearchMode: domain.WebSearchLive},
		"title":    {Mode: ThreadProtocol, ManagedTitle: true, WebSearchMode: domain.WebSearchLive},
	} {
		if configureWebSearch(&config) == nil || len(config.Process.Args) != 0 {
			t.Fatal("unsupported profile accepted", name)
		}
	}
	for _, mode := range []domain.WebSearchMode{domain.WebSearchCached, domain.WebSearchLive} {
		config := Config{Mode: ThreadProtocol, API: &APIConfig{}, WebSearchMode: mode}
		if err := configureWebSearch(&config); domain.SafeError(err).Code != domain.Unsupported {
			t.Fatal("unsupported provider must stay unavailable", mode)
		}
	}
	api := Config{Mode: ThreadProtocol, API: &APIConfig{}, WebSearchMode: domain.WebSearchDisabled}
	if configureWebSearch(&api) != nil {
		t.Fatal("disabled is valid on a custom provider")
	}
}

func TestDecodeWebSearchKeepsNativeActionsAndMode(t *testing.T) {
	cases := map[string]struct {
		raw  string
		kind domain.WebSearchActionKind
	}{
		"search": {`{"type":"webSearch","id":"ws1","query":"q","action":{"type":"search","query":"q","queries":["a","b"]}}`, domain.WebSearchActionSearch},
		"open":   {`{"type":"webSearch","id":"ws2","query":"","action":{"type":"openPage","url":"https://example.com"}}`, domain.WebSearchActionOpenPage},
		"find":   {`{"type":"webSearch","id":"ws3","query":"","action":{"type":"findInPage","url":"https://example.com","pattern":"x"}}`, domain.WebSearchActionFindInPage},
		"other":  {`{"type":"webSearch","id":"ws4","query":"q","action":{"type":"other"}}`, domain.WebSearchActionOther},
		"none":   {`{"type":"webSearch","id":"ws5","query":"q","action":null}`, domain.WebSearchActionOther},
	}
	for name, tc := range cases {
		got, err := decodeWebSearch(json.RawMessage(tc.raw), domain.WebSearchLive)
		if err != nil || got.Observation.Kind != tc.kind || got.Observation.Mode != domain.WebSearchLive {
			t.Fatal(name, err, got)
		}
	}
	got, _ := decodeWebSearch(json.RawMessage(cases["search"].raw), domain.WebSearchCached)
	if got.Observation.Mode != domain.WebSearchCached || len(got.Observation.Queries) != 2 || *got.Observation.Search != "q" {
		t.Fatal("search details or mode lost")
	}
}

func TestDecodeWebSearchRejectsMalformedActions(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown action":   `{"type":"webSearch","id":"a","query":"q","action":{"type":"browse"}}`,
		"unknown field":    `{"type":"webSearch","id":"a","query":"q","result":"invented"}`,
		"action field":     `{"type":"webSearch","id":"a","query":"q","action":{"type":"search","url":"https://x"}}`,
		"open with query":  `{"type":"webSearch","id":"a","query":"","action":{"type":"openPage","query":"q"}}`,
		"missing query":    `{"type":"webSearch","id":"a"}`,
		"missing identity": `{"type":"webSearch","id":"","query":"q"}`,
		"wrong type":       `{"type":"imageGeneration","id":"a","query":"q"}`,
		"duplicate key":    `{"type":"webSearch","id":"a","id":"b","query":"q"}`,
	} {
		if _, err := decodeWebSearch(json.RawMessage(raw), domain.WebSearchLive); err == nil {
			t.Fatal("accepted", name)
		}
	}
}

func TestObserveWebSearchPublishesNothingWhenDisabledOrUnsupported(t *testing.T) {
	raw := json.RawMessage(`{"type":"webSearch","id":"a","query":"q","action":null}`)
	for name, c := range map[string]*Client{
		"inherited": {},
		"disabled":  {webSearchMode: domain.WebSearchDisabled},
		"sidechat":  {webSearchMode: domain.WebSearchLive, sidechat: ReadOnlySidechatV1},
		"provider":  {webSearchMode: domain.WebSearchLive, api: &apiBinding{}},
	} {
		event, err := c.observeWebSearch(nativewire.Event{Method: "item/completed"}, domain.NewID(), raw)
		if domain.SafeError(err).Code != domain.Unsupported || event.WebSearch != nil {
			t.Fatal("search published", name)
		}
	}
}
