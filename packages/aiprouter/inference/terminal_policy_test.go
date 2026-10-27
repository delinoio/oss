// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference/policy"
	"github.com/delinoio/oss/packages/aiprouter/providers"
)

const graphCreated = `{"type":"response.created","sequence_number":0,"response":{"id":"r","object":"response","model":"upstream-model","status":"in_progress","output":[]}}`
const graphAdded = `{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"message","id":"m","role":"assistant","status":"in_progress","content":[]}}`
const graphDone = `{"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"type":"message","id":"m","role":"assistant","status":"completed","content":[{"type":"output_text","text":"complete"}]}}`

func graphFrames(w io.Writer, frames ...string) {
	for _, frame := range frames {
		fmt.Fprintf(w, "data: %s\n\n", frame)
	}
}
func setTerminalPolicy(t *testing.T, h *Handler, wire providers.Wire, overrides string) {
	t.Helper()
	router := h.c.Router.(testRouter)
	target := &router.route.Targets[0]
	p, e := policy.Resolve(policy.Config{ProviderID: "fixture", Model: target.Model, BaseURL: target.BaseURL, Wire: wire, Overrides: []byte(overrides)})
	if e != nil {
		t.Fatal(e)
	}
	target.Policy = &p
	h.c.Router = router
}
func TestResponsesQuietTerminalRepairUsesCompleteGraph(t *testing.T) {
	closed := make(chan struct{})
	h, _, _, _, u := harness(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		graphFrames(w, graphCreated, graphAdded, graphDone)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}, Responses)
	setTerminalPolicy(t, h, providers.Responses, `{"modelResponsesTerminalRepair":{"upstream-model":{"graceMs":30}}}`)
	start := time.Now()
	w := call(h, "/v1/responses", `{"model":"m","input":"hi","stream":true}`)
	if time.Since(start) < 25*time.Millisecond || !strings.Contains(w.Body.String(), `"type":"response.completed"`) || !strings.Contains(w.Body.String(), `"sequence_number":3`) || u.records[0].Sends != 1 {
		t.Fatalf("%d %s %+v", w.Code, w.Body, u.records)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("repaired stream did not close upstream")
	}
}
func TestResponsesTerminalRepairRejectsIncompleteOrUnframedGraph(t *testing.T) {
	for _, mode := range []string{"unfinished", "mismatched", "duplicate", "unframed-terminal", "partial-tool"} {
		t.Run(mode, func(t *testing.T) {
			h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				graphFrames(w, graphCreated)
				switch mode {
				case "unfinished":
					graphFrames(w, graphAdded)
				case "mismatched":
					graphFrames(w, graphAdded, strings.Replace(graphDone, `"id":"m"`, `"id":"other"`, 1))
				case "duplicate":
					graphFrames(w, graphAdded, graphAdded, graphDone)
				case "unframed-terminal":
					graphFrames(w, graphAdded, graphDone)
					io.WriteString(w, `data: {"type":"response.completed","response":{"status":"completed"}}`)
				case "partial-tool":
					graphFrames(w, `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"f"}}`, `{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"f","call_id":"c","name":"read","arguments":"{","status":"completed"}}`)
				}
			}, Responses)
			setTerminalPolicy(t, h, providers.Responses, `{"modelResponsesTerminalRepair":{"upstream-model":{"graceMs":30}}}`)
			w := call(h, "/v1/responses", `{"model":"m","input":"hi","stream":true}`)
			if strings.Contains(w.Body.String(), `"type":"response.completed"`) || !strings.Contains(w.Body.String(), `"type":"response.incomplete"`) && !strings.Contains(w.Body.String(), "event: error") {
				t.Fatalf("unsafe synthesis %s", w.Body)
			}
		})
	}
}
func TestChatEOFToleranceRequiresOptInAndCompleteArguments(t *testing.T) {
	for _, tc := range []struct {
		name, args        string
		enabled, wantDone bool
	}{{"complete", `{}`, true, true}, {"partial", `{`, true, false}, {"disabled", `{}`, false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _, _, u := harness(t, func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":%q}}]}}]}\n\n", tc.args)
			}, Chat)
			setTerminalPolicy(t, h, providers.Chat, fmt.Sprintf(`{"openaiChatEofTolerance":%v}`, tc.enabled))
			w := call(h, "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`)
			if strings.Contains(w.Body.String(), "data: [DONE]") != tc.wantDone || u.records[0].Sends != 1 {
				t.Fatalf("%s %+v", w.Body, u.records)
			}
			if !tc.wantDone && !strings.Contains(w.Body.String(), "upstream_stream_incomplete") {
				t.Fatal(w.Body)
			}
		})
	}
}

func TestResponsesQuietTimerResetsAndCancellationDoesNotSynthesize(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelEarly), func(t *testing.T) {
			h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				graphFrames(w, graphCreated, graphAdded, graphDone)
				w.(http.Flusher).Flush()
				if !cancelEarly {
					time.Sleep(35 * time.Millisecond)
					graphFrames(w, `{"type":"response.in_progress","response":{"id":"r"}}`)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}, Responses)
			setTerminalPolicy(t, h, providers.Responses, `{"modelResponsesTerminalRepair":{"upstream-model":{"graceMs":70}}}`)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelEarly {
				time.AfterFunc(25*time.Millisecond, cancel)
			}
			req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"m","input":"hi","stream":true}`)).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer client-secret")
			out := httptest.NewRecorder()
			start := time.Now()
			h.ServeHTTP(out, req)
			completed := strings.Contains(out.Body.String(), `"type":"response.completed"`)
			if cancelEarly && completed {
				t.Fatal("cancellation synthesized completion")
			}
			if !cancelEarly && (!completed || time.Since(start) < 95*time.Millisecond) {
				t.Fatalf("quiet timer not reset: %s", out.Body)
			}
		})
	}
}
