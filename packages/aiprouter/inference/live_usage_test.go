// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"
)

type failedLiveDialer struct{}

func (failedLiveDialer) Dial(context.Context, string, http.Header, *http.Client) (Socket, error) {
	return nil, errors.New("dial failed")
}

func TestLiveSocketUsageLifecycle(t *testing.T) {
	for _, mode := range []string{"reported", "unknown", "canceled", "dial", "resolution"} {
		t.Run(mode, func(t *testing.T) {
			h, _, _, _, usage := harness(t, func(http.ResponseWriter, *http.Request) {}, Responses)
			up, down := newFixtureSocket(), newFixtureSocket()
			h.c.SocketDialer = fixtureDialer{up}
			path := "/v1/realtime"
			if mode == "dial" {
				h.c.SocketDialer = failedLiveDialer{}
			}
			if mode == "resolution" {
				path = "/v1/live/missing"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- h.ServeRealtimeSocket(ctx, "client-secret", path, url.Values{"model": {"public"}}, down)
			}()
			if mode == "reported" || mode == "unknown" || mode == "canceled" {
				frames := []string{`{"type":"session.created"}`}
				if mode == "reported" {
					frames = []string{
						`{"type":"response.done","response":{"id":"r1","usage":{"input_tokens":7,"output_tokens":2}}}`,
						`{"type":"response.done","response":{"id":"r1","usage":{"input_tokens":7,"output_tokens":2}}}`,
						`{"type":"response.done","response":{"id":"r2","usage":{"input_tokens":3,"output_tokens":1}}}`,
					}
				}
				for _, frame := range frames {
					up.in <- []byte(frame)
					select {
					case <-down.out:
					case <-time.After(time.Second):
						t.Fatal("relay timed out")
					}
				}
				if mode == "canceled" {
					cancel()
				} else {
					up.Close()
				}
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("session did not finish")
			}
			if len(usage.records) != 1 {
				t.Fatalf("records=%d", len(usage.records))
			}
			r := usage.records[0]
			if mode == "reported" {
				if r.Usage.InputTokens == nil || *r.Usage.InputTokens != 10 || r.Usage.OutputTokens == nil || *r.Usage.OutputTokens != 3 {
					t.Fatalf("usage=%+v", r.Usage)
				}
			} else if r.Usage.InputTokens != nil || r.Usage.OutputTokens != nil {
				t.Fatal("invented usage")
			}
			if mode == "canceled" && (!r.Canceled || r.Outcome != OutcomeCanceled) {
				t.Fatalf("record=%+v", r)
			}
			if (mode == "dial" || mode == "resolution") && r.Outcome != OutcomeGatewayError {
				t.Fatalf("record=%+v", r)
			}
			if mode != "resolution" && (r.AccountID == "" || r.Model != "upstream-model") {
				t.Fatalf("identity=%+v", r)
			}
		})
	}
}
