// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestFuzzySearchDiscardsOnlyBoundedNotifications(t *testing.T) {
	for _, files := range [][]any{{}, {fuzzyRecord()}, {fuzzyRecord(), fuzzyRecord()}} {
		raw, _ := json.Marshal(map[string]any{"sessionId": "foreign-session", "query": "private-query", "files": files})
		c, turn := observationClient()
		settings, paused := c.execution.settings, c.execution.paused
		for i := 0; i < 3; i++ {
			event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "fuzzyFileSearch/sessionUpdated", Params: raw})
			if err != nil || event.Kind != MetadataEvent || event.Metadata != FuzzySearchDiscarded || !event.Correlated || event.ThreadID != c.thread || event.TurnID != "" || event.Native != nil {
				t.Fatal("valid passive search metadata rejected or exposed", event, err)
			}
			encoded, _ := json.Marshal(event)
			if strings.Contains(string(encoded), "private-") || strings.Contains(string(encoded), "foreign-session") {
				t.Fatal("private search payload escaped")
			}
			if c.execution.active != turn || c.execution.paused != paused || !reflect.DeepEqual(c.execution.settings, settings) || c.problem != nil {
				t.Fatal("passive search metadata changed original execution")
			}
		}
	}
	valid, _ := json.Marshal(map[string]any{"sessionId": "fixture-session", "query": "a", "files": []any{}})
	c, _ := observationClient()
	event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: "fuzzyFileSearch/sessionUpdated", Params: valid})
	if err == nil && event.Kind != NativeExtensionEvent {
		t.Fatal("server request acquired notification authority", event)
	}
	unbound := &Client{}
	event, err = unbound.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "fuzzyFileSearch/sessionUpdated", Params: valid})
	if err != nil || event.Kind != NativeExtensionEvent {
		t.Fatal("unbound notification gained execution authority", event, err)
	}
	if validationStage("fuzzyFileSearch/sessionUpdated") != validationFuzzySearch {
		t.Fatal("search failure lacks a redacted classification")
	}
}

func TestFuzzySearchPreservesOriginalAcceptedTurn(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "normal")
	settings := c.execution.settings
	accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	for i := 0; i < 3; i++ {
		fixtureSignal(t, c, "notify", map[string]any{"method": "fuzzyFileSearch/sessionUpdated", "params": map[string]any{"sessionId": "unsolicited", "query": "a", "files": []any{}}})
		event := nextKind(t, c, MetadataEvent)
		if event.Metadata != FuzzySearchDiscarded || event.Native != nil || c.problem != nil || c.execution.active != accepted.TurnID || !reflect.DeepEqual(c.execution.settings, settings) {
			t.Fatal("notification changed original ownership", event, c.problem)
		}
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	terminal := nextKind(t, c, TurnCompletedEvent)
	nextKind(t, c, ThreadStatusEvent)
	if terminal.TurnID != accepted.TurnID || c.execution.active != "" || c.problem != nil {
		t.Fatal("ordinary original turn did not complete", terminal, c.problem)
	}
	if len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "fuzzyFileSearch/sessionStart")) != 0 || len(requestsOf(t, capture, "fuzzyFileSearch/sessionUpdate")) != 0 {
		t.Fatal("passive notification triggered native work")
	}
}

func fuzzyPayload(t *testing.T, file map[string]any) json.RawMessage {
	t.Helper()
	files := []any{}
	if file != nil {
		files = append(files, file)
	}
	raw, err := json.Marshal(map[string]any{"sessionId": "private-session", "query": "", "files": files})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func fuzzyRecord() map[string]any {
	return map[string]any{"root": "/private-root", "path": "private-path", "file_name": "private-name", "match_type": "file", "score": uint32(4294967295)}
}
func TestFuzzySearchCompleteShapesAndPresence(t *testing.T) {
	for _, spelling := range []string{"match_type", "matchType"} {
		for _, kind := range []string{"file", "directory"} {
			for _, indices := range []any{"omitted", nil, []uint32{}, []uint32{4294967295, 0, 2, 2}} {
				file := fuzzyRecord()
				delete(file, "match_type")
				file[spelling] = kind
				if indices != "omitted" {
					file["indices"] = indices
				}
				raw := fuzzyPayload(t, file)
				c, turn := observationClient()
				for _, method := range []string{"fuzzyFileSearch/sessionCompleted", "fuzzyFileSearch/sessionUpdated", "fuzzyFileSearch/sessionCompleted"} {
					params := raw
					if method == "fuzzyFileSearch/sessionCompleted" {
						params = json.RawMessage(`{"sessionId":"private-session"}`)
					}
					event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: method, Params: params})
					if err != nil || event.Metadata != FuzzySearchDiscarded || event.Native != nil || c.execution.active != turn || c.problem != nil {
						t.Fatal("passive shape changed execution", event, err)
					}
					encoded, _ := json.Marshal(event)
					if strings.Contains(string(encoded), "private-") {
						t.Fatal("private payload escaped")
					}
				}
				decoded, err := decodeFuzzySearch(nativewire.Event{Method: "fuzzyFileSearch/sessionUpdated", Params: raw}, nativewire.MaxFrame)
				if err != nil || !reflect.DeepEqual(decoded.Files, json.RawMessage(raw[strings.Index(string(raw), "["):strings.LastIndex(string(raw), "]")+1])) {
					t.Fatal("ordered private files changed", err)
				}
			}
		}
	}
}
func TestFuzzySearchRejectsMalformedMetadata(t *testing.T) {
	payloads := []json.RawMessage{}
	for _, raw := range []string{"", "null", "[]", "{}", `{"sessionId":"s","query":"q"}`, `{"sessionId":null,"query":"q","files":[]}`, `{"sessionId":"s","query":null,"files":[]}`, `{"sessionId":"s","query":"q","files":null}`, `{"sessionId":"s","query":"q","files":[null]}`, `{"sessionId":"s","query":"q","files":[],"unknown":1}`, `{"sessionId":"s","sessionId":"x","query":"q","files":[]}`, `{"sessionId":"s","query":"q","files":[],"threadId":"foreign"}`} {
		payloads = append(payloads, json.RawMessage(raw))
	}
	for _, field := range []string{"root", "path", "file_name", "match_type", "score"} {
		file := fuzzyRecord()
		delete(file, field)
		payloads = append(payloads, fuzzyPayload(t, file))
		file[field] = nil
		payloads = append(payloads, fuzzyPayload(t, file))
	}
	for _, pair := range []struct {
		field string
		value any
	}{{"match_type", "other"}, {"score", -1}, {"score", uint64(4294967296)}, {"score", 1.5}, {"indices", []any{nil}}, {"indices", []int{-1}}, {"indices", []uint64{4294967296}}, {"indices", []float64{1.5}}, {"indices", make([]uint32, 4097)}, {"root", strings.Repeat("x", 4097)}, {"file_name", "bad\x00name"}, {"fileName", "renamed"}, {"matchType", "file"}} {
		file := fuzzyRecord()
		file[pair.field] = pair.value
		payloads = append(payloads, fuzzyPayload(t, file))
	}
	for _, raw := range []string{`{"root":"r","path":"p","file_name":"n","match_type":"file","score":1,"score":2}`, `{"root":"r","path":"p","file_name":"n","match_type":"file","score":1,"indices":[],"indices":null}`} {
		payloads = append(payloads, json.RawMessage(`{"sessionId":"s","query":"q","files":[`+raw+`]}`))
	}
	for _, raw := range payloads {
		c, turn := observationClient()
		_, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "fuzzyFileSearch/sessionUpdated", Params: raw})
		if err == nil || c.execution.active != turn {
			t.Fatal("malformed observation accepted or replaced input")
		}
	}
	for _, raw := range []string{`{"sessionId":"s","query":null}`, `{"sessionId":"s","files":null}`, `{"sessionId":null}`, `{"sessionId":"s","turnId":"foreign"}`} {
		if _, err := decodeFuzzySearch(nativewire.Event{Method: "fuzzyFileSearch/sessionCompleted", Params: json.RawMessage(raw)}, nativewire.MaxFrame); err == nil {
			t.Fatal("malformed completion accepted")
		}
	}
}
func TestFuzzySearchExactBounds(t *testing.T) {
	file := fuzzyRecord()
	file["root"] = strings.Repeat("x", 4096)
	file["path"] = strings.Repeat("x", 4096)
	file["file_name"] = strings.Repeat("x", 4096)
	file["indices"] = make([]uint32, 4096)
	for _, raw := range []json.RawMessage{fuzzyPayload(t, file), json.RawMessage(`{"sessionId":"` + strings.Repeat("x", 1024) + `","query":"` + strings.Repeat("x", 4096) + `","files":[]}`)} {
		if _, err := decodeFuzzySearch(nativewire.Event{Method: "fuzzyFileSearch/sessionUpdated", Params: raw}, nativewire.MaxFrame); err != nil {
			t.Fatal("exact string/index bound rejected", err)
		}
	}
	for _, count := range []int{1000, 1001} {
		files := make([]any, count)
		for i := range files {
			files[i] = fuzzyRecord()
		}
		raw, _ := json.Marshal(map[string]any{"sessionId": "s", "query": "q", "files": files})
		_, err := decodeFuzzySearch(nativewire.Event{Method: "fuzzyFileSearch/sessionUpdated", Params: raw}, nativewire.MaxFrame)
		if (err == nil) != (count == 1000) {
			t.Fatal("file bound incorrect", count, err)
		}
	}
	for _, field := range []string{"sessionId", "query"} {
		value := map[string]any{"sessionId": "s", "query": "q", "files": []any{}}
		limit := 1024
		if field == "query" {
			limit = 4096
		}
		value[field] = strings.Repeat("x", limit+1)
		raw, _ := json.Marshal(value)
		if _, err := decodeFuzzySearch(nativewire.Event{Method: "fuzzyFileSearch/sessionUpdated", Params: raw}, nativewire.MaxFrame); err == nil {
			t.Fatal("oversized descriptor accepted")
		}
	}
	if _, err := decodeFuzzySearch(nativewire.Event{Method: "fuzzyFileSearch/sessionUpdated", Params: json.RawMessage(strings.Repeat(" ", nativewire.MaxFrame+1))}, nativewire.MaxFrame); err == nil {
		t.Fatal("oversized frame accepted")
	}
}

func TestFuzzySearchMalformedLogsStayRedacted(t *testing.T) {
	c, _, _, _ := boundTurnFixture(t, "normal")
	if _, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode)); err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	var logs bytes.Buffer
	c.logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fixtureSignal(t, c, "notify", map[string]any{"method": "fuzzyFileSearch/sessionUpdated", "params": map[string]any{"sessionId": "private-sentinel", "query": "private-sentinel", "files": []any{map[string]any{"root": "private-sentinel"}}}})
	if _, err := c.NextEvent(context.Background()); err == nil {
		t.Fatal("malformed event did not retain rejection")
	}
	if strings.Contains(logs.String(), "private-sentinel") || strings.Contains(logs.String(), "fuzzyFileSearch") || !strings.Contains(logs.String(), "fuzzy-search-session") {
		t.Fatal("native rejection logging escaped the closed classification")
	}
}
