// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/packages/tairouter/inference/reasoningreplay"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type progressWriter struct {
	header http.Header
	writes chan string
}

func (w *progressWriter) Header() http.Header         { return w.header }
func (*progressWriter) WriteHeader(int)               {}
func (*progressWriter) Flush()                        {}
func (w *progressWriter) Write(p []byte) (int, error) { w.writes <- string(p); return len(p), nil }
func firstTextFrames(w Wire) string {
	switch w {
	case Chat:
		return "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"
	case Anthropic:
		return "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n"
	default:
		return "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
	}
}
func terminalFrames(w Wire) string {
	switch w {
	case Chat:
		return "data: [DONE]\n\n"
	case Anthropic:
		return "data: {\"type\":\"message_stop\"}\n\n"
	default:
		return "data: {\"type\":\"response.completed\",\"response\":" + fixture(Responses) + "}\n\n"
	}
}
func TestTranslatedTextArrivesBeforeUpstreamTermination(t *testing.T) {
	for _, from := range []Wire{Chat, Responses, Anthropic} {
		for _, to := range []Wire{Chat, Responses, Anthropic} {
			if from == to {
				continue
			}
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				reader, writer := io.Pipe()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				defer reader.Close()
				defer writer.Close()
				sink := &progressWriter{header: http.Header{}, writes: make(chan string, 100)}
				done := make(chan error, 1)
				go func() {
					done <- (&Handler{}).deliverStream(ctx, sink, Principal{}, to, Generate, object{"stream": true}, Target{Wire: from}, &http.Response{Body: reader}, &UsageRecord{Model: "m"}, cancel, 0)
				}()
				go func() { _, _ = io.WriteString(writer, firstTextFrames(from)) }()
				text := ""
				timer := time.NewTimer(time.Second)
				defer timer.Stop()
				for !strings.Contains(text, "hello") {
					select {
					case chunk := <-sink.writes:
						text += chunk
					case err := <-done:
						t.Fatalf("ended before upstream terminal: %v %s", err, text)
					case <-timer.C:
						t.Fatal("first text waited for upstream termination")
					}
				}
				if strings.Contains(text, "response.completed") || strings.Contains(text, "message_stop") || strings.Contains(text, "[DONE]") {
					t.Fatal("early successful terminal")
				}
				go func() { _, _ = io.WriteString(writer, terminalFrames(from)); writer.Close() }()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("stream did not finish")
				}
			})
		}
	}
}
func TestTranslatedCancellationAfterProgress(t *testing.T) {
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer writer.Close()
	sink := &progressWriter{header: http.Header{}, writes: make(chan string, 100)}
	done := make(chan error, 1)
	go func() {
		done <- (&Handler{}).deliverStream(ctx, sink, Principal{}, Responses, Generate, object{"stream": true}, Target{Wire: Chat}, &http.Response{Body: reader}, &UsageRecord{}, cancel, 0)
	}()
	go func() { io.WriteString(writer, firstTextFrames(Chat)) }()
	for {
		select {
		case chunk := <-sink.writes:
			if strings.Contains(chunk, "hello") {
				cancel()
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("cancellation succeeded")
					}
				case <-time.After(time.Second):
					t.Fatal("cancel did not close upstream body")
				}
				return
			}
		case <-time.After(time.Second):
			t.Fatal("no progress")
		}
	}
}
func TestHostedCodecStreamPublishesToolBoundary(t *testing.T) {
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer writer.Close()
	events := make(chan string, 10)
	done := make(chan error, 1)
	go func() {
		_, e := (HostedCodec{}).Stream(ctx, &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}, Chat, 1<<20, func(b json.RawMessage) error { events <- string(b); return nil })
		done <- e
	}()
	go func() {
		io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c\",\"function\":{\"name\":\"search\",\"arguments\":\"{\"}}]}}]}\n\n")
	}()
	select {
	case event := <-events:
		if !strings.Contains(event, "tool_calls") {
			t.Fatal(event)
		}
	case <-time.After(time.Second):
		t.Fatal("tool start buffered")
	}
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("canceled helper succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("helper did not cancel")
	}
}

func TestTranslatedToolArgumentsArriveBeforeTermination(t *testing.T) {
	for _, to := range []Wire{Responses, Anthropic} {
		t.Run(string(to), func(t *testing.T) {
			reader, writer := io.Pipe()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			defer reader.Close()
			defer writer.Close()
			sink := &progressWriter{header: http.Header{}, writes: make(chan string, 100)}
			done := make(chan error, 1)
			tool := object{"type": "function", "name": "read", "parameters": object{"type": "object"}}
			if to == Anthropic {
				tool = object{"name": "read", "input_schema": object{"type": "object"}}
			}
			go func() {
				done <- (&Handler{}).deliverStream(ctx, sink, Principal{}, to, Generate, object{"stream": true, "tools": []any{tool}}, Target{Wire: Chat}, &http.Response{Body: reader}, &UsageRecord{}, cancel, 0)
			}()
			go func() {
				io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c\",\"function\":{\"name\":\"read\",\"arguments\":\"{ \\\"x\\\": \"}}]}}]}\n\n")
			}()
			seen := ""
			for !strings.Contains(seen, `x\"`) {
				select {
				case part := <-sink.writes:
					seen += part
				case e := <-done:
					t.Fatalf("premature end %v %s", e, seen)
				case <-time.After(time.Second):
					t.Fatalf("tool arguments buffered %s", seen)
				}
			}
			go func() {
				io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"1 }\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
				writer.Close()
			}()
			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(time.Second):
				t.Fatal("tool stream failed to close")
			}
		})
	}
}
func TestPendingReasoningBudget(t *testing.T) {
	emitter := &streamEmitter{w: &progressWriter{header: http.Header{}, writes: make(chan string, 100)}, wire: Anthropic, id: "r"}
	items := []any{reasoningItem("thinking", reasoningEnvelope{}), object{"type": "message", "role": "assistant", "content": []any{object{"type": "output_text", "text": strings.Repeat("x", 2<<20)}}}}
	e := emitter.progress(items, map[int]bool{0: false, 1: true}, object{}, Anthropic)
	if e == nil || publicError(e).Code != "translation_pending_block_limit" {
		t.Fatal(e)
	}
}
func TestHostedProgressSameID(t *testing.T) {
	acc := newTranslationStream(Chat)
	sink := &progressWriter{header: http.Header{}, writes: make(chan string, 100)}
	emitter := &streamEmitter{w: sink, wire: Responses, id: "r"}
	for _, status := range []string{"in_progress", "completed"} {
		if e := acc.consume(object{"hosted_outputs": []any{object{"id": "search", "type": "web_search_call", "status": status}}}); e != nil {
			t.Fatal(e)
		}
		items, ready, e := acc.progressItems()
		if e != nil {
			t.Fatal(e)
		}
		if e = emitter.progress(items, ready, object{}, Responses); e != nil {
			t.Fatal(e)
		}
	}
	if len(arr(acc.extra["hosted_outputs"])) != 1 {
		t.Fatal("duplicate hosted item")
	}
	all := ""
	for len(sink.writes) > 0 {
		all += <-sink.writes
	}
	if strings.Count(all, "event: response.output_item.added") != 1 || !strings.Contains(all, "response.web_search_call.in_progress") || !strings.Contains(all, "response.web_search_call.completed") {
		t.Fatal(all)
	}
}

func TestVisibleTranslationFailureDoesNotReplay(t *testing.T) {
	sends := 0
	h, _, _, _, usage := harness(t, func(w http.ResponseWriter, r *http.Request) {
		sends++
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, firstTextFrames(Chat))
		w.(http.Flusher).Flush()
	}, Chat)
	router := h.c.Router.(testRouter)
	router.route.Targets[0].Retry.TransientSends = 3
	router.route.Targets[0].Retry.FallbackOn5xx = true
	router.route.Targets = append(router.route.Targets, router.route.Targets[0])
	h.c.Router = router
	out := call(h, "/v1/responses", `{"model":"m","stream":true,"input":"hello"}`)
	body := out.Body.String()
	if !strings.Contains(body, "hello") || strings.Contains(body, "response.completed") || strings.Contains(body, "[DONE]") {
		t.Fatal(body)
	}
	if sends != 1 || len(usage.records) != 1 || usage.records[0].Sends != 1 {
		t.Fatalf("replayed visible stream sends=%d records=%+v", sends, usage.records)
	}
}
func TestIncrementalReasoningIsNotRepeatedAtCompletion(t *testing.T) {
	state := newTranslationStream(Anthropic)
	sink := &progressWriter{header: http.Header{}, writes: make(chan string, 100)}
	emitter := &streamEmitter{w: sink, wire: Chat, id: "r"}
	for _, raw := range []string{`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"thought"}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed"}}`, `{"type":"content_block_stop","index":0}`} {
		if e := state.consume(translationObject(t, raw)); e != nil {
			t.Fatal(e)
		}
		items, ready, e := state.progressItems()
		if e != nil {
			t.Fatal(e)
		}
		if e = emitter.progress(items, ready, object{}, Chat); e != nil {
			t.Fatal(e)
		}
	}
	r, e := state.result()
	if e != nil {
		t.Fatal(e)
	}
	if e = emitter.finish(r); e != nil {
		t.Fatal(e)
	}
	all := ""
	for len(sink.writes) > 0 {
		all += <-sink.writes
	}
	if strings.Count(all, `"reasoning_content":"thought"`) != 1 {
		t.Fatal(all)
	}
}

func TestDeliveryCapturesRawReasoningReplay(t *testing.T) {
	for _, mode := range []string{"native", "translated", "buffered", "truncated", "save-error"} {
		t.Run(mode, func(t *testing.T) {
			states := map[string]reasoningreplay.State{}
			replay, e := reasoningreplay.New(reasoningreplay.Hooks{
				Lookup: func(_ context.Context, key string) (reasoningreplay.State, bool, error) {
					v, ok := states[key]
					return v, ok, nil
				},
				Save: func(_ context.Context, key string, v reasoningreplay.State) error {
					if mode == "save-error" {
						return reasoningreplay.ErrMissing
					}
					states[key] = v
					return nil
				},
			}, reasoningreplay.Scope{OrganizationID: "org", UserID: "user", ConversationID: "thread", Provider: "fixture", AccountID: "account", Model: "deepseek-v3.2-pro", Destination: "https://fixture.test", Adapter: "chat", StateBinding: "version"})
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), reasoningReplayKey{}, replay))
			defer cancel()
			event := object{"choices": []any{object{"index": 0, "delta": object{"reasoning_content": "raw exact thought", "tool_calls": []any{object{"index": 0, "id": "call_1", "type": "function", "function": object{"name": "lookup", "arguments": "{}"}}}}}}}
			payload := "data: " + translationJSON(event) + "\n\n"
			if mode != "truncated" {
				payload += "data: [DONE]\n\n"
			}
			wire := Chat
			if mode == "translated" {
				wire = Responses
			}
			tool := object{"type": "function", "name": "lookup", "parameters": object{"type": "object"}}
			if wire == Chat {
				tool = object{"type": "function", "function": object{"name": "lookup", "parameters": object{"type": "object"}}}
			}
			sink := httptest.NewRecorder()
			record := &UsageRecord{}
			e = (&Handler{}).deliverStream(ctx, sink, Principal{}, wire, Generate, object{"stream": mode != "buffered", "tools": []any{tool}}, Target{Wire: Chat}, &http.Response{Body: ioNopCloser(payload)}, record, cancel, 0)
			text, lookupErr := replay.Peek(ctx, "call_1")
			if mode == "truncated" || mode == "save-error" {
				if e == nil || !errors.Is(lookupErr, reasoningreplay.ErrMissing) {
					t.Fatalf("failed stream captured state: %v %v", e, lookupErr)
				}
				if mode == "save-error" && !errors.Is(e, ErrContinuation) {
					t.Fatal(e)
				}
				if strings.Contains(sink.Body.String(), "[DONE]") {
					t.Fatal("success before replay capture")
				}
				return
			}
			if e != nil || lookupErr != nil || text != "raw exact thought" {
				t.Fatalf("raw capture: %q %v %v", text, e, lookupErr)
			}
		})
	}
}
