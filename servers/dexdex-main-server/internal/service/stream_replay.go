package service

import "math"

type StreamEventType uint8

const (
	StreamEventTypeTaskUpdated StreamEventType = iota + 1
	StreamEventTypeSubTaskUpdated
	StreamEventTypeSessionOutput
	StreamEventTypeSessionStateChanged
	StreamEventTypePRUpdated
	StreamEventTypeReviewAssistUpdated
	StreamEventTypeInlineCommentUpdated
	StreamEventTypeNotificationCreated
)

type WorkspaceStreamEnvelope struct {
	WorkspaceID string
	Sequence    uint64
	EventType   StreamEventType
}

type CursorOutOfRange struct {
	EarliestAvailableSequence uint64
}

type StreamReplayErrorCode uint8

const (
	StreamReplayErrorCodeCursorOutOfRange StreamReplayErrorCode = iota + 1
	StreamReplayErrorCodeInvalidSequence
)

type StreamReplayError struct {
	Code   StreamReplayErrorCode
	Cursor *CursorOutOfRange
}

func (e *StreamReplayError) Error() string {
	if e == nil {
		return "stream replay error"
	}

	switch e.Code {
	case StreamReplayErrorCodeCursorOutOfRange:
		return "cursor out of range"
	case StreamReplayErrorCodeInvalidSequence:
		return "invalid sequence"
	default:
		return "unknown stream replay error"
	}
}

func ReplayWorkspaceEvents(
	events []WorkspaceStreamEnvelope,
	fromSequence *uint64,
	earliestAvailableSequence uint64,
) ([]WorkspaceStreamEnvelope, error) {
	if earliestAvailableSequence == 0 {
		return nil, &StreamReplayError{Code: StreamReplayErrorCodeInvalidSequence}
	}

	normalizedFromSequence := earliestAvailableSequence - 1
	if fromSequence != nil {
		normalizedFromSequence = *fromSequence
	}

	if normalizedFromSequence != math.MaxUint64 && normalizedFromSequence+1 < earliestAvailableSequence {
		return nil, &StreamReplayError{
			Code: StreamReplayErrorCodeCursorOutOfRange,
			Cursor: &CursorOutOfRange{
				EarliestAvailableSequence: earliestAvailableSequence,
			},
		}
	}

	replayed := make([]WorkspaceStreamEnvelope, 0, len(events))
	var previousSequence uint64
	for index, event := range events {
		if event.Sequence == 0 {
			return nil, &StreamReplayError{Code: StreamReplayErrorCodeInvalidSequence}
		}

		if index > 0 && event.Sequence <= previousSequence {
			return nil, &StreamReplayError{Code: StreamReplayErrorCodeInvalidSequence}
		}
		previousSequence = event.Sequence

		if event.Sequence > normalizedFromSequence {
			replayed = append(replayed, event)
		}
	}

	return replayed, nil
}
