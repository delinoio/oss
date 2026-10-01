package worker

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"strings"
	"testing"
)

func TestGrokExtraReceiptLossCannotAcquireNativeReply(t *testing.T) {
	b, client := acceptedGrokContentFixture(t)
	ctx := context.Background()
	raw, err := os.ReadFile("../harness/grok/testdata/file-tool-write.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Method domain.GrokToolMethod `json:"method"`
		Params json.RawMessage       `json:"params"`
	}
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 {
		t.Fatal("original fixture")
	}
	var payload domain.GrokToolPayload
	if domain.Decode([]byte(strings.ReplaceAll(string(rows[0].Params), "019f6de0-a760-7000-8000-000000000071", string(b.thread))), &payload) != nil {
		t.Fatal("original payload")
	}
	value := domain.ExecutionEvent{Kind: domain.ExecutionGrokToolObserved, GrokTool: &domain.ExecutionGrokToolUpdate{ID: domain.NewID(), Observation: domain.GrokToolEvent{Method: rows[0].Method, Payload: payload}}}

	client.lose = true
	b.mu.Lock()
	err = b.publishExtra(ctx, value)
	b.mu.Unlock()
	if err == nil {
		t.Fatal("persistent receipt loss was not retained")
	}
	client.lose = false
	before := len(b.journal.state.Claims)
	if err := b.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if len(b.journal.state.Claims) != before || len(client.events) != 5 || client.requests[2] != client.requests[3] || client.requests[2] != client.requests[4] {
		t.Fatal("receipt retry changed native claims or request identity")
	}
	var first, last domain.ExecutionEvent
	if json.Unmarshal(client.events[2], &first) != nil || json.Unmarshal(client.events[4], &last) != nil || first.Sequence != last.Sequence {
		t.Fatal("receipt changed original sequence")
	}
}
