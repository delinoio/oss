// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

const fuzzyUpdateFixture = `{"sessionId":"fixture-search","query":"a","files":[{"root":"/fixture-private-root","path":"a.txt","match_type":"file","file_name":"a.txt","score":12,"indices":[0,4294967295]}]}`

func TestFuzzySearchRetainsOrderedPrivateWireValues(t *testing.T) {
	raw := strings.Replace(fuzzyUpdateFixture, `}]}`, `},{"root":"/fixture-private-root","path":"folder","match_type":"directory","file_name":"folder","score":0,"indices":null},{"root":"/fixture-private-root","path":"empty","match_type":"file","file_name":"empty","score":4294967295}]}`, 1)
	value, err := decodeFuzzySearch("fuzzyFileSearch/sessionUpdated", json.RawMessage(raw))
	if err != nil || value.SessionID != "fixture-search" || value.Query == nil || *value.Query != "a" || len(value.Files) != 3 {
		t.Fatal("complete ordered envelope rejected", err)
	}
	if *value.Files[0].FileName != "a.txt" || value.Files[1].MatchType != fuzzyDirectory || *value.Files[1].Score != 0 || *value.Files[2].Score != 4294967295 || !bytes.Equal(value.Files[1].Indices, []byte("null")) || value.Files[2].Indices != nil {
		t.Fatal("wire identity/order/null/omission lost")
	}
	var indices []uint32
	if json.Unmarshal(value.Files[0].Indices, &indices) != nil || len(indices) != 2 || indices[1] != 4294967295 {
		t.Fatal("original uint32 indices changed")
	}
	empty, err := decodeFuzzySearch("fuzzyFileSearch/sessionUpdated", json.RawMessage(`{"sessionId":"foreign","query":"","files":[]}`))
	if err != nil || empty.Files == nil || len(empty.Files) != 0 {
		t.Fatal("real empty observation lost", err)
	}
}
func TestFuzzySearchNoOwnerDiscardsBeforeAfterAndStaleNotifications(t *testing.T) {
	c, turn := observationClient()
	settings := c.execution.settings
	paused := c.execution.paused
	for _, method := range []string{"fuzzyFileSearch/sessionCompleted", "fuzzyFileSearch/sessionUpdated", "fuzzyFileSearch/sessionUpdated", "fuzzyFileSearch/sessionCompleted"} {
		raw := json.RawMessage(fuzzyUpdateFixture)
		if method == "fuzzyFileSearch/sessionCompleted" {
			raw = json.RawMessage(`{"sessionId":"foreign-or-stale"}`)
		}
		event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: method, Params: raw})
		if err != nil || event.Kind != MetadataEvent || event.Metadata != FuzzySearchDiscarded || event.Native != nil || event.TurnID != "" || c.execution.active != turn || c.execution.paused != paused || c.execution.settings.Model != settings.Model || c.execution.settings.Cwd != settings.Cwd {
			t.Fatal("unowned search notification changed input or settings", err)
		}
		encoded, _ := json.Marshal(event)
		if bytes.Contains(encoded, []byte("fixture-private-root")) || bytes.Contains(encoded, []byte("fixture-search")) || bytes.Contains(encoded, []byte("a.txt")) {
			t.Fatal("private result escaped disposal")
		}
	}
}
func TestFuzzySearchRejectsMalformedEnvelopesWithClosedDiagnostics(t *testing.T) {
	cases := []string{strings.Replace(fuzzyUpdateFixture, `"match_type":"file"`, `"matchType":"file"`, 1), "null", "{}", `{"sessionId":null,"query":"a","files":[]}`, `{"sessionId":"s","query":null,"files":[]}`, `{"sessionId":"s","query":"a","files":null}`, `{"sessionId":"s","query":"a","files":[null]}`, `{"sessionId":"s","query":"a","files":[],"unknown":true}`, `{"sessionId":"s","sessionId":"other","query":"a","files":[]}`, `{"SessionId":"s","query":"a","files":[]}`}
	for _, pair := range [][2]string{{`"match_type":"file"`, `"match_type":"link"`}, {`"score":12`, `"score":-1`}, {`"score":12`, `"score":4294967296`}, {`"score":12`, `"score":1.5`}, {`"score":12`, `"score":null`}, {`"score":12`, `"Score":12`}, {`"indices":[0,4294967295]`, `"indices":[-1]`}, {`"indices":[0,4294967295]`, `"indices":[4294967296]`}, {`"indices":[0,4294967295]`, `"indices":[null]`}, {`"indices":[0,4294967295]`, `"indices":{}`}, {`"file_name":"a.txt"`, `"fileName":"a.txt"`}, {`"file_name":"a.txt"`, `"file_name":null`}, {`"root":"/fixture-private-root"`, `"root":null`}, {`"path":"a.txt"`, `"path":null`}, {`"score":12`, `"score":12,"score":13`}} {
		cases = append(cases, strings.Replace(fuzzyUpdateFixture, pair[0], pair[1], 1))
	}
	cases = append(cases, strings.Replace(fuzzyUpdateFixture, `"query":"a"`, `"query":"`+strings.Repeat("a", 4097)+`"`, 1))

	for _, field := range []string{"root", "path", "match_type", "file_name", "score"} {
		var envelope map[string]json.RawMessage
		_ = json.Unmarshal([]byte(fuzzyUpdateFixture), &envelope)
		var files []map[string]json.RawMessage
		_ = json.Unmarshal(envelope["files"], &files)
		delete(files[0], field)
		envelope["files"], _ = json.Marshal(files)
		raw, _ := json.Marshal(envelope)
		cases = append(cases, string(raw))
	}
	cases = append(cases, strings.Replace(fuzzyUpdateFixture, `"score":12`, `"score":12,"private_unknown":"sentinel"`, 1))
	cases = append(cases, strings.Replace(fuzzyUpdateFixture, `"indices":[0,4294967295]`, `"indices":[`+strings.Repeat("0,", 4096)+`0]`, 1))
	cases = append(cases, strings.Repeat(" ", 1<<20)+fuzzyUpdateFixture)
	for _, field := range []string{"root", "path", "file_name"} {
		var envelope map[string]json.RawMessage
		_ = json.Unmarshal([]byte(fuzzyUpdateFixture), &envelope)
		var files []map[string]json.RawMessage
		_ = json.Unmarshal(envelope["files"], &files)
		files[0][field], _ = json.Marshal(strings.Repeat("a", 4097))
		envelope["files"], _ = json.Marshal(files)
		raw, _ := json.Marshal(envelope)
		cases = append(cases, string(raw))
	}
	var files []json.RawMessage
	for i := 0; i < 1001; i++ {
		files = append(files, json.RawMessage(`{"root":"r","path":"p","match_type":"file","file_name":"f","score":0}`))
	}
	many, _ := json.Marshal(map[string]any{"sessionId": "s", "query": "a", "files": files})
	cases = append(cases, string(many))
	for i, raw := range cases {
		c, turn := observationClient()
		_, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "fuzzyFileSearch/sessionUpdated", Params: json.RawMessage(raw)})
		if err == nil || strings.Contains(err.Error(), "fixture-private-root") || c.execution.active != turn {
			t.Fatalf("malformed fixture %d accepted or reflected private content", i)
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"sessionId":null}`, `{"sessionId":"s","query":"a"}`, `{"sessionId":"s","sessionId":"s"}`} {
		if _, err := decodeFuzzySearch("fuzzyFileSearch/sessionCompleted", json.RawMessage(raw)); err == nil {
			t.Fatal("invalid completion accepted")
		}
	}
	if validationStage("fuzzyFileSearch/sessionUpdated") != validationFuzzySearch || validationStage("fuzzyFileSearch/sessionCompleted") != validationFuzzySearch {
		t.Fatal("known family lost closed diagnostic")
	}
}
func TestFuzzySearchServerRequestsCannotUseNotificationDisposal(t *testing.T) {
	for _, method := range []string{"fuzzyFileSearch/sessionUpdated", "fuzzyFileSearch/sessionCompleted"} {
		c, turn := observationClient()
		event, _ := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: method, Params: json.RawMessage(fuzzyUpdateFixture)})
		if event.Kind == MetadataEvent || c.execution.active != turn {
			t.Fatal("request acquired notification or input authority")
		}
	}
}
