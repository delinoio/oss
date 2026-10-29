// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureSocket struct {
	in     chan []byte
	out    chan []byte
	closed chan struct{}
	once   sync.Once
}

func newFixtureSocket() *fixtureSocket {
	return &fixtureSocket{in: make(chan []byte, 4), out: make(chan []byte, 32), closed: make(chan struct{})}
}
func (s *fixtureSocket) Read(ctx context.Context) ([]byte, error) {
	select {
	case b := <-s.in:
		return b, nil
	case <-s.closed:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (s *fixtureSocket) Write(ctx context.Context, b []byte) error {
	select {
	case s.out <- append([]byte{}, b...):
		return nil
	case <-s.closed:
		return io.EOF
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *fixtureSocket) Close() error { s.once.Do(func() { close(s.closed) }); return nil }

func TestResponseSocketCompletedWorkerAcceptance(t *testing.T) {
	for _, completionFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(completionFirst), func(t *testing.T) {
			finished := make(chan struct{})
			var done <-chan struct{} = finished
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			turnCancel := context.CancelFunc(cancel)
			if completionFirst {
				close(finished)
				select {
				case <-done:
					done, turnCancel = nil, nil
				default:
					t.Fatal("completed worker was not ready")
				}
			} else {
				// Model the frame branch winning the outer select after completion.
				close(finished)
			}
			if responseWorkerActive(&done, &turnCancel) || done != nil || turnCancel != nil {
				t.Fatal("completed worker still blocks the next create")
			}
			if ctx.Err() != nil {
				t.Fatal("worker cleanup canceled the socket")
			}
		})
	}
	finished := make(chan struct{})
	var done <-chan struct{} = finished
	called := false
	turnCancel := context.CancelFunc(func() { called = true })
	if !responseWorkerActive(&done, &turnCancel) || done == nil || turnCancel == nil || called {
		t.Fatal("active worker was cleared or canceled")
	}
}

type blockingSocketRelease struct {
	*testAuth
	admitted       atomic.Int32
	releaseEntered chan struct{}
	allowRelease   chan struct{}
}

func (a *blockingSocketRelease) Admit(ctx context.Context, token string) (Principal, func(), error) {
	p, release, err := a.testAuth.Admit(ctx, token)
	if err != nil {
		return p, release, err
	}
	if a.admitted.Add(1) == 1 {
		return p, func() {
			close(a.releaseEntered)
			<-a.allowRelease
			release()
		}, nil
	}
	return p, release, nil
}

func TestResponseSocketSequentialRealTurns(t *testing.T) {
	var sends atomic.Int32
	h, _, baseAuth, _, _ := harness(t, func(w http.ResponseWriter, _ *http.Request) {
		sends.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, streamFixture(Responses))
	}, Responses)
	auth := &blockingSocketRelease{testAuth: baseAuth, releaseEntered: make(chan struct{}), allowRelease: make(chan struct{})}
	h.c.Authorizer = auth
	s := newFixtureSocket()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- h.ServeSocket(ctx, "client-secret", s) }()
	create := []byte(`{"type":"response.create","model":"m","input":"turn"}`)
	readType := func() string {
		t.Helper()
		select {
		case b := <-s.out:
			var event object
			if err := json.Unmarshal(b, &event); err != nil {
				t.Fatal(err)
			}
			if str(event["type"]) == "error" {
				return str(event["error"].(map[string]any)["code"])
			}
			return str(event["type"])
		case <-time.After(2 * time.Second):
			t.Fatal("socket event timeout")
			return ""
		}
	}
	s.in <- create
	for event := readType(); event != "response.completed"; event = readType() {
		if event == "response_in_progress" {
			t.Fatal("first turn was rejected")
		}
	}
	select {
	case <-auth.releaseEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("first release was not reached")
	}
	s.in <- create
	if event := readType(); event != "response_in_progress" {
		t.Fatalf("active worker: got %s", event)
	}
	if auth.admitted.Load() != 1 || sends.Load() != 1 {
		t.Fatalf("overlap admitted %d times and sent %d times", auth.admitted.Load(), sends.Load())
	}
	close(auth.allowRelease)
	// The release callback completes before the worker closes its finished signal.
	// Allow that final defer to run before sending the next real turn.
	time.Sleep(20 * time.Millisecond)
	s.in <- create
	for event := readType(); event != "response.completed"; event = readType() {
		if event == "response_in_progress" {
			t.Fatal("completed worker rejected the next real turn")
		}
	}
	s.Close()
	<-serveDone
	if auth.admitted.Load() != 2 || auth.released.Load() != 2 || sends.Load() != 2 {
		t.Fatalf("admitted %d, released %d, sent %d", auth.admitted.Load(), auth.released.Load(), sends.Load())
	}
}

func TestResponseSocketTurnAndWarmup(t *testing.T) {
	sends := 0
	h, _, a, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		sends++
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, streamFixture(Responses))
	}, Responses)
	s := newFixtureSocket()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.ServeSocket(ctx, "client-secret", s) }()
	s.in <- []byte(`{"type":"response.create","model":"m","generate":false}`)
	select {
	case b := <-s.out:
		var m object
		json.Unmarshal(b, &m)
		if str(m["type"]) != "response.created" {
			t.Fatal(string(b))
		}
	case <-time.After(time.Second):
		t.Fatal("warmup timeout")
	}
	<-s.out // completed warmup frame
	s.in <- []byte(`{"type":"response.create","model":"m","input":"hi"}`)
	for {
		select {
		case b := <-s.out:
			var m object
			json.Unmarshal(b, &m)
			if str(m["type"]) == "response.completed" {
				s.Close()
				<-done
				if sends != 1 || a.released.Load() != 2 {
					t.Fatalf("sends %d released %d", sends, a.released.Load())
				}
				return
			}
		case <-time.After(time.Second):
			t.Fatal("turn timeout")
		}
	}
}
func TestResponseSocketCancelAbortsUpstream(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
		close(canceled)
	}, Responses)
	s := newFixtureSocket()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.ServeSocket(ctx, "client-secret", s) }()
	s.in <- []byte(`{"type":"response.create","model":"m","input":"hi"}`)
	<-entered
	s.in <- []byte(`{"type":"response.cancel"}`)
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("socket cancellation did not stop send")
	}
	s.Close()
	<-done
}

type fixtureDialer struct{ s Socket }

func (d fixtureDialer) Dial(context.Context, string, http.Header, *http.Client) (Socket, error) {
	return d.s, nil
}
func TestNativeResponsesSocketSendAndAmbiguity(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprint(ambiguous), func(t *testing.T) {
			h, _, _, _, u := harness(t, func(http.ResponseWriter, *http.Request) { t.Error("websocket fell back to HTTP") }, Responses)
			up := newFixtureSocket()
			h.c.SocketDialer = fixtureDialer{up}
			rt := h.c.Router.(testRouter)
			rt.route.Targets[0].Transport = WebSocketTransport
			rt.route.Targets[0].Retry.FallbackOn5xx = true
			rt.route.Targets = append(rt.route.Targets, rt.route.Targets[0])
			h.c.Router = rt
			sent := make(chan struct{})
			go func() {
				defer close(sent)
				b := <-up.out
				var m object
				json.Unmarshal(b, &m)
				if str(m["type"]) != "response.create" {
					t.Error(string(b))
				}
				if ambiguous {
					up.Close()
					return
				}
				up.in <- []byte(`{"type":"response.completed","response":` + fixture(Responses) + `}`)
			}()
			w := call(h, "/v1/responses", `{"model":"m","input":"hi"}`)
			<-sent
			if ambiguous {
				if w.Code != 429 || w.Header().Get("x-should-retry") != "false" {
					t.Fatalf("%d %s", w.Code, w.Body.String())
				}
			} else if w.Code != 200 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if u.records[0].Sends != 1 {
				t.Fatal("wrong physical budget")
			}
		})
	}
}
