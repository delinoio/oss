package stream

import (
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestFanOutPublishAndSubscribe(t *testing.T) {
	fo := NewFanOut(1000, testLogger())

	ch, unsub := fo.Subscribe("ws-1")
	defer unsub()

	fo.Publish("ws-1", dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, &TaskPayload{
		Task: &dexdexv1.UnitTask{UnitTaskId: "task-1"},
	})

	select {
	case resp := <-ch:
		if resp.Sequence != 1 {
			t.Fatalf("expected sequence 1, got %d", resp.Sequence)
		}
		if resp.WorkspaceId != "ws-1" {
			t.Fatalf("expected workspace_id ws-1, got %s", resp.WorkspaceId)
		}
		if resp.EventType != dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED {
			t.Fatalf("expected TASK_UPDATED, got %v", resp.EventType)
		}
		if resp.GetTask() == nil || resp.GetTask().UnitTaskId != "task-1" {
			t.Fatalf("expected task payload with id task-1, got %v", resp.GetTask())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestFanOutSubscriberDoesNotReceiveOtherWorkspaceEvents(t *testing.T) {
	fo := NewFanOut(1000, testLogger())

	ch, unsub := fo.Subscribe("ws-1")
	defer unsub()

	fo.Publish("ws-2", dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, nil)

	select {
	case <-ch:
		t.Fatal("should not receive events from another workspace")
	case <-time.After(50 * time.Millisecond):
		// Expected: no event received.
	}
}

func TestFanOutUnsubscribeStopsDelivery(t *testing.T) {
	fo := NewFanOut(1000, testLogger())

	ch, unsub := fo.Subscribe("ws-1")
	unsub()

	fo.Publish("ws-1", dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, nil)

	// Channel should be closed.
	_, ok := <-ch
	if ok {
		t.Fatal("expected channel to be closed after unsubscribe")
	}
}

func TestFanOutSequenceIsMonotonic(t *testing.T) {
	fo := NewFanOut(1000, testLogger())

	ch, unsub := fo.Subscribe("ws-1")
	defer unsub()

	for i := 0; i < 5; i++ {
		fo.Publish("ws-1", dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, nil)
	}

	var lastSeq uint64
	for i := 0; i < 5; i++ {
		select {
		case resp := <-ch:
			if resp.Sequence <= lastSeq {
				t.Fatalf("sequence not monotonic: got %d after %d", resp.Sequence, lastSeq)
			}
			lastSeq = resp.Sequence
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}
}

func TestFanOutReplayFromSequence(t *testing.T) {
	fo := NewFanOut(1000, testLogger())

	fo.Publish("ws-1", dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, nil)
	fo.Publish("ws-1", dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, nil)
	fo.Publish("ws-1", dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_OUTPUT, nil)

	// Replay from sequence 1 (exclusive), should get events 2 and 3.
	events, err := fo.Replay("ws-1", 1)
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 replayed events, got %d", len(events))
	}
	if events[0].Sequence != 2 {
		t.Fatalf("expected first replayed event sequence 2, got %d", events[0].Sequence)
	}
	if events[1].Sequence != 3 {
		t.Fatalf("expected second replayed event sequence 3, got %d", events[1].Sequence)
	}
}

func TestFanOutReplayFromZeroReturnsAll(t *testing.T) {
	fo := NewFanOut(1000, testLogger())

	fo.Publish("ws-1", dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, nil)
	fo.Publish("ws-1", dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, nil)

	events, err := fo.Replay("ws-1", 0)
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

func TestFanOutReplayOutOfRange(t *testing.T) {
	fo := NewFanOut(3, testLogger())

	// Publish 5 events, buffer holds only 3 (sequences 3, 4, 5).
	for i := 0; i < 5; i++ {
		fo.Publish("ws-1", dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, nil)
	}

	// Request from sequence 1, but earliest is 3.
	_, err := fo.Replay("ws-1", 1)
	if err == nil {
		t.Fatal("expected out-of-range error")
	}

	var oorErr *ReplayOutOfRangeError
	if !errors.As(err, &oorErr) {
		t.Fatalf("expected ReplayOutOfRangeError, got %T: %v", err, err)
	}
	if oorErr.EarliestAvailableSequence != 3 {
		t.Fatalf("expected earliest 3, got %d", oorErr.EarliestAvailableSequence)
	}
	if oorErr.RequestedFromSequence != 1 {
		t.Fatalf("expected requested 1, got %d", oorErr.RequestedFromSequence)
	}
}

func TestFanOutReplayEmptyWorkspaceReturnsNil(t *testing.T) {
	fo := NewFanOut(1000, testLogger())

	events, err := fo.Replay("ws-nonexistent", 0)
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	if events != nil {
		t.Fatalf("expected nil, got %v", events)
	}
}

func TestFanOutBufferEviction(t *testing.T) {
	fo := NewFanOut(3, testLogger())

	for i := 0; i < 5; i++ {
		fo.Publish("ws-1", dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, nil)
	}

	// Buffer should contain only the last 3 events (sequences 3, 4, 5).
	events, err := fo.Replay("ws-1", 2)
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events after eviction, got %d", len(events))
	}
	if events[0].Sequence != 3 {
		t.Fatalf("expected first event sequence 3, got %d", events[0].Sequence)
	}
}

func TestFanOutConcurrentPublishSubscribe(t *testing.T) {
	fo := NewFanOut(1000, testLogger())

	ch, unsub := fo.Subscribe("ws-1")
	defer unsub()

	const numEvents = 100
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		for i := 0; i < numEvents; i++ {
			fo.Publish("ws-1", dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, nil)
		}
	}()

	received := 0
	timeout := time.After(5 * time.Second)
	for received < numEvents {
		select {
		case <-ch:
			received++
		case <-timeout:
			t.Fatalf("timed out after receiving %d/%d events", received, numEvents)
		}
	}

	wg.Wait()
}

func TestFanOutMultipleSubscribers(t *testing.T) {
	fo := NewFanOut(1000, testLogger())

	ch1, unsub1 := fo.Subscribe("ws-1")
	defer unsub1()
	ch2, unsub2 := fo.Subscribe("ws-1")
	defer unsub2()

	fo.Publish("ws-1", dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, nil)

	for _, ch := range []<-chan *dexdexv1.StreamWorkspaceEventsResponse{ch1, ch2} {
		select {
		case resp := <-ch:
			if resp.Sequence != 1 {
				t.Fatalf("expected sequence 1, got %d", resp.Sequence)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for event on subscriber")
		}
	}
}

func TestFanOutPublishWithNilPayload(t *testing.T) {
	fo := NewFanOut(1000, testLogger())

	ch, unsub := fo.Subscribe("ws-1")
	defer unsub()

	fo.Publish("ws-1", dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, nil)

	select {
	case resp := <-ch:
		if resp.GetPayload() != nil {
			t.Fatalf("expected nil payload, got %v", resp.GetPayload())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}
