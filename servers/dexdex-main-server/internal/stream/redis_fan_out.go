package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

// RedisFanOut implements EventBroadcaster using Redis Pub/Sub for live events
// and Redis Streams for bounded retention and replay.
type RedisFanOut struct {
	client     *redis.Client
	bufferSize int
	logger     *slog.Logger

	mu        sync.Mutex
	sequence  uint64
	nextSubID uint64
}

// redisFanOutEvent is the JSON envelope published to Redis channels and streams.
type redisFanOutEvent struct {
	Sequence    uint64 `json:"sequence"`
	WorkspaceID string `json:"workspace_id"`
	EventType   int32  `json:"event_type"`
	OccurredAt  string `json:"occurred_at"`
	PayloadJSON string `json:"payload_json"`
}

// NewRedisFanOut creates a new RedisFanOut connected to the given Redis URL.
func NewRedisFanOut(redisURL string, bufferSize int, logger *slog.Logger) (*RedisFanOut, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid redis URL: %w", err)
	}

	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	logger.Info("redis fan-out connected", "url", redisURL)

	return &RedisFanOut{
		client:     client,
		bufferSize: bufferSize,
		logger:     logger,
	}, nil
}

func (r *RedisFanOut) channelKey(workspaceID string) string {
	return fmt.Sprintf("dexdex:ws:%s", workspaceID)
}

func (r *RedisFanOut) streamKey(workspaceID string) string {
	return fmt.Sprintf("dexdex:stream:%s", workspaceID)
}

// Publish broadcasts an event via Redis Pub/Sub and appends to a Redis Stream.
func (r *RedisFanOut) Publish(workspaceID string, eventType dexdexv1.StreamEventType, payload isStreamWorkspaceEventsResponsePayload) {
	r.mu.Lock()
	r.sequence++
	seq := r.sequence
	r.mu.Unlock()

	now := time.Now()

	resp := &dexdexv1.StreamWorkspaceEventsResponse{
		Sequence:    seq,
		WorkspaceId: workspaceID,
		EventType:   eventType,
		OccurredAt:  timestamppb.New(now),
	}

	if payload != nil {
		payload.setPayload(resp)
	}

	// Serialize the full response as JSON for transport.
	payloadBytes, err := protojson.Marshal(resp)
	if err != nil {
		r.logger.Error("failed to marshal event payload", "error", err)
		return
	}

	evt := redisFanOutEvent{
		Sequence:    seq,
		WorkspaceID: workspaceID,
		EventType:   int32(eventType),
		OccurredAt:  now.Format(time.RFC3339Nano),
		PayloadJSON: string(payloadBytes),
	}

	evtJSON, err := json.Marshal(evt)
	if err != nil {
		r.logger.Error("failed to marshal redis event", "error", err)
		return
	}

	ctx := context.Background()

	// PUBLISH to Redis channel for live subscribers.
	if err := r.client.Publish(ctx, r.channelKey(workspaceID), string(evtJSON)).Err(); err != nil {
		r.logger.Error("redis PUBLISH failed", "workspace_id", workspaceID, "error", err)
	}

	// XADD to Redis Stream for replay with bounded retention.
	if err := r.client.XAdd(ctx, &redis.XAddArgs{
		Stream: r.streamKey(workspaceID),
		MaxLen: int64(r.bufferSize),
		Approx: true,
		Values: map[string]interface{}{
			"data":     string(evtJSON),
			"sequence": seq,
		},
	}).Err(); err != nil {
		r.logger.Error("redis XADD failed", "workspace_id", workspaceID, "error", err)
	}

	r.logger.Debug("event published via redis",
		"workspace_id", workspaceID,
		"event_type", eventType.String(),
		"sequence", seq,
	)
}

// Subscribe returns a channel that receives events for the given workspace
// and an unsubscribe function. It creates a goroutine that SUBSCRIBE to
// the Redis channel and forwards messages to the returned channel.
func (r *RedisFanOut) Subscribe(workspaceID string) (<-chan *dexdexv1.StreamWorkspaceEventsResponse, func()) {
	ch := make(chan *dexdexv1.StreamWorkspaceEventsResponse, 256)

	r.mu.Lock()
	r.nextSubID++
	subID := r.nextSubID
	r.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	pubsub := r.client.Subscribe(ctx, r.channelKey(workspaceID))

	r.logger.Info("redis subscriber added",
		"workspace_id", workspaceID,
		"subscriber_id", subID,
	)

	go func() {
		defer close(ch)
		redisCh := pubsub.Channel()

		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-redisCh:
				if !ok {
					return
				}

				var evt redisFanOutEvent
				if err := json.Unmarshal([]byte(msg.Payload), &evt); err != nil {
					r.logger.Error("failed to unmarshal redis event", "error", err)
					continue
				}

				resp := &dexdexv1.StreamWorkspaceEventsResponse{}
				if err := protojson.Unmarshal([]byte(evt.PayloadJSON), resp); err != nil {
					r.logger.Error("failed to unmarshal event payload", "error", err)
					continue
				}

				select {
				case ch <- resp:
				default:
					r.logger.Warn("subscriber channel full, dropping event",
						"workspace_id", workspaceID,
						"sequence", evt.Sequence,
						"subscriber_id", subID,
					)
				}
			}
		}
	}()

	unsubscribe := func() {
		cancel()
		_ = pubsub.Close()
		r.logger.Info("redis subscriber removed",
			"workspace_id", workspaceID,
			"subscriber_id", subID,
		)
	}

	return ch, unsubscribe
}

// Replay returns events from the Redis Stream for the given workspace
// where sequence > fromSequence.
func (r *RedisFanOut) Replay(workspaceID string, fromSequence uint64) ([]*dexdexv1.StreamWorkspaceEventsResponse, error) {
	ctx := context.Background()

	// Read all messages from the stream.
	msgs, err := r.client.XRange(ctx, r.streamKey(workspaceID), "-", "+").Result()
	if err != nil {
		return nil, fmt.Errorf("redis XRANGE failed: %w", err)
	}

	if len(msgs) == 0 {
		return nil, nil
	}

	var result []*dexdexv1.StreamWorkspaceEventsResponse
	var earliestSeq uint64

	for i, msg := range msgs {
		seqStr, ok := msg.Values["sequence"].(string)
		if !ok {
			continue
		}
		seq, parseErr := strconv.ParseUint(seqStr, 10, 64)
		if parseErr != nil {
			continue
		}

		if i == 0 {
			earliestSeq = seq
		}

		if seq <= fromSequence {
			continue
		}

		dataStr, ok := msg.Values["data"].(string)
		if !ok {
			continue
		}

		var evt redisFanOutEvent
		if err := json.Unmarshal([]byte(dataStr), &evt); err != nil {
			continue
		}

		resp := &dexdexv1.StreamWorkspaceEventsResponse{}
		if err := protojson.Unmarshal([]byte(evt.PayloadJSON), resp); err != nil {
			continue
		}

		result = append(result, resp)
	}

	// Check if requested sequence is older than retention.
	if fromSequence > 0 && fromSequence+1 < earliestSeq {
		return nil, &ReplayOutOfRangeError{
			EarliestAvailableSequence: earliestSeq,
			RequestedFromSequence:     fromSequence,
		}
	}

	r.logger.Debug("redis replay completed",
		"workspace_id", workspaceID,
		"from_sequence", fromSequence,
		"replayed_count", len(result),
	)

	return result, nil
}
