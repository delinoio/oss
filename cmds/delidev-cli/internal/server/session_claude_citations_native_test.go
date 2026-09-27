package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"net/http"
	"strings"
	"testing"
)

func TestManualNativeClaudePublicCitations(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) { nativeClaudePublicDispatch(t, mode, claudePublicCitations) })
	}
}
func claudePublicCitationsResponse(w http.ResponseWriter) {
	citation := map[string]any{"type": "web_search_result_location", "cited_text": "Original native cited passage.", "title": "Fixture source", "url": "https://fixture.invalid/citation", "encrypted_index": "private-original-encrypted-index"}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range []map[string]any{
		{"type": "message_start", "message": map[string]any{"id": "msg_original_citations", "type": "message", "role": "assistant", "content": []any{}, "model": "fixture-model", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
		{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": "", "citations": []any{}}},
		{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "Original native cited answer."}},
		{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "citations_delta", "citation": citation}},
		{"type": "content_block_stop", "index": 0},
		{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 8}},
		{"type": "message_stop"},
	} {
		raw, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
	}
}
func verifyClaudePublicCitations(t *testing.T, ctx context.Context, f *firstDispatchFixture) {
	t.Helper()
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: domain.ID(f.change.Session.Id), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, row := range rows {
		if strings.Contains(string(row.Data), "private-original-encrypted-index") || strings.Contains(string(row.Data), "encrypted_index") {
			t.Fatal("opaque original index published")
		}
		m, err := store.Decode[domain.ExecutionMessage](row)
		if err != nil {
			t.Fatal(err)
		}
		if m.Claude == nil {
			continue
		}
		found++
		if m.State != domain.MessageComplete || len(m.Claude.Blocks) != 1 {
			t.Fatal("original cited provider message lost")
		}
		b := m.Claude.Blocks[0]
		h := b.Citations
		if b.Block.Text != "Original native cited answer." || h == nil || h.Validate(b.State) != nil || h.Completion != domain.ClaudeCitationsOmitted || h.Initial == nil || h.Initial.Null || len(h.Initial.Entries) != 0 || h.Completed == nil || h.Completed.Null || len(h.Completed.Entries) != 0 || len(h.Deltas) != 1 || h.Deltas[0].Text != "Original native cited passage." || h.Deltas[0].Web == nil || h.Deltas[0].Web.URL != "https://fixture.invalid/citation" {
			t.Fatal("original citation stream/omission lost")
		}
	}
	if found != 1 {
		t.Fatal("original cited message duplicated", found)
	}
}
