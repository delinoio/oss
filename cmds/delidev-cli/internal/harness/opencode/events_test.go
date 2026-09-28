package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func eventFrame(id int, kind EventKind, properties any) string {
	raw, _ := json.Marshal(map[string]any{"id": fmt.Sprintf("evt_%012xABCDEFGHIJKLMN", id), "type": kind, "properties": properties})
	return "event: message\ndata: " + string(raw) + "\n\n"
}

func streamFixture(t *testing.T, wire string) *eventStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stream := &eventStream{ctx: ctx, body: io.NopCloser(strings.NewReader(wire)), cancel: cancel, queue: make(chan NativeEvent, maxQueuedEvents), done: make(chan struct{}), seen: map[string]bool{}, last: time.Now(), cwd: fixtureWorkspacePath(), alive: func() error { return nil }}
	go stream.run(ctx)
	t.Cleanup(stream.Close)
	return stream
}

func TestEventStreamRetainsPrivateArrivalOrder(t *testing.T) {
	wire := eventFrame(3, ServerConnectedEvent, map[string]any{}) +
		eventFrame(2, MessagePartDeltaEvent, map[string]any{"delta": "private-native-event-sentinel"}) +
		eventFrame(1, ServerHeartbeatEvent, map[string]any{})
	stream := streamFixture(t, wire)
	for _, id := range []int{3, 2, 1} {
		event, err := stream.Next(context.Background())
		if err != nil || event.ID != fmt.Sprintf("evt_%012xABCDEFGHIJKLMN", id) {
			t.Fatalf("arrival order changed: %+v, %v", event, err)
		}
		raw, _ := json.Marshal(event)
		if strings.Contains(string(raw), "private-native-event-sentinel") {
			t.Fatal("native private event entered ordinary JSON")
		}
	}
	if _, err := stream.Next(context.Background()); err == nil || domain.SafeError(err).Code != domain.Unavailable {
		t.Fatal("EOF became native completion")
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.pending != 0 || len(stream.seen) != 3 {
		t.Fatal("draining lost identity or byte accounting")
	}
}

func TestEventStreamRejectsInvalidOrReplayFrames(t *testing.T) {
	connected := eventFrame(1, ServerConnectedEvent, map[string]any{})
	for name, wire := range map[string]string{
		"missing-connected":     eventFrame(1, ServerHeartbeatEvent, map[string]any{}),
		"duplicate-connected":   connected + eventFrame(2, ServerConnectedEvent, map[string]any{}),
		"duplicate-id":          connected + eventFrame(1, ServerHeartbeatEvent, map[string]any{}),
		"unknown-kind":          connected + eventFrame(2, "foreign.kind", map[string]any{}),
		"heartbeat-fields":      connected + eventFrame(2, ServerHeartbeatEvent, map[string]any{"foreign": true}),
		"non-object-properties": eventFrame(1, ServerConnectedEvent, []any{}),
		"null-properties":       eventFrame(1, ServerConnectedEvent, nil),
		"foreign-disposal":      connected + eventFrame(2, ServerInstanceDisposedEvent, map[string]any{"directory": "/foreign"}),
		"sse-replay-id":         "id: foreign\n" + connected,
		"sse-retry":             "retry: 1000\n" + connected,
		"comment":               ": private-native-diagnostic\n" + connected,
		"unknown-field":         "foreign: private-native-diagnostic\n" + connected,
		"duplicate-event":       "event: message\n" + connected,
		"duplicate-data":        strings.Replace(connected, "\n\n", "\ndata: {}\n\n", 1),
		"unknown-event":         strings.Replace(connected, "event: message", "event: foreign", 1),
		"unterminated":          strings.TrimSuffix(connected, "\n"),
		"empty-frame":           "\n",
		"alias":                 strings.Replace(connected, `"id":`, `"ID":`, 1),
		"duplicate-key":         strings.Replace(connected, `"type":`, `"type":"server.connected","type":`, 1),
		"wrong-id":              strings.Replace(connected, "evt_", "msg_", 1),
	} {
		t.Run(name, func(t *testing.T) {
			stream := streamFixture(t, wire)
			for {
				_, err := stream.Next(context.Background())
				if err == nil {
					continue
				}
				if domain.SafeError(err).Code != domain.Unsupported || strings.Contains(err.Error(), "private-native-diagnostic") {
					t.Fatalf("invalid stream classification: %v", err)
				}
				break
			}
		})
	}
}

func TestEventStreamBoundsKeepPriorObservations(t *testing.T) {
	for _, kind := range []string{"queue", "line", "bytes", "identities"} {
		t.Run(kind, func(t *testing.T) {
			wire := eventFrame(1, ServerConnectedEvent, map[string]any{})
			switch kind {
			case "queue":
				for i := 2; i <= maxQueuedEvents+1; i++ {
					wire += eventFrame(i, ServerHeartbeatEvent, map[string]any{})
				}
			case "line":
				wire += "data: " + strings.Repeat("x", maxHTTPBody+256)
			case "bytes":
				for i := 2; i < 12; i++ {
					wire += eventFrame(i, MessagePartDeltaEvent, map[string]any{"delta": strings.Repeat("x", maxHTTPBody-256)})
				}
			}
			stream := streamFixture(t, wire)
			if kind == "identities" {
				// Exercise the exact retaining boundary without allocating a long
				// stream or evicting the already verified original connected ID.
				<-stream.done
				stream.mu.Lock()
				for i := 2; i <= maxEventIdentities; i++ {
					stream.seen[fmt.Sprintf("evt_%012xABCDEFGHIJKLMN", i)] = true
				}
				stream.mu.Unlock()
				raw := strings.TrimSuffix(strings.SplitN(eventFrame(maxEventIdentities+1, ServerHeartbeatEvent, map[string]any{}), "data: ", 2)[1], "\n\n")
				if problem := stream.accept([]byte(raw)); problem == nil || problem.Code != domain.ResourceExhausted {
					t.Fatal("identity capacity silently evicted original evidence")
				}
				return
			}
			<-stream.done
			if problem := stream.status(); problem == nil || problem.Code != domain.ResourceExhausted {
				t.Fatalf("bound: %v", problem)
			}
			if event, err := stream.Next(context.Background()); err != nil || event.Kind != ServerConnectedEvent {
				t.Fatal("later failure discarded a prior valid observation")
			}
		})
	}
}

func TestEventConnectionIsOwnedAndCannotReconnect(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed=%t", failed), func(t *testing.T) {
			f := newSessionFixture(t)
			f.create(t)
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/event" || r.URL.RawQuery != "" || r.Header.Get("Last-Event-ID") != "" || r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("x-opencode-directory") != f.api.cwd {
					t.Error("event request changed its native contract")
				}
				username, password, ok := r.BasicAuth()
				if !ok || username != "delidev" || password != f.api.password {
					t.Error("event authentication changed")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if failed {
					w.WriteHeader(503)
					return
				}
				_, _ = io.WriteString(w, eventFrame(1, ServerConnectedEvent, map[string]any{}))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(closed)
			}))
			defer server.Close()
			client, transport := probeHTTPClient(strings.TrimPrefix(server.URL, "http://"))
			defer transport.CloseIdleConnections()
			original := f.api.client.Transport
			// The synthetic transport switches only this one endpoint, while all
			// native session metadata reads still use the original fixture owner.
			f.api.client.Transport = eventRouteFixture{original: original, stream: client.Transport, origin: server.URL}
			stream, err := f.api.openEvents(context.Background())
			if failed != (err != nil) {
				t.Fatalf("open: %v", err)
			}
			if !failed {
				stream.Close()
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("native HTTP stream was not closed")
				}
				if _, err := f.api.submit(context.Background(), domain.NewID(), fixtureMessageID, fixturePartID, "blocked after stream loss"); err == nil || f.postCount() != 1 {
					t.Fatal("stream loss permitted native input")
				}
			}
			if _, err := f.api.openEvents(context.Background()); err == nil {
				t.Fatal("event connection retried without reconciliation")
			}
		})
	}
}

type eventRouteFixture struct {
	original http.RoundTripper
	stream   http.RoundTripper
	origin   string
}

func (r eventRouteFixture) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path != "/event" {
		return r.original.RoundTrip(request)
	}
	copy := request.Clone(request.Context())
	copy.URL.Host = strings.TrimPrefix(r.origin, "http://")
	copy.Host = copy.URL.Host
	return r.stream.RoundTrip(copy)
}

func TestEventStreamCancellationJoinsBlockedReader(t *testing.T) {
	for _, stale := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		reader, writer := io.Pipe()
		stream := &eventStream{ctx: ctx, body: reader, cancel: cancel, queue: make(chan NativeEvent, maxQueuedEvents), done: make(chan struct{}), seen: map[string]bool{}, last: time.Now(), alive: func() error { return nil }}
		if stale {
			stream.last = time.Now().Add(-2 * eventIdleLimit)
		}
		go stream.run(ctx)
		if !stale {
			cancel()
		}
		select {
		case <-stream.done:
		case <-time.After(3 * time.Second):
			_ = writer.Close()
			t.Fatal("canceled or stale stream retained a blocked reader")
		}
		_ = writer.Close()
		stream.Close()
		if stream.status() == nil {
			t.Fatal("stream ended without retaining uncertainty")
		}
	}
}

func TestEventLossAfterInputClaimPreventsMutation(t *testing.T) {
	f := newSessionFixture(t)
	f.create(t)
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer writer.Close()
	stream := &eventStream{ctx: ctx, body: reader, cancel: cancel, queue: make(chan NativeEvent, maxQueuedEvents), done: make(chan struct{}), seen: map[string]bool{}, last: time.Now(), alive: func() error { return nil }}
	go stream.run(ctx)
	defer stream.Close()
	f.api.events = stream
	original := f.api.claim
	f.api.claim = func(ctx context.Context, claim SessionClaim) error {
		err := original(ctx, claim)
		stream.Close()
		return err
	}
	receipt, err := f.api.submit(context.Background(), domain.NewID(), fixtureMessageID, fixturePartID, "Must remain unsent")
	if err == nil || receipt.HTTPAccepted || f.postCount() != 1 || f.api.input == nil {
		t.Fatal("lost subscription sent or discarded the original claim")
	}
}

func TestEventConnectionCannotFillAnAlreadyClaimedInputGap(t *testing.T) {
	f := newSessionFixture(t)
	f.create(t)
	if _, err := f.api.submit(context.Background(), domain.NewID(), fixtureMessageID, fixturePartID, "private input"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.openEvents(context.Background()); err == nil || f.api.eventAttempt {
		t.Fatal("late subscription pretended to cover an original input")
	}
}
