// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/inference/hosted"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

func hostedDownloadFixture(t *testing.T, handler http.HandlerFunc) (*hostedOperation, string, *atomic.Int32, *[]store.Usage) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	sends := &atomic.Int32{}
	receipts := &[]store.Usage{}
	tr := &hostedTransport{policy: inference.DestinationPolicy{ApprovedOrigins: []string{server.URL}}, gate: func(ctx context.Context, start func(context.Context) error) error { return start(ctx) }, record: func(_ context.Context, _ string, u store.Usage) error { *receipts = append(*receipts, u); return nil }}
	return &hostedOperation{transport: tr}, server.URL, sends, receipts
}
func hostedDownloadBudget(ctx context.Context, sends *atomic.Int32) context.Context {
	return inference.WithPhysicalSendBudget(ctx, func(c context.Context, start func(context.Context) error) error {
		if sends.Add(1) > 1 {
			return inference.ErrUnavailable
		}
		return start(c)
	})
}

func TestHostedDownloadConnectDeadlineDoesNotSend(t *testing.T) {
	op, raw, sends, receipts := hostedDownloadFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("stalled connect reached server") })
	entered := make(chan struct{})
	op.transport.policy.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	_, err := op.downloadWithTimeouts(hostedDownloadBudget(ctx, sends), raw, 1024, 40*time.Millisecond, time.Second)
	if err == nil || ctx.Err() != nil {
		t.Fatalf("connect bound did not own timeout: %v (outer %v)", err, ctx.Err())
	}
	select {
	case <-entered:
	default:
		t.Fatal("connect was not attempted")
	}
	if sends.Load() != 0 || len(*receipts) != 0 {
		t.Fatal("stalled connect consumed a physical send or usage receipt")
	}
}

func TestHostedDownloadIdleHeadersAndBody(t *testing.T) {
	for _, phase := range []string{"headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			op, raw, sends, receipts := hostedDownloadFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("download leaked credentials")
				}
				if phase == "body" {
					w.Header().Set("Content-Type", "video/mp4")
					io.WriteString(w, "part")
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			})
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			_, err := op.downloadWithTimeouts(hostedDownloadBudget(ctx, sends), raw, 1024, time.Second, 40*time.Millisecond)
			if err == nil || ctx.Err() != nil {
				t.Fatalf("idle %s not bounded independently: %v outer=%v", phase, err, ctx.Err())
			}
			if sends.Load() != 1 || len(*receipts) != 1 || (*receipts)[0].Outcome != store.OutcomeUnknown {
				t.Fatalf("idle timeout retried or confirmed usage: sends=%d receipts=%+v", sends.Load(), *receipts)
			}
		})
	}
}

func TestHostedDownloadProgressCanExceedIdleBudget(t *testing.T) {
	const chunks = 16
	const interval = 50 * time.Millisecond
	const idle = 400 * time.Millisecond
	op, raw, sends, receipts := hostedDownloadFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for i := 0; i < chunks; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
			}
			if _, err := io.WriteString(w, "part"); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	started := time.Now()
	item, err := op.downloadWithTimeouts(hostedDownloadBudget(ctx, sends), raw, 1024, time.Second, idle)
	if err != nil {
		t.Fatalf("progressing download narrowed to total timeout: %v", err)
	}
	if time.Since(started) <= idle || len(item.Data) != chunks*4 || item.ContentType != "video/mp4" {
		t.Fatalf("progress fixture incomplete: bytes=%d elapsed=%s", len(item.Data), time.Since(started))
	}
	if sends.Load() != 1 || len(*receipts) != 1 || (*receipts)[0].Outcome != store.OutcomeSucceeded {
		t.Fatal("progress changed shared budget or receipts")
	}
}

func TestHostedDownloadPreservesOuterCancellationAndDeadline(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			began := make(chan struct{})
			op, raw, sends, receipts := hostedDownloadFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "video/mp4")
				io.WriteString(w, "part")
				w.(http.Flusher).Flush()
				close(began)
				<-r.Context().Done()
			})
			ctx, cancel := context.WithCancel(t.Context())
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
			}
			defer cancel()
			if mode == "cancel" {
				go func() {
					select {
					case <-began:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			_, err := op.downloadWithTimeouts(hostedDownloadBudget(ctx, sends), raw, 1024, time.Second, time.Second)
			if err == nil || ctx.Err() == nil {
				t.Fatalf("outer %s lost: %v", mode, err)
			}
			if mode == "deadline" && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Fatal(ctx.Err())
			}
			if sends.Load() != 1 || len(*receipts) != 1 {
				t.Fatal("cancellation caused retry or lost receipt")
			}
			if mode == "cancel" && (*receipts)[0].Outcome != store.OutcomeCanceled {
				t.Fatal("client cancellation not recorded")
			}
		})
	}
}

func TestHostedVideoDownloadRetainsJobDeadline(t *testing.T) {
	var base string
	started := make(chan struct{})
	op, raw, sends, receipts := hostedDownloadFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/videos/generations":
			io.WriteString(w, `{"request_id":"job"}`)
		case "/videos/job":
			io.WriteString(w, `{"status":"done","video":{"url":"`+base+`/media"}}`)
		case "/media":
			w.Header().Set("Content-Type", "video/mp4")
			io.WriteString(w, "part")
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
		default:
			t.Error("unexpected request")
		}
	})
	base = raw
	op.transport.base = raw
	timeout := uint32(100)
	op.settings = &v1.HostedSettings{Images: &v1.HostedImageSettings{VideoTimeoutMs: &timeout}}
	op.request = hosted.OperationRequest{Kind: hosted.Video, Arguments: json.RawMessage(`{"prompt":"fixture"}`)}
	op.wait = func(context.Context, time.Duration) error { return nil }
	parent, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	ctx := inference.WithPhysicalSendBudget(parent, func(c context.Context, start func(context.Context) error) error { sends.Add(1); return start(c) })
	_, err := op.Execute(ctx, op.request)
	if err == nil || parent.Err() != nil {
		t.Fatalf("video job deadline lost during fetch: %v outer=%v", err, parent.Err())
	}
	select {
	case <-started:
	default:
		t.Fatal("video download did not start")
	}
	if sends.Load() != 3 || len(*receipts) != 3 {
		t.Fatalf("expected submit/poll/download, got sends=%d receipts=%d", sends.Load(), len(*receipts))
	}
}
