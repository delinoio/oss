package claude

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestManualNativeCitationCheckpointContinuation(t *testing.T) {
	nativeClosedSessionContinuation(t, nativeCheckpointCitations)
}

func nativeFixtureCitationsResponse(w http.ResponseWriter, n int64) {
	citation := map[string]any{"type": "web_search_result_location", "cited_text": "Original fixture quote.", "title": "Original fixture source", "url": "https://fixture.invalid/citation", "encrypted_index": "private-fixture-citation-index"}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range []map[string]any{
		{"type": "message_start", "message": map[string]any{"id": fmt.Sprintf("msg_fixture_%d", n), "type": "message", "role": "assistant", "content": []any{}, "model": "fixture-model", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
		{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": "", "citations": []any{}}},
		{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": fmt.Sprintf("Fixture response %d.", n)}},
		{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "citations_delta", "citation": citation}},
		{"type": "content_block_stop", "index": 0},
		{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 2}},
		{"type": "message_stop"},
	} {
		raw, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
	}
}
