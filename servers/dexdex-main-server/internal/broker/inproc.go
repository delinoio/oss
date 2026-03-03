package broker

import (
	"context"
	"sync"
	"time"

	v1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/repository"
)

type InProcBroker struct {
	store *repository.Store

	mu          sync.RWMutex
	subscribers map[string]map[chan *v1.StreamWorkspaceEventsResponse]struct{}
}

func NewInProc(store *repository.Store) *InProcBroker {
	return &InProcBroker{
		store:       store,
		subscribers: make(map[string]map[chan *v1.StreamWorkspaceEventsResponse]struct{}),
	}
}

func (b *InProcBroker) Publish(ctx context.Context, workspaceID string, event *v1.StreamWorkspaceEventsResponse) (*v1.StreamWorkspaceEventsResponse, error) {
	persisted, err := b.store.AppendWorkspaceEvent(ctx, workspaceID, event)
	if err != nil {
		return nil, err
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers[workspaceID] {
		select {
		case ch <- persisted:
		default:
		}
	}

	return persisted, nil
}

func (b *InProcBroker) Replay(ctx context.Context, workspaceID string, fromSequence uint64, limit int) ([]*v1.StreamWorkspaceEventsResponse, uint64, error) {
	return replayWithCursorValidation(ctx, b.store, workspaceID, fromSequence, limit)
}

func (b *InProcBroker) Stream(ctx context.Context, workspaceID string, fromSequence uint64, limit int, send func(*v1.StreamWorkspaceEventsResponse) error) error {
	replayed, _, err := b.Replay(ctx, workspaceID, fromSequence, limit)
	if err != nil {
		return err
	}

	latest := fromSequence
	for _, event := range replayed {
		if err := send(event); err != nil {
			return err
		}
		latest = event.Sequence
	}

	ch := make(chan *v1.StreamWorkspaceEventsResponse, 32)
	b.subscribe(workspaceID, ch)
	defer b.unsubscribe(workspaceID, ch)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case event := <-ch:
			if event == nil {
				continue
			}
			if event.Sequence <= latest {
				continue
			}
			if err := send(event); err != nil {
				return err
			}
			latest = event.Sequence
		case <-ticker.C:
			// keep stream loop responsive even when no events are published.
		}
	}
}

func (b *InProcBroker) subscribe(workspaceID string, ch chan *v1.StreamWorkspaceEventsResponse) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subscribers[workspaceID]; !ok {
		b.subscribers[workspaceID] = make(map[chan *v1.StreamWorkspaceEventsResponse]struct{})
	}
	b.subscribers[workspaceID][ch] = struct{}{}
}

func (b *InProcBroker) unsubscribe(workspaceID string, ch chan *v1.StreamWorkspaceEventsResponse) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.subscribers[workspaceID]
	if subs == nil {
		return
	}
	delete(subs, ch)
	if len(subs) == 0 {
		delete(b.subscribers, workspaceID)
	}
	close(ch)
}
