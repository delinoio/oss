// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/delinoio/oss/packages/aiprouter/inference"
)

func TestSocketDictationProtocolsAndFrames(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	protocols := []string{"chatgpt-dictation", "openai-bearer.fixture-token", "codex-desktop"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",")
		for i := range got {
			got[i] = strings.TrimSpace(got[i])
		}
		if !reflect.DeepEqual(got, protocols) {
			t.Error("dictation protocols changed")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected bearer header")
		}
		w.Header().Set("X-Test-Quota", "fixture")
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"chatgpt-dictation"}})
		if err != nil {
			return
		}
		defer c.CloseNow()
		if err = c.Write(ctx, websocket.MessageBinary, []byte("binary")); err != nil {
			return
		}
		if err = c.Write(ctx, websocket.MessageText, []byte("text")); err != nil {
			return
		}
		// Keep the peer alive until the client closes, without racing frame delivery.
		_, _, _ = c.Read(ctx)
	}))
	defer srv.Close()
	socket, err := (outboundSocketDialer{}).DialProtocols(ctx, srv.URL, nil, protocols, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	framed := socket.(inference.FrameSocket)
	data, text, err := framed.ReadFrame(ctx)
	if err != nil || text || string(data) != "binary" {
		t.Fatalf("binary classification: %v %v", text, err)
	}
	data, text, err = framed.ReadFrame(ctx)
	if err != nil || !text || string(data) != "text" {
		t.Fatalf("text classification: %v %v", text, err)
	}
	meta := socket.(interface{ HandshakeResponse() (int, http.Header) })
	status, headers := meta.HandshakeResponse()
	if status != 101 || headers.Get("X-Test-Quota") != "fixture" {
		t.Fatal("successful handshake metadata lost")
	}
	headers.Set("X-Test-Quota", "mutated")
	_, again := meta.HandshakeResponse()
	if again.Get("X-Test-Quota") != "fixture" {
		t.Fatal("handshake headers alias caller")
	}
}

func TestSocketRefusedHandshakeMetadata(t *testing.T) {
	for _, status := range []int{401, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "17")
				w.WriteHeader(status)
				io.WriteString(w, "private refusal body")
			}))
			defer srv.Close()
			socket, response, err := (outboundSocketDialer{}).DialHandshake(context.Background(), srv.URL, nil, srv.Client())
			if err == nil || socket != nil || response == nil || response.StatusCode != status || response.Header.Get("Retry-After") != "17" {
				t.Fatal("refusal handshake metadata lost")
			}
			body, _ := io.ReadAll(response.Body)
			if len(body) != 0 {
				t.Fatal("private handshake body retained")
			}
		})
	}
}

func TestSocketReadLimitMatchesCore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	payload := bytes.Repeat([]byte("x"), 17<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		if c.Write(ctx, websocket.MessageText, payload) != nil {
			return
		}
		_, _, _ = c.Read(ctx)
	}))
	defer srv.Close()
	socket, err := (outboundSocketDialer{}).Dial(ctx, srv.URL, nil, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	data, err := socket.Read(ctx)
	if err != nil || len(data) != len(payload) {
		t.Fatalf("valid frame above old16MiB cap: bytes=%d err=%v", len(data), err)
	}
}
