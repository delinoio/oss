// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

const allNativeSearchCommands = `{"search_query":[{"q":"fixture","recency":7,"domains":["example.test"]}],"image_query":[{"q":"fixture"}],"open":[{"ref_id":"reference","lineno":9007199254740993}],"click":[{"ref_id":"reference","id":0}],"find":[{"ref_id":"reference","pattern":"text"}],"screenshot":[{"ref_id":"reference","pageno":0}],"finance":[{"ticker":"BTC","type":"crypto","market":""}],"weather":[{"location":"Seoul","start":"2026-10-17","duration":3}],"sports":[{"tool":"sports","fn":"schedule","league":"nba","team":"GSW","opponent":"LAL","date_from":"2026-10-17","date_to":"2026-10-18","num_games":2,"locale":"ko-KR"}],"time":[{"utc_offset":"+09:00"}],"response_length":"short"}`

func TestOpenRouterSearchAdmitsCompletePinnedCommands(t *testing.T) {
	var commands map[string]json.RawMessage
	if json.Unmarshal([]byte(allNativeSearchCommands), &commands) != nil {
		t.Fatal("invalid fixture")
	}
	cases := []map[string]json.RawMessage{commands, {}}
	for name, value := range commands {
		cases = append(cases, map[string]json.RawMessage{name: value})
	}
	for _, command := range cases {
		expected, _ := json.Marshal(command)
		f := openRouterFixture(t, StandaloneSearch, func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			var projected struct {
				Input string `json:"input"`
			}
			if json.Unmarshal(raw, &projected) != nil || !bytes.Contains([]byte(projected.Input), expected) {
				t.Error("original command changed or disappeared")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"openrouter:web_search","status":"completed"},{"type":"message","content":[{"type":"output_text","text":"original result"}]}]}`)
		})
		raw, _ := json.Marshal(map[string]any{"id": "fixture", "model": "fixed-model", "commands": command})
		response, _, err := f.request(t, "/alpha/search", string(raw), nil)
		if err != nil || response.StatusCode != 200 || f.authority.keys.Load() != 1 || f.calls.Load() != 1 {
			t.Fatal("pinned commands rejected", err, response.StatusCode)
		}
	}
}
func TestOpenRouterSearchRejectsInvalidCommandsBeforeCredentials(t *testing.T) {
	for _, commands := range []string{
		`{"calculator":[{"expression":"1+1"}]}`,
		`{"screenshot":[{"ref_id":"x"}]}`,
		`{"screenshot":[{"ref_id":"x","pageno":-1}]}`,
		`{"screenshot":[{"ref_id":"x","pageno":1.5}]}`,
		`{"finance":[{"ticker":"A","type":"unknown"}]}`,
		`{"finance":[{"ticker":"A","type":"equity","extra":true}]}`,
		`{"weather":[{"location":"Seoul","duration":"7"}]}`,
		`{"sports":[{"fn":"scores","league":"nba"}]}`,
		`{"sports":[{"fn":"schedule","league":"unknown"}]}`,
		`{"sports":[{"fn":"schedule","league":"nba","tool":"other"}]}`,
		`{"time":[{"utc_offset":null}]}`,
		`{"search_query":[{"q":"test","domains":[null]}]}`,
		`{"response_length":"huge"}`,
	} {
		f := openRouterFixture(t, StandaloneSearch, func(http.ResponseWriter, *http.Request) { t.Error("invalid command reached upstream") })
		response, _, err := f.request(t, "/alpha/search", `{"id":"fixture","model":"fixed-model","commands":`+commands+`}`, nil)
		if err != nil || response.StatusCode < 400 || f.authority.keys.Load() != 0 || f.calls.Load() != 0 {
			t.Fatal("invalid command acquired credentials", commands, err)
		}
	}
}

func TestOpenRouterSearchEmptyCommandsAreDistinctFromMissing(t *testing.T) {
	f := openRouterFixture(t, StandaloneSearch, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var projected struct {
			Input string `json:"input"`
		}
		if json.Unmarshal(raw, &projected) != nil || !bytes.Contains([]byte(projected.Input), []byte("Commands:\n{}")) {
			t.Error("empty command object was not projected")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"openrouter:web_search","status":"completed"},{"type":"message","content":[{"type":"output_text","text":"original result"}]}]}`)
	})
	response, _, err := f.request(t, "/alpha/search", `{"id":"fixture","model":"fixed-model","commands":{}}`, nil)
	if err != nil || response.StatusCode != 200 || f.authority.keys.Load() != 1 || f.calls.Load() != 1 {
		t.Fatal("empty command object rejected", err)
	}
	for _, body := range []string{
		`{"id":"fixture","model":"fixed-model"}`,
		`{"id":"fixture","model":"fixed-model","commands":null}`,
		`{"id":"fixture","model":"fixed-model","commands":[]}`,
		`{"id":"fixture","model":"fixed-model","commands":"{}"}`,
		`{"id":"fixture","model":"fixed-model","commands":0}`,
		`{"id":"fixture","model":"fixed-model","commands":{},"unknown":true}`,
	} {
		g := openRouterFixture(t, StandaloneSearch, func(http.ResponseWriter, *http.Request) { t.Error("invalid commands reached upstream") })
		response, _, err := g.request(t, "/alpha/search", body, nil)
		if err != nil || response.StatusCode < 400 || g.authority.keys.Load() != 0 || g.calls.Load() != 0 {
			t.Fatal("non-object commands acquired credentials", body, err)
		}
	}
}
