// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type aggregateCloseBody struct {
	io.Reader
	closeErr error
}

func (b aggregateCloseBody) Close() error { return b.closeErr }

func TestAggregateResponseRejectsCloseFailure(t *testing.T) {
	receiptErr := errors.New("receipt write failed")
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[streaming], func(t *testing.T) {
			contentType := "application/json"
			payload := `{"id":"fixture","choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`
			if streaming {
				contentType = "text/event-stream"
				payload = "data: {\"id\":\"fixture\",\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n"
			}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: aggregateCloseBody{strings.NewReader(payload), receiptErr}}
			body, err := AggregateResponse(context.Background(), resp, Chat)
			if !errors.Is(err, receiptErr) {
				t.Fatalf("close error lost: %v", err)
			}
			m, err := decode(body)
			if err != nil || len(m) != 1 || m["usage"] == nil {
				t.Fatalf("failure returned content: %s, %v", body, err)
			}
			usage := readUsage(m, Chat)
			if usage.InputTokens == nil || *usage.InputTokens != 2 || usage.OutputTokens == nil || *usage.OutputTokens != 3 {
				t.Fatalf("confirmed usage lost: %s", body)
			}
		})
	}
}
