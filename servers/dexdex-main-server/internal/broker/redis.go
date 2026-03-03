package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	v1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/repository"
	"github.com/redis/go-redis/v9"
)

type RedisBroker struct {
	store        *repository.Store
	client       *redis.Client
	streamPrefix string
}

func NewRedis(store *repository.Store, redisAddr string, streamPrefix string) *RedisBroker {
	return &RedisBroker{
		store: store,
		client: redis.NewClient(&redis.Options{
			Addr: redisAddr,
		}),
		streamPrefix: streamPrefix,
	}
}

func (b *RedisBroker) Close() error {
	if b == nil || b.client == nil {
		return nil
	}
	return b.client.Close()
}

func (b *RedisBroker) Publish(ctx context.Context, workspaceID string, event *v1.StreamWorkspaceEventsResponse) (*v1.StreamWorkspaceEventsResponse, error) {
	persisted, err := b.store.AppendWorkspaceEvent(ctx, workspaceID, event)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(persisted)
	if err != nil {
		return nil, fmt.Errorf("marshal redis event: %w", err)
	}

	streamKey := b.streamKey(workspaceID)
	if _, err := b.client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: map[string]any{
			"sequence":   persisted.Sequence,
			"event_json": string(payload),
		},
	}).Result(); err != nil {
		return nil, fmt.Errorf("publish redis stream event: %w", err)
	}

	return persisted, nil
}

func (b *RedisBroker) Replay(ctx context.Context, workspaceID string, fromSequence uint64, limit int) ([]*v1.StreamWorkspaceEventsResponse, uint64, error) {
	return replayWithCursorValidation(ctx, b.store, workspaceID, fromSequence, limit)
}

func (b *RedisBroker) Stream(ctx context.Context, workspaceID string, fromSequence uint64, limit int, send func(*v1.StreamWorkspaceEventsResponse) error) error {
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

	streamKey := b.streamKey(workspaceID)
	startID := "$"

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		results, err := b.client.XRead(ctx, &redis.XReadArgs{
			Streams: []string{streamKey, startID},
			Block:   10 * time.Second,
			Count:   int64(limit),
		}).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return fmt.Errorf("read redis stream: %w", err)
		}

		for _, stream := range results {
			for _, message := range stream.Messages {
				startID = message.ID
				rawJSON, ok := message.Values["event_json"].(string)
				if !ok {
					continue
				}
				event := &v1.StreamWorkspaceEventsResponse{}
				if err := json.Unmarshal([]byte(rawJSON), event); err != nil {
					continue
				}
				if event.Sequence <= latest {
					continue
				}
				if err := send(event); err != nil {
					return err
				}
				latest = event.Sequence
			}
		}
	}
}

func (b *RedisBroker) streamKey(workspaceID string) string {
	return fmt.Sprintf("%s:%s", b.streamPrefix, workspaceID)
}
