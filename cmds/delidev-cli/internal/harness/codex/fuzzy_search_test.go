// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestFuzzySearchNotificationsDiscardPrivateResultsWithoutSearchAuthority(t *testing.T) {
	c, turn := observationClient()
	paused := c.execution.paused
	cases := []struct{ method, raw string }{
		{"fuzzyFileSearch/sessionCompleted", `{"sessionId":"completion-before-update"}`},
		{"fuzzyFileSearch/sessionUpdated", `{"sessionId":"fixture-search","query":"a","files":[{"root":"/fixture","path":"a.txt","match_type":"file","file_name":"a.txt","score":12,"indices":[0]}]}`},
		{"fuzzyFileSearch/sessionUpdated", `{"sessionId":"foreign-search","query":"old-query","files":[]}`},
		{"fuzzyFileSearch/sessionUpdated", `{"sessionId":"fixture-search","query":"","files":[{"root":"/fixture","path":"directory","match_type":"directory","file_name":"directory","score":4294967295,"indices":null}]}`},
		{"fuzzyFileSearch/sessionUpdated", `{"sessionId":"fixture-search","query":"a","files":[{"root":"/fixture","path":"a","match_type":"file","file_name":"a","score":0}]}`},
		{"fuzzyFileSearch/sessionCompleted", `{"sessionId":"fixture-search"}`},
	}
	for _, value := range cases {
		for i := 0; i < 2; i++ {
			event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: value.method, Params: json.RawMessage(value.raw)})
			if err != nil || event.Kind != MetadataEvent || event.Metadata != FuzzySearchDiscarded || event.Native != nil || event.TurnID != "" || c.execution.active != turn || c.execution.paused != paused {
				t.Fatal("passive search changed original ownership", value.method, err)
			}
		}
	}
	if validationStage("fuzzyFileSearch/sessionUpdated") != validationFuzzySearch || validationStage("fuzzyFileSearch/sessionCompleted") != validationFuzzySearch {
		t.Fatal("search diagnostics lost closed classification")
	}
}

func TestFuzzySearchRejectsMalformedOfficialEnvelopes(t *testing.T) {
	const good = `{"sessionId":"fixture-search","query":"a","files":[{"root":"/fixture","path":"a","match_type":"file","file_name":"a","score":12,"indices":[0]}]}`
	cases := []string{`null`, `{}`, `{"sessionId":null,"query":"a","files":[]}`, `{"sessionId":"s","files":[]}`, `{"sessionId":"s","query":null,"files":[]}`, `{"sessionId":"s","query":"a","files":null}`, `{"sessionId":"s","query":"a","files":[null]}`,
		strings.Replace(good, `"match_type":"file"`, `"match_type":"unknown"`, 1),
		strings.Replace(good, `"match_type"`, `"matchType"`, 1),
		strings.Replace(good, `"score"`, `"Score"`, 1),
		strings.Replace(good, `"sessionId"`, `"SessionId"`, 1),
		strings.Replace(good, `"file_name"`, `"fileName"`, 1),
		strings.Replace(good, `"score":12`, `"score":-1`, 1),
		strings.Replace(good, `"score":12`, `"score":4294967296`, 1),
		strings.Replace(good, `"score":12`, `"score":null`, 1),
		strings.Replace(good, `"score":12,`, ``, 1),
		strings.Replace(good, `"path":"a",`, ``, 1),
		strings.Replace(good, `"file_name":"a",`, ``, 1),
		strings.Replace(good, `"root":"/fixture"`, `"root":"`+strings.Repeat("x", 4097)+`"`, 1),
		strings.Replace(good, `"indices":[0]`, `"indices":[-1]`, 1),
		strings.Replace(good, `"indices":[0]`, `"indices":[4294967296]`, 1),
		strings.Replace(good, `"indices":[0]`, `"indices":[null]`, 1),
		strings.Replace(good, `"root":"/fixture"`, `"root":null`, 1),
		strings.Replace(good, `"score":12`, `"score":12,"score":13`, 1),
		strings.Replace(good, `"score":12`, `"score":12,"unknown":true`, 1),
		strings.Replace(good, `"query":"a"`, `"query":"a","query":"b"`, 1),
		strings.Replace(good, `"query":"a"`, `"query":"`+strings.Repeat("a", 4097)+`"`, 1),
		strings.Replace(good, `"indices":[0]`, `"indices":[`+strings.Repeat("0,", maxFuzzyIndices)+`0]`, 1),
		`{"sessionId":"s","query":"a","files":[` + strings.Repeat(`{"root":"/f","path":"a","match_type":"file","file_name":"a","score":0},`, maxFuzzyFiles) + `{"root":"/f","path":"a","match_type":"file","file_name":"a","score":0}]}`,
	}
	for _, raw := range cases {
		c, turn := observationClient()
		if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "fuzzyFileSearch/sessionUpdated", Params: json.RawMessage(raw)}); err == nil || c.execution.active != turn {
			t.Fatal("malformed search accepted or changed input")
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"sessionId":null}`, `{"sessionId":"s","unknown":true}`, `{"sessionId":"s","sessionId":"s"}`} {
		c, _ := observationClient()
		if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "fuzzyFileSearch/sessionCompleted", Params: json.RawMessage(raw)}); err == nil {
			t.Fatal("malformed completion accepted")
		}
	}
}

func TestFuzzySearchServerRequestsNeverAcquirePassiveNotificationHandling(t *testing.T) {
	for _, method := range []string{"fuzzyFileSearch/sessionUpdated", "fuzzyFileSearch/sessionCompleted"} {
		c, turn := observationClient()
		event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: method, Params: json.RawMessage(`{"sessionId":"fixture-search"}`)})
		if err == nil && event.Kind == MetadataEvent || c.execution.active != turn {
			t.Fatal("server request gained notification authority")
		}
	}
}
