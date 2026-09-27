package claude

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type NativeRunState string
type NativeTurnOrigin string

const (
	RunIdle                NativeRunState   = "idle"
	RunRunning             NativeRunState   = "running"
	RunRequiresAction      NativeRunState   = "requires_action"
	TaskNotificationOrigin NativeTurnOrigin = "task-notification"
)

// NativeRunObservation is the native loop's state at this exact stream
// position, not process cleanup, input acceptance, or permission to send again.
// Background membership and a result cannot substitute for the idle event.
type NativeRunObservation struct {
	State              NativeRunState
	KnownWork          NativeWorkObservation
	ContinuationFailed bool
}

func (b *ExecutionBinding) observeRunState(raw []byte) (*NativeRunObservation, error) {
	var value struct {
		Type    string         `json:"type"`
		Subtype string         `json:"subtype"`
		Session domain.ID      `json:"session_id"`
		UUID    string         `json:"uuid"`
		State   NativeRunState `json:"state"`
	}
	if decodeNativeObject(raw, &value) != nil || value.Subtype != "session_state_changed" || value.State == b.runState {
		return nil, lifecycleUncertain()
	}
	switch value.State {
	case RunRunning:
		if b.runState == RunIdle || (b.finished && b.runState == "") {
			// This binding owns one run. An autonomous wake after its idle
			// boundary requires the session-wide controller's retained ownership.
			return nil, lifecycleUncertain()
		}
	case RunRequiresAction:
		if b.runState != RunRunning || !b.initialized {
			return nil, lifecycleUncertain()
		}
	case RunIdle:
		if b.runState != RunRunning || (!b.finished && b.interruptResult == nil) || b.continuing ||
			(b.command != CommandCompleted && b.command != CommandCancelled) || len(b.content.active) != 0 || b.content.openTools != 0 {
			return nil, lifecycleUncertain()
		}
	default:
		return nil, lifecycleUncertain()
	}
	b.runState = value.State
	if b.logger != nil {
		b.logger.Debug("Claude Code native run state observed", "owner_id", b.owner, "state", value.State, "automatic_continuation", b.continuationSeen)
	}
	return &NativeRunObservation{State: value.State, KnownWork: b.knownWork(), ContinuationFailed: b.continuationFailed}, nil
}

func (b *ExecutionBinding) canStartContinuation() bool {
	if !b.finished || !b.accepted || b.continuing || b.command != CommandCompleted || b.runState != RunRunning || len(b.content.active) != 0 || b.content.openTools != 0 {
		return false
	}
	// A native init has no origin. Known task notification evidence permits
	// retaining this provisional turn; its result must independently declare
	// task-notification origin. Never guess which task produced that turn.
	return b.notifications != 0
}

func taskNotificationOrigin(raw json.RawMessage) bool {
	var value struct {
		Kind NativeTurnOrigin `json:"kind"`
	}
	return decodeNativeObject(raw, &value) == nil && value.Kind == TaskNotificationOrigin
}
