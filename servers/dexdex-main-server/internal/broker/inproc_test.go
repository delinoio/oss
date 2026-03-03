package broker

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	v1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/repository"
)

func openInProcTestStore(t *testing.T) *repository.Store {
	t.Helper()
	store, err := repository.NewSQLite(filepath.Join(t.TempDir(), "dexdex-main.sqlite3"))
	if err != nil {
		t.Fatalf("NewSQLite returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestInProcStreamReplaysUntilCaughtUpBeforeLiveMode(t *testing.T) {
	ctx := context.Background()
	store := openInProcTestStore(t)
	broker := NewInProc(store)

	workspaceID := "workspace-1"
	for i := 0; i < 5; i++ {
		_, err := broker.Publish(ctx, workspaceID, &v1.StreamWorkspaceEventsResponse{
			EventType: v1.StreamEventType_STREAM_EVENT_TYPE_NOTIFICATION_CREATED,
			Payload: &v1.StreamWorkspaceEventsResponse_NotificationCreated{
				NotificationCreated: &v1.NotificationCreatedEvent{
					Notification: &v1.NotificationRecord{NotificationId: "note"},
				},
			},
		})
		if err != nil {
			t.Fatalf("Publish returned error: %v", err)
		}
	}

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sequences := make([]uint64, 0, 5)
	var mu sync.Mutex
	done := make(chan struct{})

	go func() {
		err := broker.Stream(streamCtx, workspaceID, 0, 2, func(event *v1.StreamWorkspaceEventsResponse) error {
			mu.Lock()
			sequences = append(sequences, event.Sequence)
			currentCount := len(sequences)
			mu.Unlock()

			if currentCount == 5 {
				cancel()
			}
			return nil
		})
		if err != nil {
			t.Errorf("Stream returned error: %v", err)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish in time")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sequences) != 5 {
		t.Fatalf("unexpected sequence count: got=%d want=5", len(sequences))
	}
	for index, sequence := range sequences {
		want := uint64(index + 1)
		if sequence != want {
			t.Fatalf("unexpected sequence at index %d: got=%d want=%d", index, sequence, want)
		}
	}
}
