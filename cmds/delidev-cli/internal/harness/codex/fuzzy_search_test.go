// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

const fuzzyResultFixture = `{"root":"/private-root","path":"private-file","match_type":"file","file_name":"private-name","score":12}`

func fuzzyUpdateFixture(result string) string {
	return `{"sessionId":"private-search","query":"private-query","files":[` + result + `]}`
}

func TestFuzzySearchRetainsPrivateSchemaDistinctions(t *testing.T) {
	for _, test := range []struct {
		indices       string
		present, null bool
		values        []uint32
	}{
		{"", false, false, nil},
		{`,"indices":null`, true, true, nil},
		{`,"indices":[]`, true, false, []uint32{}},
		{`,"indices":[4294967295,0,12,0]`, true, false, []uint32{4294967295, 0, 12, 0}},
	} {
		raw := fuzzyUpdateFixture(strings.TrimSuffix(fuzzyResultFixture, "}") + test.indices + "}")
		value, err := decodeFuzzySearchUpdate([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		indices := (*value.Files)[0].Indices
		if indices.Present != test.present || indices.Null != test.null || !reflect.DeepEqual(indices.Values, test.values) {
			t.Fatal("native indices lost presence, nullness or original order", indices)
		}
	}
}

func TestFuzzySearchNotificationsDiscardWithoutOwnership(t *testing.T) {
	c, turn := observationClient()
	settings := c.execution.settings
	updates := []string{
		fuzzyUpdateFixture(fuzzyResultFixture),
		fuzzyUpdateFixture(strings.Replace(fuzzyResultFixture, ":12", ":4294967295", 1)),
		fuzzyUpdateFixture(strings.ReplaceAll(fuzzyResultFixture, `"file"`, `"directory"`)),
		`{"sessionId":"foreign-search","query":"","files":[]}`,
	}
	// Completion has no prerequisite because no search owner exists at all.
	for _, raw := range append([]string{`{"sessionId":"unknown-before-update"}`}, updates...) {
		method := "fuzzyFileSearch/sessionUpdated"
		if !strings.Contains(raw, `"files"`) {
			method = "fuzzyFileSearch/sessionCompleted"
		}
		for range 3 {
			event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: method, Params: json.RawMessage(raw)})
			if err != nil || event.Kind != MetadataEvent || event.Metadata != FuzzySearchDiscarded || !event.Correlated || event.ThreadID != c.thread || event.TurnID != "" || event.Native != nil {
				t.Fatal("passive search notification was not discarded", event, err)
			}
			encoded, _ := json.Marshal(event)
			if strings.Contains(string(encoded), "private-") || strings.Contains(string(encoded), "foreign-search") || strings.Contains(string(encoded), "unknown-before-update") {
				t.Fatal("private search payload escaped")
			}
			if c.execution.active != turn || c.execution.paused || c.problem != nil || !reflect.DeepEqual(settings, c.execution.settings) {
				t.Fatal("search notification changed original input or settings")
			}
		}
		if validationStage(method) != validationFuzzySearch {
			t.Fatal("missing closed diagnostic classification")
		}
		event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: method, Params: json.RawMessage(raw)})
		if err == nil && event.Kind != NativeExtensionEvent {
			t.Fatal("server request gained notification authority")
		}
		unbound := &Client{}
		event, err = unbound.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: method, Params: json.RawMessage(raw)})
		if err != nil || event.Kind != NativeExtensionEvent {
			t.Fatal("unbound notification gained execution ownership")
		}
	}
}

func TestFuzzySearchRejectsMalformedNativePayloads(t *testing.T) {
	badResults := []string{
		`null`, `{}`, strings.Replace(fuzzyResultFixture, `"file"`, `"other"`, 1),
		strings.Replace(fuzzyResultFixture, `"match_type"`, `"matchType"`, 1),
		strings.Replace(fuzzyResultFixture, `"file_name"`, `"fileName"`, 1),
		strings.Replace(fuzzyResultFixture, `"score"`, `"SCORE"`, 1),
		strings.Replace(fuzzyResultFixture, `:12`, `:-1`, 1),
		strings.Replace(fuzzyResultFixture, `:12`, `:4294967296`, 1),
		strings.Replace(fuzzyResultFixture, `:12`, `:1.5`, 1),
		strings.Replace(fuzzyResultFixture, `:12`, `:null`, 1),
		strings.Replace(fuzzyResultFixture, `"root":"/private-root"`, `"root":null`, 1),
		strings.TrimSuffix(fuzzyResultFixture, "}") + `,"root":"duplicate"}`,
		strings.TrimSuffix(fuzzyResultFixture, "}") + `,"unknown":true}`,
	}
	for _, indices := range []string{`[null]`, `[-1]`, `[4294967296]`, `[0.5]`, `["0"]`, `{}`, `true`, `[` + strings.Repeat("0,", maxFuzzySearchIndices) + `0]`} {
		badResults = append(badResults, strings.TrimSuffix(fuzzyResultFixture, "}")+`,"indices":`+indices+`}`)
	}
	payloads := []string{
		`null`, `[]`, `{}`, `{"sessionId":"id","query":"q"}`,
		`{"sessionId":null,"query":"q","files":[]}`,
		`{"sessionId":"id","query":null,"files":[]}`,
		`{"sessionId":"id","query":"q","files":null}`,
		`{"sessionId":"id","query":"q","files":[],"unknown":true}`,
		`{"sessionId":"id","query":"q","files":[],"files":[]}`,
		`{"sessionid":"id","query":"q","files":[]}`,
		`{"sessionId":"id","query":"nul\u0000query","files":[]}`,
		fmt.Sprintf(`{"sessionId":%q,"query":"q","files":[]}`, strings.Repeat("s", 1025)),
		fmt.Sprintf(`{"sessionId":"id","query":%q,"files":[]}`, strings.Repeat("q", maxFuzzySearchText+1)),
		`{"sessionId":"id","query":"q","files":[` + strings.Repeat(fuzzyResultFixture+",", maxFuzzySearchFiles) + fuzzyResultFixture + `]}`,
		strings.Repeat(" ", 1<<20) + `{}`,
	}
	for _, key := range []string{"root", "path", "match_type", "file_name", "score"} {
		for _, missing := range []bool{false, true} {
			var file map[string]any
			_ = json.Unmarshal([]byte(fuzzyResultFixture), &file)
			if missing {
				delete(file, key)
			} else {
				file[key] = nil
			}
			raw, _ := json.Marshal(file)
			payloads = append(payloads, fuzzyUpdateFixture(string(raw)))
		}
	}
	for _, result := range badResults {
		payloads = append(payloads, fuzzyUpdateFixture(result))
	}
	for _, key := range []string{"root", "path", "file_name"} {
		var file map[string]any
		_ = json.Unmarshal([]byte(fuzzyResultFixture), &file)
		file[key] = strings.Repeat("x", maxFuzzySearchText+1)
		raw, _ := json.Marshal(file)
		payloads = append(payloads, fuzzyUpdateFixture(string(raw)))
	}
	for i, raw := range payloads {
		c, turn := observationClient()
		if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "fuzzyFileSearch/sessionUpdated", Params: json.RawMessage(raw)}); err == nil {
			t.Fatalf("invalid update %d accepted", i)
		}
		if c.execution.active != turn || c.execution.paused || c.problem != nil {
			t.Fatal("decoder rejection changed input ownership")
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"sessionId":null}`, `{"sessionId":""}`, `{"sessionid":"id"}`, `{"sessionId":"id","sessionId":"other"}`, `{"sessionId":"id","query":"q"}`, fmt.Sprintf(`{"sessionId":%q}`, strings.Repeat("x", 1025))} {
		c, _ := observationClient()
		if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "fuzzyFileSearch/sessionCompleted", Params: json.RawMessage(raw)}); err == nil {
			t.Fatal("invalid completion accepted")
		}
	}
}

func TestFuzzySearchPreservesOrdinaryTurnCompletion(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "normal")
	accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	for range 3 {
		fixtureSignal(t, c, "notify", map[string]any{"method": "fuzzyFileSearch/sessionCompleted", "params": map[string]any{"sessionId": "foreign-search"}})
		if event := nextKind(t, c, MetadataEvent); event.Metadata != FuzzySearchDiscarded || c.execution.active != accepted.TurnID {
			t.Fatal("search changed accepted input")
		}
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	if event := nextKind(t, c, TurnCompletedEvent); event.TurnID != accepted.TurnID {
		t.Fatal("wrong turn completed")
	}
	nextKind(t, c, ThreadStatusEvent)
	if c.execution.active != "" || c.problem != nil || len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "fuzzyFileSearch/sessionStart")) != 0 {
		t.Fatal("search notification changed execution or sent a search")
	}
}
