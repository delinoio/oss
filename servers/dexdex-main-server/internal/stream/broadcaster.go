package stream

import (
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

// EventBroadcaster is the interface for workspace-scoped event broadcast, subscribe,
// and replay. Both in-process FanOut and distributed RedisFanOut implement this.
type EventBroadcaster interface {
	Publish(workspaceID string, eventType dexdexv1.StreamEventType, payload isStreamWorkspaceEventsResponsePayload)
	Subscribe(workspaceID string) (ch <-chan *dexdexv1.StreamWorkspaceEventsResponse, unsubscribe func())
	Replay(workspaceID string, fromSequence uint64) ([]*dexdexv1.StreamWorkspaceEventsResponse, error)
}
