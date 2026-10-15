package nativewire

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// profileCase is one row of the transport limit table owned by the harness
// contract. Every profile is exercised exactly at and one past each bound.
type profileCase struct {
	profile    Profile
	fixture    string
	jsonrpc    bool
	frameBytes int
	queueBytes int
	eventCount int
}

var profileCases = []profileCase{
	{profile: ProfileCodex, fixture: "normal", frameBytes: 1 << 20, queueBytes: 8 << 20, eventCount: 4096},
	{profile: ProfileImage, fixture: "image-observations", frameBytes: 16 << 20, queueBytes: 32 << 20, eventCount: 4096},
	{profile: ProfileJSONRPC, fixture: "jsonrpc", jsonrpc: true, frameBytes: 1 << 20, queueBytes: 8 << 20, eventCount: 128},
}

// limitLog captures structured logs so every case can assert the exact record.
type limitLog struct{ buffer bytes.Buffer }

func (l *limitLog) logger() *slog.Logger { return slog.New(slog.NewJSONHandler(&l.buffer, nil)) }

// records returns every native_limit_exceeded record and fails the test when a
// record carries any field outside the closed content-free schema.
func (l *limitLog) records(t *testing.T) []map[string]any {
	t.Helper()
	var result []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(l.buffer.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["msg"] != "native_limit_exceeded" {
			continue
		}
		for key := range record {
			switch key {
			case "time", "level", "msg", "limit_kind", "profile", "observed", "maximum":
			default:
				t.Fatalf("limit record carries unexpected field %q", key)
			}
		}
		result = append(result, record)
	}
	return result
}

func (l *limitLog) requireOne(t *testing.T, profile Profile, kind LimitKind, observed, maximum int) {
	t.Helper()
	records := l.records(t)
	if len(records) != 1 {
		t.Fatalf("expected exactly one limit record, got %d", len(records))
	}
	record := records[0]
	if record["limit_kind"] != string(kind) || record["profile"] != string(profile) || record["observed"] != float64(observed) || record["maximum"] != float64(maximum) {
		t.Fatalf("wrong limit record: %v", record)
	}
}

func (l *limitLog) requireNone(t *testing.T) {
	t.Helper()
	if records := l.records(t); len(records) != 0 {
		t.Fatalf("unexpected limit record: %v", records)
	}
}

// newUnit builds an unstarted connection for exact in-memory boundary tests.
func newUnit(t *testing.T, test profileCase) (*Connection, *limitLog) {
	t.Helper()
	var log limitLog
	c := newConnection(test.profile, func() {}, log.logger(), nil)
	return c, &log
}

func (c *Connection) queue(event Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queueLocked(event)
}

func requireExhausted(t *testing.T, err error) {
	t.Helper()
	problem := domain.SafeError(err)
	if err == nil || problem.Code != domain.ResourceExhausted || problem.Message != "Native event buffering reached its bound." {
		t.Fatalf("expected the unchanged public exhaustion error, got %v", err)
	}
}

func TestProfileLimitTable(t *testing.T) {
	for _, test := range profileCases {
		t.Run(string(test.profile), func(t *testing.T) {
			c, _ := newUnit(t, test)
			if c.frameLimit() != test.frameBytes || c.eventLimit() != test.queueBytes || c.countLimit() != test.eventCount || cap(c.events) != test.eventCount || (c.jsonrpc != "") != test.jsonrpc {
				t.Fatal("profile limits differ from the documented table")
			}
			started, _, _ := startFixture(t, test.fixture)
			if started.profile != test.profile || cap(started.events) != test.eventCount || started.frameLimit() != test.frameBytes || started.eventLimit() != test.queueBytes {
				t.Fatal("started connection did not select its profile limits")
			}
		})
	}
	if maxPending != 128 || maxRequests != 4096 {
		t.Fatal("pending and remembered request bounds changed")
	}
}

func TestEventDescriptorMemoryBound(t *testing.T) {
	size := int(unsafe.Sizeof(Event{}))
	for _, test := range profileCases {
		reserved := test.eventCount * size
		t.Logf("profile=%s event_descriptor_bytes=%d slots=%d reserved_descriptor_bytes=%d queued_payload_bytes=%d", test.profile, size, test.eventCount, reserved, test.queueBytes)
		// Documented worst case: slots * descriptor plus the independent payload
		// byte budget. The descriptor reservation stays below 1 MiB on 64-bit targets.
		if strconv.IntSize == 64 && reserved > 1<<20 {
			t.Fatalf("descriptor reservation %d exceeds the documented bound", reserved)
		}
	}
}

func TestEventCountBoundaryAdmitsExactlyTheProfileCount(t *testing.T) {
	for _, test := range profileCases {
		t.Run(string(test.profile), func(t *testing.T) {
			c, log := newUnit(t, test)
			done := make(chan struct{})
			go func() {
				defer close(done)
				// Row: queue holds count-1 events -> the count-th is admitted.
				for i := 0; i < test.eventCount; i++ {
					if err := c.queue(Event{Kind: Notification, size: 1}); err != nil {
						t.Errorf("event %d of %d rejected: %v", i+1, test.eventCount, err)
						return
					}
				}
				log.requireNone(t)
				// Row: queue holds count events -> the next fails without blocking.
				requireExhausted(t, c.queue(Event{Kind: Notification, size: 1}))
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("queueing blocked the stdout reader on a full queue")
			}
			log.requireOne(t, test.profile, LimitEventCount, test.eventCount+1, test.eventCount)
			// Row: one consumed event frees exactly one slot again.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := c.Next(ctx); err != nil {
				t.Fatal(err)
			}
			if err := c.queue(Event{Kind: Notification, size: 1}); err != nil {
				t.Fatalf("consumed slot was not reusable: %v", err)
			}
		})
	}
}

func TestEventByteBoundaryBelowTheCountCeiling(t *testing.T) {
	for _, test := range profileCases {
		t.Run(string(test.profile), func(t *testing.T) {
			// Row: one event whose size equals the queue budget is admitted.
			c, log := newUnit(t, test)
			if err := c.queue(Event{Kind: Notification, size: test.queueBytes}); err != nil {
				t.Fatal(err)
			}
			log.requireNone(t)
			// Row: queue is exactly full by bytes with one event -> one more byte fails.
			requireExhausted(t, c.queue(Event{Kind: Notification, size: 1}))
			log.requireOne(t, test.profile, LimitEventBytes, test.queueBytes+1, test.queueBytes)

			// Row: a single event one byte over the budget fails on an empty queue.
			c, log = newUnit(t, test)
			requireExhausted(t, c.queue(Event{Kind: Notification, size: test.queueBytes + 1}))
			log.requireOne(t, test.profile, LimitEventBytes, test.queueBytes+1, test.queueBytes)

			// Row: several events totalling exactly the budget are admitted, the next byte fails,
			// all with far fewer events than the count ceiling.
			c, log = newUnit(t, test)
			for i := 0; i < 3; i++ {
				if err := c.queue(Event{Kind: Notification, size: test.queueBytes / 4}); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.queue(Event{Kind: Notification, size: test.queueBytes - 3*(test.queueBytes/4)}); err != nil {
				t.Fatal(err)
			}
			log.requireNone(t)
			requireExhausted(t, c.queue(Event{Kind: Notification, size: 1}))
			log.requireOne(t, test.profile, LimitEventBytes, test.queueBytes+1, test.queueBytes)

			// Row: consuming frees the bytes so admission resumes.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := c.Next(ctx); err != nil {
				t.Fatal(err)
			}
			if err := c.queue(Event{Kind: Notification, size: test.queueBytes / 4}); err != nil {
				t.Fatalf("consumed bytes were not released: %v", err)
			}
		})
	}
}

func TestCountBoundTakesPrecedenceWhenBothBoundsAreExceeded(t *testing.T) {
	for _, test := range profileCases {
		t.Run(string(test.profile), func(t *testing.T) {
			c, log := newUnit(t, test)
			for i := 0; i < test.eventCount; i++ {
				if err := c.queue(Event{Kind: Notification, size: 1}); err != nil {
					t.Fatal(err)
				}
			}
			requireExhausted(t, c.queue(Event{Kind: Notification, size: test.queueBytes + 1}))
			log.requireOne(t, test.profile, LimitEventCount, test.eventCount+1, test.eventCount)
		})
	}
}

// notificationFrame returns one valid notification of exactly the requested size.
func notificationFrame(t *testing.T, c *Connection, size int) []byte {
	t.Helper()
	prefix := `{"method":"progress","params":{"p":"`
	if c.jsonrpc != "" {
		prefix = `{"jsonrpc":"2.0","method":"progress","params":{"p":"`
	}
	const suffix = `"}}`
	padding := size - len(prefix) - len(suffix)
	if padding < 0 {
		t.Fatal("frame size is below the minimal notification")
	}
	return []byte(prefix + strings.Repeat("x", padding) + suffix)
}

func TestFrameBoundaryAtAndPastTheProfileBound(t *testing.T) {
	for _, test := range profileCases {
		t.Run(string(test.profile), func(t *testing.T) {
			// Row: a frame exactly at the bound is admitted and queued by size.
			c, log := newUnit(t, test)
			writer := &frameWriter{connection: c}
			frame := notificationFrame(t, c, test.frameBytes)
			if n, err := writer.Write(append(frame, '\n')); err != nil || n != len(frame)+1 {
				t.Fatal(n, err)
			}
			if problem := c.status(); problem != nil || len(c.events) != 1 || c.eventBytes != test.frameBytes {
				t.Fatalf("frame at the bound was not admitted: %v %d %d", problem, len(c.events), c.eventBytes)
			}
			log.requireNone(t)

			// Row: a frame one byte over the bound fails before parsing.
			c, log = newUnit(t, test)
			writer = &frameWriter{connection: c}
			over := append(bytes.Repeat([]byte("x"), test.frameBytes+1), '\n')
			if _, err := writer.Write(over); err != nil {
				t.Fatal(err)
			}
			if problem := c.status(); problem == nil || problem.Code != domain.ResourceExhausted || problem.Message != "A native protocol frame exceeds its bound." || len(c.events) != 0 {
				t.Fatalf("frame past the bound was not rejected: %v", problem)
			}
			log.requireOne(t, test.profile, LimitFrameBytes, test.frameBytes+1, test.frameBytes)
		})
	}
}

func TestFrameBoundaryAcrossFragmentedWrites(t *testing.T) {
	for _, test := range profileCases {
		t.Run(string(test.profile), func(t *testing.T) {
			c, log := newUnit(t, test)
			writer := &frameWriter{connection: c}
			frame := notificationFrame(t, c, test.frameBytes)
			// The bound counts accumulated bytes, not one Write call.
			half := len(frame) / 2
			if _, err := writer.Write(frame[:half]); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(append(frame[half:], '\n')); err != nil {
				t.Fatal(err)
			}
			if problem := c.status(); problem != nil || len(c.events) != 1 {
				t.Fatalf("fragmented frame at the bound failed: %v", problem)
			}
			log.requireNone(t)
			writer = &frameWriter{connection: c}
			if _, err := writer.Write(bytes.Repeat([]byte("x"), test.frameBytes)); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write([]byte("x\n")); err != nil {
				t.Fatal(err)
			}
			if problem := c.status(); problem == nil || problem.Code != domain.ResourceExhausted {
				t.Fatal("fragmented frame past the bound was admitted")
			}
			log.requireOne(t, test.profile, LimitFrameBytes, test.frameBytes+1, test.frameBytes)
		})
	}
}

func TestEventBurstThroughProcessAtAndPastTheCountBound(t *testing.T) {
	for _, test := range profileCases {
		t.Run(string(test.profile), func(t *testing.T) {
			// Row: exactly count events are admitted while the queue is full and the
			// native reply still progresses; every event is then delivered in order.
			c, config, logs := startFixture(t, test.fixture)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			response, err := c.Call(ctx, domain.NewID(), "events", map[string]int{"count": test.eventCount})
			if err != nil || string(response.Result) != `{"accepted":true}` {
				t.Fatalf("reply did not progress behind a full queue: %v", err)
			}
			if c.Err() != nil {
				t.Fatal("exactly the profile count failed the connection")
			}
			for i := 0; i < test.eventCount; i++ {
				event, err := c.Next(ctx)
				if err != nil || string(event.Params) != fmt.Sprintf(`{"step":%d}`, i) {
					t.Fatalf("event %d lost or reordered: %v", i, err)
				}
			}
			if strings.Contains(logs.String(), "native_limit_exceeded") {
				t.Fatal("limit record logged without exhaustion")
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}

			// Row: count+1 undelivered events fail with the public exhaustion code,
			// log one event_count record and terminate the owned scope.
			c, config, logs = startFixture(t, test.fixture)
			_, err = c.Call(ctx, domain.NewID(), "events", map[string]int{"count": test.eventCount + 1})
			if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatalf("exhaustion asserted delivery: %v", err)
			}
			select {
			case <-c.Done():
			case <-ctx.Done():
				t.Fatal("scope did not stop")
			}
			if problem := c.Err(); problem == nil || problem.Code != domain.ResourceExhausted || problem.Message != "Native event buffering reached its bound." {
				t.Fatalf("wrong exhaustion error: %v", problem)
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			if err := process.ReconcileOwner(config.Directory, config.OwnerID); err != nil {
				t.Fatal(err)
			}
			var records int
			for _, line := range strings.Split(logs.String(), "\n") {
				if !strings.Contains(line, "native_limit_exceeded") {
					continue
				}
				records++
				if !strings.Contains(line, `"limit_kind":"event_count"`) || !strings.Contains(line, `"profile":"`+string(test.profile)+`"`) || !strings.Contains(line, fmt.Sprintf(`"observed":%d`, test.eventCount+1)) || !strings.Contains(line, fmt.Sprintf(`"maximum":%d`, test.eventCount)) || strings.Contains(line, "progress") {
					t.Fatalf("wrong limit record: %s", line)
				}
			}
			if records != 1 {
				t.Fatalf("expected one limit record, got %d", records)
			}
		})
	}
}

func TestIdentitiesSurviveAtEventQueueCapacity(t *testing.T) {
	for _, test := range profileCases {
		t.Run(string(test.profile), func(t *testing.T) {
			c, log := newUnit(t, test)
			frame := func(fields map[string]any) []byte {
				if c.jsonrpc != "" {
					fields["jsonrpc"] = "2.0"
				}
				raw, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			lateID := domain.NewID()
			c.pending["s:"+string(lateID)] = &pending{response: make(chan Response, 1), abandoned: true}
			for i := 0; i < test.eventCount-2; i++ {
				if err := c.receive(frame(map[string]any{"method": "progress", "params": map[string]int{"step": i}})); err != nil {
					t.Fatal(err)
				}
			}
			// The server request takes the next-to-last slot and the late response the last.
			if err := c.receive(frame(map[string]any{"id": 7, "method": "approval", "params": map[string]int{}})); err != nil {
				t.Fatal(err)
			}
			if err := c.receive(frame(map[string]any{"id": string(lateID), "result": map[string]bool{"accepted": true}})); err != nil {
				t.Fatal(err)
			}
			if len(c.events) != test.eventCount {
				t.Fatalf("queue is not at capacity: %d", len(c.events))
			}
			requireExhausted(t, c.receive(frame(map[string]any{"method": "progress", "params": map[string]int{}})))
			log.requireOne(t, test.profile, LimitEventCount, test.eventCount+1, test.eventCount)

			// A queued state failure does not erase already queued facts.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var request, late Event
			for i := 0; i < test.eventCount; i++ {
				event, err := c.Next(ctx)
				if err != nil {
					t.Fatal(err)
				}
				switch event.Kind {
				case ServerRequest:
					request = event
				case LateResponse:
					late = event
				}
			}
			if request.Kind != ServerRequest || string(request.ID) != "7" || request.Token.Validate() != nil || c.incoming["n:7"].token != request.Token {
				t.Fatal("server request lost its original identity at capacity")
			}
			if late.Kind != LateResponse || string(late.ID) != `"`+string(lateID)+`"` || string(late.Response.Result) != `{"accepted":true}` {
				t.Fatal("late response lost its original identity at capacity")
			}
			if retired, err := c.RetireRequest(request); err != nil || !retired {
				t.Fatalf("bounded cancellation failed after exhaustion: %v", err)
			}
			if len(c.incoming) != 0 {
				t.Fatal("retired request remained tracked")
			}
		})
	}
}
