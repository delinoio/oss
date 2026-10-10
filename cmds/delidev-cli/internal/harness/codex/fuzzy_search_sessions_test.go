// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

const fuzzySearchFixture = `{"sessionId":"fixture-search","query":"a","files":[{"root":"/fixture","path":"a.txt","match_type":"file","file_name":"a.txt","score":12,"indices":[0]}]}`

func TestFuzzySearchSessionDiscardsUnownedOriginalMetadata(t *testing.T) {
	for _, raw := range []string{
		fuzzySearchFixture,
		strings.Replace(fuzzySearchFixture, `"indices":[0]`, `"indices":null`, 1),
		strings.Replace(fuzzySearchFixture, `,"indices":[0]`, ``, 1),
		strings.Replace(fuzzySearchFixture, `"match_type":"file"`, `"match_type":"directory"`, 1),
		strings.Replace(fuzzySearchFixture, `"indices":[0]`, `"indices":[4294967295,0,0]`, 1),
		`{"sessionId":"foreign","query":"stale-query","files":[]}`,
		`{"sessionId":"foreign","query":"","files":[]}`,
	} {
		c, turn := observationClient()
		settings, paused := c.execution.settings, c.execution.paused
		for _, method := range []string{"fuzzyFileSearch/sessionCompleted", "fuzzyFileSearch/sessionUpdated", "fuzzyFileSearch/sessionUpdated", "fuzzyFileSearch/sessionCompleted"} {
			payload := raw
			if method == "fuzzyFileSearch/sessionCompleted" {
				payload = `{"sessionId":"completion-before-update"}`
			}
			event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: method, Params: json.RawMessage(payload)})
			if err != nil || event.Kind != MetadataEvent || event.Metadata != FuzzySearchSessionDiscarded || event.ThreadID != c.thread || event.TurnID != "" || !event.Correlated || event.Native != nil {
				t.Fatal("valid private search metadata rejected or exposed", event, err)
			}
			encoded, _ := json.Marshal(event)
			for _, private := range []string{"/fixture", "a.txt", "fixture-search", "stale-query", "completion-before-update"} {
				if strings.Contains(string(encoded), private) {
					t.Fatal("private search payload escaped")
				}
			}
			if c.problem != nil || c.execution.active != turn || c.execution.paused != paused || !reflect.DeepEqual(c.execution.settings, settings) {
				t.Fatal("search notification changed original input/settings")
			}
		}
	}
	c, _ := observationClient()
	for _, method := range []string{"fuzzyFileSearch/sessionUpdated", "fuzzyFileSearch/sessionCompleted"} {
		event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: method, Params: json.RawMessage(fuzzySearchFixture)})
		if err == nil && event.Kind != NativeExtensionEvent {
			t.Fatal("server request acquired notification authority", event)
		}
		if validationStage(method) != validationFuzzySearch {
			t.Fatal("search failure lacks redacted classification")
		}
	}
}

func TestFuzzySearchSessionRejectsMalformedClosedEnvelopes(t *testing.T) {
	payloads := []string{"", "null", "[]", "{}",
		`{"sessionId":"search","query":"a"}`, `{"sessionId":null,"query":"a","files":[]}`,
		`{"sessionId":"search","query":null,"files":[]}`, `{"sessionId":"search","files":[]}`,
		`{"sessionId":"search","query":"a","files":null}`, `{"sessionId":"search","query":"a","files":[null]}`,
		`{"sessionId":"search","query":"a","files":[],"unknown":true}`,
		`{"sessionId":"search","sessionId":"other","query":"a","files":[]}`,
		`{"SessionId":"search","query":"a","files":[]}`,
	}
	for _, pair := range [][2]string{
		{`"match_type":"file"`, `"match_type":"symlink"`},
		{`"match_type":"file"`, `"matchType":"file"`},
		{`"file_name":"a.txt"`, `"fileName":"a.txt"`},
		{`"score":12`, `"score":-1`}, {`"score":12`, `"score":4294967296`},
		{`"score":12`, `"score":null`}, {`"score":12`, `"score":1.5`},
		{`"score":12`, `"score":12,"score":12`}, {`"score":12`, `"score":12,"extra":true`},
		{`"score":12,`, ``}, {`"root":"/fixture"`, `"root":null`},
		{`"path":"a.txt"`, `"path":null`}, {`"file_name":"a.txt"`, `"file_name":null`},
		{`"match_type":"file"`, `"match_type":null`},
		{`"indices":[0]`, `"indices":[-1]`}, {`"indices":[0]`, `"indices":[4294967296]`},
		{`"indices":[0]`, `"indices":[null]`}, {`"indices":[0]`, `"indices":[1.5]`},
		{`"indices":[0]`, `"indices":{}`},
		{`"path":"a.txt"`, `"path":"` + strings.Repeat("x", 4097) + `"`},
		{`"query":"a"`, `"query":"` + strings.Repeat("x", 4097) + `"`},
		{`"sessionId":"fixture-search"`, `"sessionId":"` + strings.Repeat("x", 1025) + `"`},
	} {
		payloads = append(payloads, strings.Replace(fuzzySearchFixture, pair[0], pair[1], 1))
	}
	manyFiles, _ := json.Marshal(map[string]any{"sessionId": "search", "query": "a", "files": make([]map[string]any, 1001)})
	payloads = append(payloads, string(manyFiles), strings.Replace(fuzzySearchFixture, `"indices":[0]`, `"indices":[`+strings.Repeat("0,", 4096)+`0]`, 1), fuzzySearchFixture+` {}`)
	for i, raw := range payloads {
		c, turn := observationClient()
		if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "fuzzyFileSearch/sessionUpdated", Params: json.RawMessage(raw)}); err == nil {
			t.Fatalf("invalid updated payload %d accepted", i)
		}
		if c.execution.active != turn || c.problem != nil {
			t.Fatal("malformed search changed original input")
		}
	}
	for _, raw := range []string{"null", "{}", `{"sessionId":null}`, `{"sessionId":""}`, `{"sessionId":"search","query":"a"}`, `{"sessionId":"search","sessionId":"search"}`} {
		c, _ := observationClient()
		if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "fuzzyFileSearch/sessionCompleted", Params: json.RawMessage(raw)}); err == nil {
			t.Fatal("invalid completed payload accepted")
		}
	}
}
