// SPDX-License-Identifier: Apache-2.0
package hosted

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

type executorFunc func(context.Context, inference.AdapterRequest) (*http.Response, error)

func (f executorFunc) Do(ctx context.Context, r inference.AdapterRequest) (*http.Response, error) {
	return f(ctx, r)
}
func streamInput() inference.AdapterInput {
	in := input()
	m, _ := decode(in.Body)
	m["stream"] = true
	in.Body, _ = json.Marshal(m)
	return in
}
func readEvents(body io.Reader) <-chan string {
	events := make(chan string, 32)
	go func() {
		defer close(events)
		scan := bufio.NewScanner(body)
		for scan.Scan() {
			if strings.HasPrefix(scan.Text(), "data: ") {
				events <- strings.TrimPrefix(scan.Text(), "data: ")
			}
		}
	}()
	return events
}
func nextEvent(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case event, ok := <-ch:
		if !ok {
			t.Fatal("stream ended before event")
		}
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("event waited for provider termination")
		return ""
	}
}
func gatedAnswer(ctx context.Context, finish <-chan struct{}, producerDone chan<- struct{}, valid bool) *http.Response {
	r, w := io.Pipe()
	go func() {
		defer close(producerDone)
		defer w.Close()
		_, err := io.WriteString(w, "data: {\"id\":\"answer\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"}}]}\n\n")
		if err != nil {
			return
		}
		select {
		case <-finish:
		case <-ctx.Done():
			w.CloseWithError(ctx.Err())
			return
		}
		if valid {
			io.WriteString(w, "data: {\"id\":\"answer\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" second\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
		}
	}()
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: r}
}
func TestFirstModelTextBeforeTermination(t *testing.T) {
	ctx, _ := budget(2)
	finish := make(chan struct{})
	producerDone := make(chan struct{})
	a, _ := New(provider{}, Config{Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
		t.Error("unexpected helper")
		return nil, inference.ErrUnsupported
	})})
	e := executorFunc(func(ctx context.Context, req inference.AdapterRequest) (*http.Response, error) {
		m, _ := decode(req.Body)
		if m["stream"] != true {
			t.Error("model streaming disabled")
		}
		return gatedAnswer(ctx, finish, producerDone, true), inference.PhysicalSend(ctx, func(context.Context) error { return nil })
	})
	r, _, err := a.Execute(ctx, streamInput(), e, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	events := readEvents(r.Body)
	first := nextEvent(t, events)
	if !strings.Contains(first, `"content":"first"`) {
		t.Fatal(first)
	}
	select {
	case <-producerDone:
		t.Fatal("provider already terminated")
	default:
	}
	close(finish)
	var all strings.Builder
	all.WriteString(first)
	for v := range events {
		all.WriteString(v)
	}
	if strings.Count(all.String(), `"content":"first"`) != 1 || !strings.Contains(all.String(), " second") || !strings.Contains(all.String(), "[DONE]") {
		t.Fatal(all.String())
	}
}
func TestHelperProgressPrecedesExecutionAndFinalText(t *testing.T) {
	ctx, sends := budget(4)
	releaseHelper := make(chan struct{})
	enteredHelper := make(chan struct{})
	finish := make(chan struct{})
	producerDone := make(chan struct{})
	models := 0
	a, _ := New(provider{}, Config{Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
		return operationFunc(func(ctx context.Context, _ OperationRequest) (OperationResult, error) {
			err := inference.PhysicalSend(ctx, func(context.Context) error { return nil })
			close(enteredHelper)
			select {
			case <-releaseHelper:
			case <-ctx.Done():
				return OperationResult{}, ctx.Err()
			}
			return OperationResult{Output: "source"}, err
		}), nil
	})})
	e := executorFunc(func(ctx context.Context, _ inference.AdapterRequest) (*http.Response, error) {
		if err := inference.PhysicalSend(ctx, func(context.Context) error { return nil }); err != nil {
			return nil, err
		}
		models++
		if models == 1 {
			return response(call("web_search", `{"query":"a"}`)), nil
		}
		return gatedAnswer(ctx, finish, producerDone, true), nil
	})
	r, _, err := a.Execute(ctx, streamInput(), e, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	events := readEvents(r.Body)
	begin := nextEvent(t, events)
	if !strings.Contains(begin, "in_progress") || strings.Contains(begin, "tool_calls") {
		t.Fatal(begin)
	}
	<-enteredHelper
	close(releaseHelper)
	completed := nextEvent(t, events)
	if !strings.Contains(completed, "completed") {
		t.Fatal(completed)
	}
	text := nextEvent(t, events)
	if !strings.Contains(text, "first") {
		t.Fatal(text)
	}
	close(finish)
	for v := range events {
		if strings.Contains(v, "tool_calls") {
			t.Fatal("synthetic tool leaked", v)
		}
	}
	if *sends != 3 {
		t.Fatal(*sends)
	}
}
func TestCloseCancelsAndJoinsHelper(t *testing.T) {
	ctx, _ := budget(3)
	entered := make(chan struct{})
	stopped := make(chan struct{})
	a, _ := New(provider{}, Config{Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
		return operationFunc(func(ctx context.Context, _ OperationRequest) (OperationResult, error) {
			close(entered)
			defer close(stopped)
			<-ctx.Done()
			return OperationResult{}, ctx.Err()
		}), nil
	})})
	e := &executor{replies: []string{call("web_search", `{"query":"a"}`)}}
	r, _, err := a.Execute(ctx, streamInput(), e, nil)
	if err != nil {
		t.Fatal(err)
	}
	events := readEvents(r.Body)
	nextEvent(t, events)
	<-entered
	closed := make(chan struct{})
	go func() { r.Body.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close failed to join")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("operation outlived Close")
	}
	if e.sends != 1 {
		t.Fatal("unexpected followup")
	}
}
func TestVisibleFailureDoesNotReplayOrComplete(t *testing.T) {
	ctx, sends := budget(4)
	finish := make(chan struct{})
	done := make(chan struct{})
	a, _ := New(provider{}, Config{Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
		t.Error("unexpected helper")
		return nil, nil
	})})
	e := executorFunc(func(ctx context.Context, _ inference.AdapterRequest) (*http.Response, error) {
		err := inference.PhysicalSend(ctx, func(context.Context) error { return nil })
		return gatedAnswer(ctx, finish, done, false), err
	})
	r, _, err := a.Execute(ctx, streamInput(), e, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	events := readEvents(r.Body)
	if !strings.Contains(nextEvent(t, events), "first") {
		t.Fatal("missing prefix")
	}
	close(finish)
	var rest strings.Builder
	for v := range events {
		rest.WriteString(v)
	}
	if !strings.Contains(rest.String(), "error") || strings.Contains(rest.String(), "[DONE]") || *sends != 1 {
		t.Fatal(rest.String(), *sends)
	}
}

func TestHelperFailureClosesProgressBeforeError(t *testing.T) {
	for _, kind := range []Kind{Search, Image, Video} {
		t.Run(string(kind), func(t *testing.T) {
			ctx, _ := budget(3)
			cfg := Config{EnableVideo: kind == Video, Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
				return operationFunc(func(context.Context, OperationRequest) (OperationResult, error) {
					return OperationResult{}, inference.ErrAmbiguous
				}), nil
			})}
			a, _ := New(provider{}, cfg)
			in := streamInput()
			if kind == Image {
				m, _ := decode(in.Body)
				m["tools"] = []any{object{"type": "image_generation"}}
				in.Body, _ = json.Marshal(m)
			}
			args := `{"prompt":"draw"}`
			if kind == Search {
				args = `{"query":"a"}`
			}
			e := &executor{replies: []string{call(name(kind), args)}}
			r, _, err := a.Execute(ctx, in, e, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			events := readEvents(r.Body)
			begin := nextEvent(t, events)
			failed := nextEvent(t, events)
			terminal := nextEvent(t, events)
			if !strings.Contains(begin, "in_progress") || !strings.Contains(failed, `"status":"failed"`) || !strings.Contains(terminal, "upstream_reset_replay_refused") {
				t.Fatal(begin, failed, terminal)
			}
			for v := range events {
				if v == "[DONE]" {
					t.Fatal("failed helper marked completed")
				}
			}
			if e.sends != 1 {
				t.Fatal("replayed model")
			}
		})
	}
}

type wireProvider struct {
	provider
	wire inference.Wire
}

func (p wireProvider) Build(ctx context.Context, in inference.AdapterInput) (inference.AdapterRequest, error) {
	r, err := p.provider.Build(ctx, in)
	r.ResponseWire = p.wire
	return r, err
}
func TestCrossWireFirstTextBeforeTerminal(t *testing.T) {
	for _, wire := range []inference.Wire{inference.Responses, inference.Anthropic} {
		t.Run(string(wire), func(t *testing.T) {
			ctx, _ := budget(2)
			finish := make(chan struct{})
			done := make(chan struct{})
			a, _ := New(wireProvider{wire: wire}, Config{Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
				return nil, inference.ErrUnsupported
			})})
			e := executorFunc(func(ctx context.Context, _ inference.AdapterRequest) (*http.Response, error) {
				r, w := io.Pipe()
				go func() {
					defer close(done)
					defer w.Close()
					prefix := `data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"early"}` + "\n\n"
					terminal := `data: {"type":"response.completed","response":{"id":"r","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"early"}]}]}}` + "\n\n"
					if wire == inference.Anthropic {
						prefix = `data: {"type":"message_start","message":{"id":"m","content":[]}}` + "\n\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"early"}}` + "\n\n"
						terminal = `data: {"type":"content_block_stop","index":0}` + "\n\n" + `data: {"type":"message_stop"}` + "\n\n"
					}
					if _, err := io.WriteString(w, prefix); err != nil {
						return
					}
					select {
					case <-finish:
					case <-ctx.Done():
						return
					}
					io.WriteString(w, terminal)
				}()
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: r}, nil
			})
			r, _, err := a.Execute(ctx, streamInput(), e, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			events := readEvents(r.Body)
			first := nextEvent(t, events)
			if !strings.Contains(first, "early") {
				t.Fatal(first)
			}
			select {
			case <-done:
				t.Fatal("buffered until terminal")
			default:
			}
			close(finish)
			for v := range events {
				if strings.Contains(v, `"error"`) {
					t.Fatal(v)
				}
			}
		})
	}
}

func TestConfiguredHelperTimeoutBounds(t *testing.T) {
	c := defaults(Config{SearchTimeout: 600 * time.Second, VisionTimeout: 2147483647 * time.Millisecond, MediaTimeout: 300 * time.Second})
	if c.SearchTimeout != 600*time.Second || c.VisionTimeout != 2147483647*time.Millisecond || c.MediaTimeout != 300*time.Second {
		t.Fatal("valid service timeout narrowed", c)
	}
	c = defaults(Config{SearchTimeout: 600*time.Second + 1, VisionTimeout: 2147483647*time.Millisecond + 1, MediaTimeout: 300*time.Second + 1})
	if c.SearchTimeout != 60*time.Second || c.VisionTimeout != 45*time.Second || c.MediaTimeout != 300*time.Second {
		t.Fatal("invalid timeout did not use bounded default", c)
	}
}
