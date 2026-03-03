package broker

import (
	"context"
	"errors"
	"fmt"

	v1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/repository"
)

type CursorOutOfRangeError struct {
	EarliestAvailableSequence uint64
	RequestedFromSequence     uint64
}

func (e *CursorOutOfRangeError) Error() string {
	if e == nil {
		return "cursor out of range"
	}
	return fmt.Sprintf("cursor out of range: requested=%d earliest=%d", e.RequestedFromSequence, e.EarliestAvailableSequence)
}

type Broker interface {
	Publish(context.Context, string, *v1.StreamWorkspaceEventsResponse) (*v1.StreamWorkspaceEventsResponse, error)
	Replay(context.Context, string, uint64, int) ([]*v1.StreamWorkspaceEventsResponse, uint64, error)
	Stream(context.Context, string, uint64, int, func(*v1.StreamWorkspaceEventsResponse) error) error
}

func replayWithCursorValidation(ctx context.Context, store *repository.Store, workspaceID string, fromSequence uint64, limit int) ([]*v1.StreamWorkspaceEventsResponse, uint64, error) {
	events, earliest, err := store.ListWorkspaceEvents(ctx, workspaceID, fromSequence, limit)
	if err != nil {
		return nil, 0, err
	}
	if earliest > 0 && fromSequence > 0 && fromSequence+1 < earliest {
		return nil, earliest, &CursorOutOfRangeError{
			EarliestAvailableSequence: earliest,
			RequestedFromSequence:     fromSequence,
		}
	}
	return events, earliest, nil
}

func IsCursorOutOfRange(err error) (*CursorOutOfRangeError, bool) {
	var outOfRange *CursorOutOfRangeError
	if errors.As(err, &outOfRange) {
		return outOfRange, true
	}
	return nil, false
}
