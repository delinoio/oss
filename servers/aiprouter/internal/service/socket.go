// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"errors"
	"github.com/coder/websocket"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"net/http"
)

type outboundSocketDialer struct{}

const liveSocketReadLimit = 50 << 20

var _ inference.SubprotocolSocketDialer = outboundSocketDialer{}
var _ inference.FrameSocket = webSocket{}
var _ inference.HandshakeSocketDialer = outboundSocketDialer{}
var _ inference.SocketHandshake = webSocket{}

func (d outboundSocketDialer) Dial(ctx context.Context, endpoint string, headers http.Header, client *http.Client) (inference.Socket, error) {
	socket, _, err := d.DialHandshake(ctx, endpoint, headers, client)
	return socket, err
}

func (outboundSocketDialer) DialHandshake(ctx context.Context, endpoint string, headers http.Header, client *http.Client) (inference.Socket, *http.Response, error) {
	return dialSocket(ctx, endpoint, headers, nil, client)
}

func (outboundSocketDialer) DialProtocols(ctx context.Context, endpoint string, headers http.Header, protocols []string, client *http.Client) (inference.Socket, error) {
	socket, _, err := dialSocket(ctx, endpoint, headers, protocols, client)
	return socket, err
}

func dialSocket(ctx context.Context, endpoint string, headers http.Header, protocols []string, client *http.Client) (inference.Socket, *http.Response, error) {
	if client == nil {
		return nil, nil, inference.ErrDestination
	}
	// The supplied client owns DNS, TLS, redirects and the physical dispatch gate.
	// Credential-bearing subprotocols must never be logged or copied to diagnostics.
	c, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: client, HTTPHeader: headers.Clone(), Subprotocols: append([]string(nil), protocols...), CompressionMode: websocket.CompressionDisabled})
	var handshake *http.Response
	if response != nil {
		handshake = &http.Response{StatusCode: response.StatusCode, Header: response.Header.Clone(), Body: http.NoBody}
	}
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, handshake, err
	}
	c.SetReadLimit(liveSocketReadLimit)
	socket := webSocket{c: c}
	if handshake != nil {
		socket.handshakeStatus = handshake.StatusCode
		socket.handshakeHeaders = handshake.Header.Clone()
	}
	return socket, handshake, nil
}

type webSocket struct {
	c                *websocket.Conn
	handshakeStatus  int
	handshakeHeaders http.Header
}

func (w webSocket) HandshakeResponse() (int, http.Header) {
	return w.handshakeStatus, w.handshakeHeaders.Clone()
}

func (w webSocket) ReadFrame(ctx context.Context) ([]byte, bool, error) {
	kind, b, err := w.c.Read(ctx)
	return b, kind == websocket.MessageText, err
}

func (w webSocket) Read(ctx context.Context) ([]byte, error) {
	kind, b, e := w.c.Read(ctx)
	if e == nil && kind != websocket.MessageText {
		return nil, errors.New("text frames required")
	}
	return b, e
}
func (w webSocket) Write(ctx context.Context, b []byte) error {
	return w.c.Write(ctx, websocket.MessageText, b)
}
func (w webSocket) Close() error { return w.c.Close(websocket.StatusNormalClosure, "") }
