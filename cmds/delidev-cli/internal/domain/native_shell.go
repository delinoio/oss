// SPDX-License-Identifier: Apache-2.0
package domain

import "strings"

const NativeShellDisclosure = "This native command runs outside the thread sandbox with full access on the original Agent Worker."
const MaxNativeShellOutput = 256 << 10

type NativeShellState string

const (
	NativeShellQueued    NativeShellState = "queued"
	NativeShellClaimed   NativeShellState = "claimed"
	NativeShellRunning   NativeShellState = "running"
	NativeShellExited    NativeShellState = "exited"
	NativeShellCanceled  NativeShellState = "canceled"
	NativeShellUncertain NativeShellState = "uncertain"
)

type NativeShellCommand struct {
	Command    string    `json:"command"`
	FullAccess bool      `json:"full_access"`
	TimeoutMS  *uint64   `json:"timeout_ms,omitempty"`
	Actor      Principal `json:"actor"`
}

func (c NativeShellCommand) Validate() error {
	if !c.FullAccess || (c.Actor.Type != OwnerDevice && c.Actor.Type != ClientDevice || c.Actor.MachineID != "" || c.Actor.Type == ClientDevice && c.Actor.DeviceID.Validate() != nil || c.Actor.DeviceID != "" && c.Actor.DeviceID.Validate() != nil) || Text(c.Command, "native shell command", 64<<10, true) != nil || strings.TrimSpace(c.Command) == "" || c.TimeoutMS != nil && *c.TimeoutMS > 3600000 {
		return Fail(InvalidArgument, "An explicit full-access native shell command is required.", "Confirm the exact command and original session; the maximum timeout is one hour.")
	}
	return nil
}

// Native item/turn IDs identify this native action, never an operating-system
// PID. They cannot adopt, signal or close product terminals or other processes.
type NativeShellObservation struct {
	ActionID         ID               `json:"action_id"`
	SessionID        ID               `json:"session_id"`
	ExecutionID      ID               `json:"execution_id"`
	NativeThreadID   NativeIdentity   `json:"native_thread_id"`
	NativeTurnID     NativeIdentity   `json:"native_turn_id,omitempty"`
	NativeItemID     string           `json:"native_item_id,omitempty"`
	NativeProcessID  *string          `json:"native_process_id,omitempty"`
	State            NativeShellState `json:"state"`
	Sequence         uint64           `json:"sequence,string"`
	Acknowledged     bool             `json:"acknowledged"`
	Output           string           `json:"output"`
	AggregatedOutput *string          `json:"aggregated_output,omitempty"`
	ExitCode         *int32           `json:"exit_code,omitempty"`
	DurationMS       *int64           `json:"duration_ms,omitempty"`
	CleanupVerified  bool             `json:"cleanup_verified"`
}

func (o NativeShellObservation) Validate() error {
	if UniqueIDs([]ID{o.ActionID, o.SessionID, o.ExecutionID}) != nil || o.NativeThreadID.Validate(Codex, NativeThreadIdentity) != nil || o.Sequence == 0 || Text(o.Output, "native shell output", MaxNativeShellOutput, false) != nil || o.AggregatedOutput != nil && Text(*o.AggregatedOutput, "native shell aggregate output", MaxNativeShellOutput, false) != nil || o.DurationMS != nil && *o.DurationMS < 0 || o.NativeProcessID != nil && Text(*o.NativeProcessID, "native shell process observation", 1024, true) != nil {
		return NativeShellRecovery()
	}
	if o.NativeTurnID != "" && o.NativeTurnID.Validate(Codex, NativeTurnIdentity) != nil || Text(o.NativeItemID, "native shell item", 1024, false) != nil {
		return NativeShellRecovery()
	}
	switch o.State {
	case NativeShellClaimed:
		if o.NativeTurnID != "" || o.NativeItemID != "" || o.NativeProcessID != nil || o.Output != "" || o.AggregatedOutput != nil || o.ExitCode != nil || o.DurationMS != nil || o.CleanupVerified {
			return NativeShellRecovery()
		}
	case NativeShellRunning:
		if !o.Acknowledged || o.NativeTurnID == "" || o.NativeItemID == "" || o.AggregatedOutput != nil || o.ExitCode != nil || o.DurationMS != nil || o.CleanupVerified {
			return NativeShellRecovery()
		}
	case NativeShellExited:
		if !o.Acknowledged || o.NativeTurnID == "" || o.NativeItemID == "" || o.ExitCode == nil || o.DurationMS == nil || o.AggregatedOutput == nil {
			return NativeShellRecovery()
		}
	case NativeShellCanceled:
		if !o.Acknowledged || o.NativeTurnID == "" {
			return NativeShellRecovery()
		}
		if o.ExitCode != nil && (o.NativeTurnID == "" || o.NativeItemID == "" || !o.Acknowledged) {
			return NativeShellRecovery()
		}
	case NativeShellUncertain:
		if o.CleanupVerified {
			return NativeShellRecovery()
		}
	default:
		return NativeShellRecovery()
	}
	return nil
}
func NativeShellRecovery() *Error {
	return Fail(RecoveryRequired, "The original native shell action requires reconciliation.", "Read its retained status; never resend the command or adopt another process.")
}

type NativeShellRef struct {
	JobID            ID     `json:"job_id"`
	ActionID         ID     `json:"action_id"`
	ExecutionID      ID     `json:"execution_id"`
	CheckpointDigest string `json:"checkpoint_digest"`
	NativeDigest     string `json:"native_digest"`
}

func (r NativeShellRef) Validate() error {
	if UniqueIDs([]ID{r.JobID, r.ActionID, r.ExecutionID}) != nil || !validCompactionDigest(r.CheckpointDigest) || !validCompactionDigest(r.NativeDigest) {
		return NativeShellRecovery()
	}
	return nil
}

type NativeShellResult struct {
	Version     uint32                 `json:"version"`
	Observation NativeShellObservation `json:"observation"`
	History     *NativeShellRef        `json:"history,omitempty"`
}

func (r NativeShellResult) Validate() error {
	if r.Version != 1 || r.Observation.Validate() != nil || !r.Observation.CleanupVerified || r.Observation.State != NativeShellExited && r.Observation.State != NativeShellCanceled {
		return NativeShellRecovery()
	}
	if r.History != nil && (r.History.Validate() != nil || r.History.ActionID != r.Observation.ActionID || r.History.ExecutionID != r.Observation.ExecutionID) {
		return NativeShellRecovery()
	}
	if r.Observation.State == NativeShellExited && r.History == nil {
		return NativeShellRecovery()
	}
	return nil
}

type NativeShellClaim struct {
	RequestID  ID `json:"request_id"`
	MachineID  ID `json:"machine_id"`
	InstanceID ID `json:"instance_id"`
	DeviceID   ID `json:"device_id"`
}
type NativeShellAction struct {
	Version     uint32                  `json:"version"`
	ActionID    ID                      `json:"action_id"`
	JobID       ID                      `json:"job_id"`
	SessionID   ID                      `json:"session_id"`
	ExecutionID ID                      `json:"execution_id"`
	Command     NativeShellCommand      `json:"command"`
	Disclosure  string                  `json:"disclosure"`
	State       NativeShellState        `json:"state"`
	Claim       *NativeShellClaim       `json:"claim,omitempty"`
	Observation *NativeShellObservation `json:"observation,omitempty"`
}

func (a NativeShellAction) Validate() error {
	if a.Version != 1 || UniqueIDs([]ID{a.ActionID, a.JobID, a.SessionID, a.ExecutionID}) != nil || a.Command.Validate() != nil || a.Disclosure != NativeShellDisclosure {
		return NativeShellRecovery()
	}
	if a.Observation != nil && (a.Observation.Validate() != nil || a.Observation.ActionID != a.ActionID || a.Observation.SessionID != a.SessionID || a.Observation.ExecutionID != a.ExecutionID || a.State != a.Observation.State) {
		return NativeShellRecovery()
	}
	if a.Claim != nil && UniqueIDs([]ID{a.Claim.RequestID, a.Claim.MachineID, a.Claim.InstanceID, a.Claim.DeviceID}) != nil {
		return NativeShellRecovery()
	}
	switch a.State {
	case NativeShellQueued:
		if a.Claim != nil || a.Observation != nil {
			return NativeShellRecovery()
		}
	case NativeShellCanceled:
		if (a.Claim == nil) != (a.Observation == nil) {
			return NativeShellRecovery()
		}
	case NativeShellClaimed, NativeShellRunning, NativeShellExited:
		if a.Claim == nil || a.Observation == nil {
			return NativeShellRecovery()
		}
	case NativeShellUncertain:
		if a.Observation != nil && a.Claim == nil {
			return NativeShellRecovery()
		}
	default:
		return NativeShellRecovery()
	}
	return nil
}

func CheckNativeShellTransition(previous, next NativeShellObservation) error {
	if previous.Validate() != nil || next.Validate() != nil || next.ActionID != previous.ActionID || next.SessionID != previous.SessionID || next.ExecutionID != previous.ExecutionID || next.NativeThreadID != previous.NativeThreadID || next.Sequence != previous.Sequence+1 || previous.CleanupVerified || !strings.HasPrefix(next.Output, previous.Output) || previous.Acknowledged && !next.Acknowledged {
		return NativeShellRecovery()
	}
	if previous.State != NativeShellClaimed && (previous.NativeProcessID == nil) != (next.NativeProcessID == nil) {
		return NativeShellRecovery()
	}
	if previous.NativeTurnID != "" && next.NativeTurnID != previous.NativeTurnID || previous.NativeItemID != "" && next.NativeItemID != previous.NativeItemID || previous.NativeProcessID != nil && (next.NativeProcessID == nil || *previous.NativeProcessID != *next.NativeProcessID) {
		return NativeShellRecovery()
	}
	if previous.ExitCode != nil && (next.ExitCode == nil || *previous.ExitCode != *next.ExitCode) || previous.DurationMS != nil && (next.DurationMS == nil || *previous.DurationMS != *next.DurationMS) || previous.AggregatedOutput != nil && (next.AggregatedOutput == nil || *previous.AggregatedOutput != *next.AggregatedOutput) {
		return NativeShellRecovery()
	}
	if previous.State == NativeShellClaimed && next.State != NativeShellRunning && next.State != NativeShellCanceled || previous.State == NativeShellRunning && next.State != NativeShellRunning && next.State != NativeShellExited && next.State != NativeShellCanceled {
		return NativeShellRecovery()
	}
	if previous.State == NativeShellExited && next.State != NativeShellExited && next.State != NativeShellCanceled || previous.State == NativeShellCanceled && next.State != NativeShellCanceled || previous.State == NativeShellUncertain {
		return NativeShellRecovery()
	}
	return nil
}

func (s Session) NativeShellForExecution(execution ID) *NativeShellRef {
	if s.NativeShell != nil && s.NativeShell.ExecutionID == execution {
		return s.NativeShell
	}
	return nil
}
