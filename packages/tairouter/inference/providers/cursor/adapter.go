// SPDX-License-Identifier: Apache-2.0
package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

// Decode projects Connect frames to Chat SSE. Buffering for non-streaming
// clients and translation to Responses/Messages belong to the parent handler.
func (a *Adapter) Decode(ctx context.Context, r *http.Response) (*http.Response, error) {
	if r == nil || r.Body == nil {
		return nil, protocolError("cursor_missing_response")
	}
	if err := ctx.Err(); err != nil {
		r.Body.Close()
		return nil, err
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		defer r.Body.Close()
		stop := context.AfterFunc(ctx, func() { r.Body.Close() })
		defer stop()
		b, err := io.ReadAll(io.LimitReader(r.Body, 65537))
		if err != nil {
			return nil, protocolError("cursor_error_body_read")
		}
		if len(b) > 65536 {
			return nil, protocolError("cursor_error_body_limit")
		}
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Code == "" {
			switch r.StatusCode {
			case 401, 403:
				e.Code = "unauthenticated"
			case 429:
				e.Code = "resource_exhausted"
				e.Message = "rate limit"
			case 400:
				e.Code = "invalid_argument"
			case 503:
				e.Code = "unavailable"
			}
		}
		return nil, ClassifyError(e.Code, e.Message, nil)
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/connect+proto" {
		r.Body.Close()
		return nil, protocolError("cursor_invalid_content_type")
	}
	encoding := strings.ToLower(r.Header.Get("Connect-Content-Encoding"))
	if encoding != "" && encoding != "identity" && encoding != "gzip" {
		r.Body.Close()
		return nil, protocolError("cursor_unsupported_compression")
	}
	s, err := sessionFromResponse(r)
	if err != nil {
		r.Body.Close()
		return nil, err
	}
	reader, writer := io.Pipe()
	body := &decodedBody{PipeReader: reader, upstream: r.Body}
	body.stop = context.AfterFunc(ctx, func() { body.closeWithError(ctx.Err()) })
	copyResponse := *r
	copyResponse.Header = make(http.Header)
	copyResponse.Header.Set("Content-Type", "text/event-stream")
	copyResponse.Header.Set("Cache-Control", "no-store")
	copyResponse.Header.Set("X-Should-Retry", "false")
	copyResponse.ContentLength = -1
	copyResponse.Body = body
	copyResponse.Trailer = nil
	copyResponse.TransferEncoding = nil
	copyResponse.Uncompressed = true
	go func() {
		defer r.Body.Close()
		defer body.stop()
		err := decodeStream(ctx, r.Body, writer, s)
		if err != nil {
			// Preserve a typed body error as well as an SSE failure. Never emit
			// [DONE] after a failed/truncated stream.
			var typed *inference.Error
			code := "cursor_upstream_error"
			if errors.As(err, &typed) {
				code = typed.Code
			}
			_ = writeSSE(writer, map[string]any{"error": map[string]any{"type": "upstream_error", "code": code}})
		}
		writer.CloseWithError(err)
	}()
	return &copyResponse, nil
}

type decodedBody struct {
	*io.PipeReader
	upstream io.Closer
	stop     func() bool
	once     sync.Once
}

func (b *decodedBody) closeWithError(err error) {
	b.once.Do(func() { b.PipeReader.CloseWithError(err); b.upstream.Close() })
}
func (b *decodedBody) Close() error {
	if b.stop != nil {
		b.stop()
	}
	b.closeWithError(io.ErrClosedPipe)
	return nil
}

func sessionFromResponse(r *http.Response) (*Session, error) {
	// GetBody belongs to the authorized outbound request. Never recover schema
	// authority from response headers or process-global request/account maps.
	if r.Request == nil || r.Request.GetBody == nil {
		return nil, &inference.Error{Status: 501, Code: "cursor_request_context_required"}
	}
	reader, err := r.Request.GetBody()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	f, err := ReadFrame(io.LimitReader(reader, MaxFrameBytes+5))
	if err != nil {
		return nil, err
	}
	m, err := parse(f.Payload)
	if err != nil {
		return nil, err
	}
	run, err := m.sub(1)
	if err != nil {
		return nil, err
	}
	details, err := run.sub(3)
	if err != nil {
		return nil, err
	}
	model := details.str(1)
	if model == "" {
		d, e := run.sub(9)
		if e != nil {
			return nil, e
		}
		model = d.str(1)
	}
	cat, err := run.sub(4)
	if err != nil {
		return nil, err
	}
	var tools []tool
	for _, f := range cat {
		if f.number != 1 {
			continue
		}
		d, e := parse(f.data)
		if e != nil {
			return nil, e
		}
		wireName := d.str(1)
		name := strings.TrimPrefix(wireName, "ocx_client_")
		// The serialized schema alone cannot prove a custom tool's provenance.
		// Strict JSON-object validation remains active; custom wrapper enforcement
		// is stronger in Prepare's session used by a duplex executor.
		tools = append(tools, tool{Name: name, WireName: wireName, Definition: f.data})
		if len(tools) > MaxTools {
			return nil, protocolError("cursor_tool_catalog_too_large")
		}
	}
	s := newSession(model, tools)
	s.conversationID = run.str(5)
	return s, nil
}

func chatEmitter(w io.Writer, s *Session, id string) func([]Event) error {
	index := 0
	emit := func(events []Event) error {
		for _, e := range events {
			chunk := map[string]any{"id": id, "object": "chat.completion.chunk", "model": s.model}
			delta := map[string]any{}
			var finish any
			switch e.Kind {
			case Text:
				delta["content"] = e.Text
			case Thinking:
				delta["reasoning_content"] = e.Text
			case ToolCall:
				delta["tool_calls"] = []any{map[string]any{"index": index, "id": e.ID, "type": "function", "function": map[string]any{"name": e.Name, "arguments": e.Arguments}}}
				index++
			case Done:
				finish = "stop"
				if index > 0 {
					finish = "tool_calls"
				}
				usage := map[string]any{}
				if e.Usage.OutputTokens != nil {
					usage["completion_tokens"] = *e.Usage.OutputTokens
				}
				if len(usage) > 0 {
					chunk["usage"] = usage
				}
				if e.ContextTokens != nil {
					chunk["cursor_context_tokens"] = *e.ContextTokens
				}
			case UsageUpdate:
				chunk["usage"] = map[string]any{"completion_tokens": *e.Usage.OutputTokens}
			}
			chunk["choices"] = []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}
			if e.Kind == UsageUpdate {
				chunk["choices"] = []any{}
			}
			if err := writeSSE(w, chunk); err != nil {
				return err
			}
			if e.Kind == Done {
				_, err := io.WriteString(w, "data: [DONE]\n\n")
				return err
			}
		}
		return nil
	}
	return emit
}

func decodeStream(ctx context.Context, r io.Reader, w io.Writer, s *Session) error {
	emit := chatEmitter(w, s, "chatcmpl-"+s.conversationID)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := ReadFrame(r)
		if err == io.EOF {
			events, e := s.EOF()
			if e != nil {
				return e
			}
			return emit(events)
		}
		if err != nil {
			return protocolError("cursor_stream_truncated")
		}
		if f.Flags&2 == 0 {
			message, parseErr := parse(f.Payload)
			if parseErr != nil {
				return parseErr
			}
			if message.get(4).number != 0 {
				return &inference.Error{Status: 501, Code: "cursor_duplex_executor_required"}
			}
		}
		events, replies, err := s.Handle(ctx, f)
		if err != nil {
			return err
		}
		if len(replies) > 0 {
			return &inference.Error{Status: 501, Code: "cursor_duplex_executor_required"}
		}
		if err := emit(events); err != nil {
			return err
		}
		if s.terminal {
			return nil
		}
	}
}

func writeSSE(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, bytes.NewReader(join([]byte("data: "), b, []byte("\n\n"))))
	return err
}
