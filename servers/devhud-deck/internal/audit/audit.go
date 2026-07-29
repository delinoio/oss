// Package audit defines the closed Deck security/business audit vocabulary.
// Its records cannot carry repository, query, title, login, token, or free-form
// metadata.
package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type EventType int16

const (
	EventViewCreated EventType = iota + 1
	EventViewUpdated
	EventViewDeleted
	EventDeviceRegistered
	EventDeviceUpdated
	EventDeviceUnregistered
	EventNotificationPreferenceUpdated
	EventFeatureDeletionAccepted
	EventAccountLifecycleDeletion
	EventOrganizationLifecycleDeletion
	EventAuthorizationDenied
	EventSnapshotReplaced
)

type ResourceType int16

const (
	ResourceView ResourceType = iota + 1
	ResourceDevice
	ResourceNotification
	ResourceOwner
	ResourceSnapshot
)

type Outcome int16

const (
	OutcomeSuccess Outcome = iota + 1
	OutcomeDenied
	OutcomeNoop
)

type Event struct {
	ID             uuid.UUID
	Type           EventType
	ActorPseudonym string
	OwnerScope     int16
	TargetHash     []byte
	ResourceType   ResourceType
	ResourceID     uuid.UUID
	Outcome        Outcome
	OccurredAt     time.Time
}

type Recorder interface {
	Record(context.Context, Event) error
}
