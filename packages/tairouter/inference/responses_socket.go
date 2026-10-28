// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Transport string

const (
	HTTPTransport      Transport = "http"
	WebSocketTransport Transport = "websocket"
)

// responsesSocket makes exactly one create send. A close before the first event is
// ambiguous, not permission to replay through HTTP, another account, or a combo.
func (h *Handler) responsesSocket(ctx context.Context, p Principal, t Target, credential Credential, headers http.Header, payload object, rec *UsageRecord) (*http.Response, error) {
	if h.c.SocketDialer == nil || t.Wire != Responses {
		return nil, ErrUnsupported
	}
	client, _, e := h.c.Destinations.client(t.BaseURL)
	if e != nil {
		return nil, e
	}
	gateClient(client, ctx, p, h.c.Authorizer, func() { h.event(ctx, "upstream_socket_handshake") })
	var socket Socket
	var handshake *http.Response
	if dialer, ok := h.c.SocketDialer.(HandshakeSocketDialer); ok {
		socket, handshake, e = dialer.DialHandshake(ctx, strings.TrimRight(t.BaseURL, "/")+"/responses", headers, client)
	} else {
		socket, e = h.c.SocketDialer.Dial(ctx, strings.TrimRight(t.BaseURL, "/")+"/responses", headers, client)
		if info, ok := socket.(SocketHandshake); ok {
			status, headers := info.HandshakeResponse()
			handshake = &http.Response{StatusCode: status, Header: headers}
		}
	}
	if handshake != nil {
		if handshake.Body != nil {
			handshake.Body.Close()
		}
		if observed := h.observeResponse(ctx, p, t, credential, handshake); observed != nil {
			if socket != nil {
				socket.Close()
			}
			return nil, observed
		}
	}

	if e != nil {
		return nil, e
	}
	frame := clone(payload)
	frame["type"] = "response.create"
	delete(frame, "stream")
	raw, e := json.Marshal(frame)
	if e != nil {
		socket.Close()
		return nil, e
	}
	started := false
	sendCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	e = (modelDispatchAuthorizer{Authorizer: h.c.Authorizer, record: rec}).Dispatch(sendCtx, p, func(c context.Context) error {
		if e := c.Err(); e != nil {
			return e
		}
		markModelDispatch(c)
		started = true
		rec.Sends++
		h.event(ctx, "upstream_send")
		return socket.Write(c, raw)
	})
	stop()
	if e != nil {
		socket.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if started {
			rec.UnmeasuredAttempts++
			return nil, ErrAmbiguous
		}
		return nil, e
	}
	reader, writer := io.Pipe()
	streamCtx, cancel := context.WithCancel(ctx)
	body := &socketBody{PipeReader: reader, socket: socket, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(body.done)
		defer socket.Close()
		defer cancel()
		seen := false
		for {
			b, e := socket.Read(streamCtx)
			if e != nil {
				if !seen && !errors.Is(e, context.Canceled) {
					e = ErrAmbiguous
				}
				writer.CloseWithError(e)
				return
			}
			if len(b) > 2<<20 {
				writer.CloseWithError(&Error{502, "stream_event_too_large", 0})
				return
			}
			event, e := decode(b)
			if e != nil {
				writer.CloseWithError(&Error{502, "invalid_upstream_event", 0})
				return
			}
			seen = true
			data := append([]byte("data: "), b...)
			data = append(data, '\n', '\n')
			if _, e = writer.Write(data); e != nil {
				writer.CloseWithError(e)
				return
			}
			kind := str(event["type"])
			if kind == "response.completed" || kind == "response.incomplete" || kind == "response.failed" || kind == "error" {
				writer.Close()
				return
			}
		}
	}()
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: body}, nil
}

type socketBody struct {
	*io.PipeReader
	socket Socket
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func (b *socketBody) Close() error {
	b.once.Do(func() { b.cancel(); b.PipeReader.Close(); b.socket.Close(); <-b.done })
	return nil
}

// HandshakeSocketDialer returns sanitized handshake metadata even for refused upgrades.
// Response bodies must be closed; only status and headers are observed by the core.
type HandshakeSocketDialer interface {
	DialHandshake(context.Context, string, http.Header, *http.Client) (Socket, *http.Response, error)
}
type SocketHandshake interface{ HandshakeResponse() (int, http.Header) }
