// SPDX-License-Identifier: Apache-2.0
package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

// StreamingCodec preserves the central codec's signature/tool handling while
// exposing canonical Chat chunks before the provider terminal event.
type StreamingCodec interface {
	Stream(context.Context, *http.Response, inference.Wire, int64, func(json.RawMessage) error) (json.RawMessage, error)
}
type streamResult struct {
	response *http.Response
	wire     inference.Wire
	err      error
}
type liveStream struct {
	writer                 *io.PipeWriter
	reader                 *io.PipeReader
	ready                  chan streamResult
	done                   chan struct{}
	cancel                 context.CancelFunc
	started                bool
	id                     string
	text, reasoning        string
	window                 bool
	pending                []object
	sentFields             map[string]bool
	pendingBytes, maxBytes int64
}

func (a *Adapter) Execute(ctx context.Context, in inference.AdapterInput, e inference.AdapterExecutor, s inference.AdapterStateStore) (*http.Response, inference.Wire, error) {
	body, err := decode(in.Body)
	if err != nil {
		return nil, "", err
	}
	if body["stream"] != true || in.Operation != inference.Generate {
		return a.execute(ctx, in, e, s, nil)
	}
	active, err := catalog(body, in.Wire, a.config.EnableVideo, a.config.NativeTools, a.config.EnabledTools)
	if err != nil {
		return nil, "", err
	}
	if len(active) == 0 && !a.config.DescribeImages {
		return a.execute(ctx, in, e, s, nil)
	}
	if _, ok := a.config.Codec.(StreamingCodec); !ok {
		return nil, "", fault(500, "hosted_streaming_codec_missing")
	}
	runCtx, cancel := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	live := &liveStream{reader: reader, writer: writer, ready: make(chan streamResult, 1), done: make(chan struct{}), cancel: cancel, id: in.RequestID, maxBytes: a.config.MaxBytes}
	stopOnCancel := context.AfterFunc(runCtx, func() { reader.CloseWithError(runCtx.Err()) })
	go func() {
		defer stopOnCancel()
		defer close(live.done)
		result, wire, err := a.execute(runCtx, in, e, s, live)
		if !live.started {
			// Preserve first-header recovery before any public output. Its body retains
			// the execution context until core finishes recovery/delivery.
			if result != nil && result.Body != nil {
				result.Body = &cancelBody{ReadCloser: result.Body, cancel: cancel}
			} else {
				cancel()
			}
			live.ready <- streamResult{result, wire, err}
			writer.Close()
			reader.Close()
			return
		}
		defer func() { stopOnCancel(); cancel() }()
		if err != nil {
			code := "hosted_stream_failed"
			var typed *inference.Error
			if errors.As(err, &typed) {
				code = typed.Code
			}
			if runCtx.Err() == nil {
				_ = live.write(object{"error": object{"type": "upstream_error", "code": code}})
			}
			writer.CloseWithError(err)
			return
		}
		if result != nil && result.Body != nil {
			_, err = io.Copy(writer, result.Body)
			result.Body.Close()
		}
		writer.CloseWithError(err)
	}()
	select {
	case result := <-live.ready:
		return result.response, result.wire, result.err
	case <-ctx.Done():
		cancel()
		reader.CloseWithError(ctx.Err())
		<-live.done
		return nil, "", ctx.Err()
	}
}
func (l *liveStream) start() {
	if l.started {
		return
	}
	l.started = true
	l.ready <- streamResult{response: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}, "Cache-Control": []string{"no-store"}}, Body: &liveBody{PipeReader: l.reader, cancel: l.cancel, done: l.done}}, wire: inference.Chat}
}

type liveBody struct {
	*io.PipeReader
	cancel context.CancelFunc
	done   <-chan struct{}
	once   sync.Once
}

func (b *liveBody) Close() error {
	b.once.Do(func() { b.cancel(); b.PipeReader.Close(); <-b.done })
	return nil
}
func (l *liveStream) write(value object) error {
	l.start()
	if l.id != "" {
		value["id"] = l.id
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = l.writer.Write(append(append([]byte("data: "), b...), []byte("\n\n")...))
	return err
}
func (l *liveStream) beginTurn() {
	l.text = ""
	l.reasoning = ""
	l.window = true
	l.pending = nil
	l.pendingBytes = 0
	l.sentFields = map[string]bool{}
}
func (l *liveStream) delta(delta object) error {
	out := object{}
	for k, v := range delta {
		if k == "tool_calls" || k == "function_call" || k == "role" {
			continue
		}
		if v != nil {
			out[k] = v
			l.sentFields[k] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	l.text += str(out["content"])
	l.reasoning += str(out["reasoning_content"])
	return l.write(object{"object": "chat.completion.chunk", "choices": []any{object{"index": 0, "delta": out, "finish_reason": nil}}})
}
func (l *liveStream) chunk(raw json.RawMessage) error {
	m, err := decode(raw)
	if err != nil {
		return err
	}
	if m["error"] != nil {
		return fault(502, "hosted_upstream_stream_error")
	}
	if l.id == "" {
		l.id = str(m["id"])
	}
	if outputs := arr(m["hosted_outputs"]); len(outputs) > 0 {
		if err = l.write(object{"object": "chat.completion.chunk", "choices": []any{}, "hosted_outputs": outputs}); err != nil {
			return err
		}
	}
	for _, v := range arr(m["choices"]) {
		delta := obj(obj(v)["delta"])
		// The codec owns late-signature ordering. Preserve its admissible reasoning
		// chunks, but hold the entire suffix from the first tool boundary until the
		// call validates; synthetic tool identities never enter the public stream.
		if len(arr(delta["tool_calls"])) > 0 || delta["function_call"] != nil {
			l.window = false
		}
		if !l.window {
			l.pendingBytes += int64(len(raw) + 64)
			if l.pendingBytes > l.maxBytes {
				return fault(502, "hosted_buffer_limit")
			}
			l.pending = append(l.pending, delta)
			continue
		}
		if err = l.delta(delta); err != nil {
			return err
		}
	}
	return nil
}
func (l *liveStream) remainder(result object) error {
	// This is a terminal ordinary/client-tool turn, so the withheld non-tool
	// suffix may now be delivered. Intermediate helper turns never call this.
	for _, delta := range l.pending {
		if err := l.delta(delta); err != nil {
			return err
		}
	}
	for _, v := range arr(result["choices"]) {
		message := obj(obj(v)["message"])
		for key, prefix := range map[string]string{"content": l.text, "reasoning_content": l.reasoning} {
			if prefix != "" {
				full := str(message[key])
				if full != "" && !strings.HasPrefix(full, prefix) {
					return fault(502, "hosted_stream_prefix_mismatch")
				}
			}
		}
		for key := range l.sentFields {
			delete(message, key)
		}
	}
	delete(result, "hosted_outputs")
	if l.id != "" {
		result["id"] = l.id
	}
	return nil
}
func (l *liveStream) progress(k Kind, id, status string, extra object) error {
	typ := "web_search_call"
	if k == Image {
		typ = "image_generation_call"
	}
	if k == Video {
		typ = "video_generation_call"
	}
	if k == Vision {
		return nil
	}
	item := object{"id": id, "type": typ, "status": status}
	for key, value := range extra {
		item[key] = value
	}
	return l.write(object{"object": "chat.completion.chunk", "choices": []any{}, "hosted_outputs": []any{item}})
}

// Keep raw-byte progress alive while a bounded service operation waits (for
// example video polling). Comments contain no content or operation identifiers.
func (l *liveStream) keepalive(ctx context.Context) func() {
	l.start()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := io.WriteString(l.writer, ": heartbeat\n\n"); err != nil {
					return
				}
			}
		}
	}()
	return func() { close(stop); <-done }
}
