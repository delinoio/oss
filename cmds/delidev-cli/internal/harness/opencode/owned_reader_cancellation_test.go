package opencode

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestOwnedAPIWaitingConsumerCancellationPreservesOriginalStream(t *testing.T) {
	f := newObserverFixture(t)
	original, cancelOriginal := context.WithCancel(context.Background())
	defer cancelOriginal()
	reader, writer := io.Pipe()
	defer writer.Close()
	stream := &eventStream{ctx: original, body: reader, cancel: cancelOriginal, queue: make(chan NativeEvent, maxQueuedEvents), done: make(chan struct{}), seen: map[string]bool{}, last: time.Now(), alive: func() error { return nil }}
	go stream.run(original)
	defer stream.Close()
	session := &sessionAPI{creation: &f.o.creation, input: &f.o.input, observer: f.o, events: stream, gate: make(chan struct{}, 1)}
	api := &OwnedAPI{session: session, reading: make(chan struct{}, 1)}
	send := func(frame string) {
		t.Helper()
		done := make(chan error, 1)
		go func() { _, err := io.WriteString(writer, frame); done <- err }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("original stream stopped reading native frames")
		}
	}
	send(eventFrame(100, ServerConnectedEvent, map[string]any{}))
	if _, err := stream.Next(original); err != nil {
		t.Fatal(err)
	}
	waiting, stopWaiting := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stopWaiting()
	if _, err := api.Next(waiting); err == nil || original.Err() != nil || stream.status() != nil {
		t.Fatal("waiting consumer cancellation ended the original native stream")
	}
	progress, err := api.Progress(context.Background())
	if err != nil || progress.NeedsRecovery || progress.UserSeen || progress.InputPartSeen {
		t.Fatal("waiting cancellation fabricated transport loss or native input")
	}
	for _, event := range []NativeEvent{
		f.event(MessageUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "info": f.user}),
		f.event(MessagePartUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "part": f.input, "time": 1240}),
	} {
		var properties any
		if err := json.Unmarshal(event.Properties, &properties); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(map[string]any{"id": event.ID, "type": event.Kind, "properties": properties})
		if err != nil {
			t.Fatal(err)
		}
		send("data: " + string(raw) + "\n\n")
		observed, err := api.Next(original)
		if err != nil || observed.EventID != event.ID {
			t.Fatal("original arrival was lost after canceled waiting", err)
		}
	}
	progress, err = api.Progress(context.Background())
	if err != nil || progress.NeedsRecovery || !progress.UserSeen || !progress.InputPartSeen {
		t.Fatal("same original observer could not continue after canceled waiting")
	}
	cancelOriginal()
	select {
	case <-stream.done:
	case <-time.After(time.Second):
		t.Fatal("original owner cancellation did not join its reader")
	}
	if _, err := api.Next(context.Background()); err == nil {
		t.Fatal("original stream closure was ignored")
	}
	progress, err = api.Progress(context.Background())
	if err != nil || !progress.NeedsRecovery {
		t.Fatal("original closure lost recovery state")
	}
}

func TestEventStreamCanceledConsumerCannotConsumeQueuedArrival(t *testing.T) {
	event := NativeEvent{ID: "evt_000000000001ABCDEFGHIJKLMN", Kind: ServerHeartbeatEvent, Properties: []byte("{}")}
	stream := &eventStream{ctx: context.Background(), queue: make(chan NativeEvent, 1), pending: len(event.Properties)}
	stream.queue <- event
	caller, cancel := context.WithCancel(context.Background())
	cancel()
	for range 100 {
		if _, err := stream.Next(caller); err == nil || len(stream.queue) != 1 || stream.pending != len(event.Properties) || stream.status() != nil {
			t.Fatal("already canceled consumer changed queued native evidence")
		}
	}
	observed, err := stream.Next(context.Background())
	if err != nil || observed.ID != event.ID || stream.pending != 0 {
		t.Fatal("original arrival could not be consumed once")
	}
}
