// SPDX-License-Identifier: Apache-2.0
package devin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type terminalCloseBody struct {
	reader *bytes.Reader
	closed chan struct{}
	once   sync.Once
	err    error
	closes atomic.Int32
}

func (b *terminalCloseBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if err == io.EOF {
		<-b.closed
	}
	return n, err
}
func (b *terminalCloseBody) Close() error {
	b.once.Do(func() { b.closes.Add(1); close(b.closed) })
	return b.err
}
func TestTerminalDecoderPropagatesSourceCloseError(t *testing.T) {
	receiptErr := errors.New("fixture receipt write failed")
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "receipt-failure"}[fail], func(t *testing.T) {
			wire := append(frame(textField(nil, 3, "description")), eos("{}")...)
			source := &terminalCloseBody{reader: bytes.NewReader(wire), closed: make(chan struct{})}
			if fail {
				source.err = receiptErr
			}
			ctx := New().PrepareContext(context.Background())
			body := newStream(ctx, source)
			output, err := io.ReadAll(body)
			closeErr := body.Close()
			if fail {
				if !errors.Is(err, receiptErr) || !errors.Is(closeErr, receiptErr) || !errors.Is(body.Close(), receiptErr) {
					t.Fatalf("receipt close failure lost: read=%v close=%v", err, closeErr)
				}
			} else if err != nil || closeErr != nil || !strings.Contains(string(output), "[DONE]") {
				t.Fatalf("successful terminal rejected: %s, %v, %v", output, err, closeErr)
			}
			state := attempt(ctx)
			state.mu.Lock()
			complete := state.complete
			state.mu.Unlock()
			if complete == fail {
				t.Fatalf("completion proof ignored source finalization: complete=%t failure=%t", complete, fail)
			}
			if source.closes.Load() != 1 {
				t.Fatalf("source closes=%d", source.closes.Load())
			}
		})
	}
}

func TestCloseStopsBlockedTerminalDecoder(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "early-close", true: "cancel"}[cancelFirst], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			source, writer := io.Pipe()
			defer writer.Close()
			body := newStream(ctx, source)
			if cancelFirst {
				cancel()
			}
			done := make(chan error, 1)
			go func() { done <- body.Close() }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("close left decoder blocked")
			}
			if err := body.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
