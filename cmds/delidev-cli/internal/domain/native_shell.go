// SPDX-License-Identifier: Apache-2.0
package domain

import "slices"

type NativeShellCommand struct {
	Command             string  `json:"command"`
	TimeoutMS           *uint64 `json:"timeout_ms,omitempty"`
	FullAccessConfirmed bool    `json:"full_access_confirmed"`
}

func (c NativeShellCommand) Validate() error {
	if !c.FullAccessConfirmed || Text(c.Command, "native shell command", 64<<10, true) != nil || c.TimeoutMS != nil && *c.TimeoutMS > 3600000 {
		return Fail(InvalidArgument, "An explicit full-access shell confirmation is required.", "Confirm that the command runs outside the native sandbox; use at most 64 KiB and a timeout of at most one hour.")
	}
	return nil
}

type NativeShellDelivery string

const (
	NativeShellAcknowledged      NativeShellDelivery = "acknowledged"
	NativeShellUncertainDelivery NativeShellDelivery = "uncertain"
	NativeShellRejected          NativeShellDelivery = "rejected"
)

type NativeShellStatus string

const (
	NativeShellRunning   NativeShellStatus = "inProgress"
	NativeShellCompleted NativeShellStatus = "completed"
	NativeShellFailed    NativeShellStatus = "failed"
	NativeShellDeclined  NativeShellStatus = "declined"
)

type NativeShellProcess struct {
	ItemID           string            `json:"item_id"`
	ProcessID        *string           `json:"process_id,omitempty"`
	Command          string            `json:"command"`
	Cwd              string            `json:"cwd"`
	Status           NativeShellStatus `json:"status"`
	Output           string            `json:"output"`
	AggregatedOutput *string           `json:"aggregated_output,omitempty"`
	ExitCode         *int32            `json:"exit_code,omitempty"`
}
type NativeShellObservation struct {
	Version         uint32               `json:"version"`
	ActionID        ID                   `json:"action_id"`
	NativeThreadID  ID                   `json:"native_thread_id"`
	NativeTurnID    ID                   `json:"native_turn_id,omitempty"`
	Delivery        NativeShellDelivery  `json:"delivery"`
	Terminal        bool                 `json:"terminal"`
	CleanupVerified bool                 `json:"cleanup_verified"`
	Processes       []NativeShellProcess `json:"processes"`
	Sequence        uint64               `json:"sequence"`
}

func (o NativeShellObservation) Validate() error {
	if o.Version != 1 || o.ActionID.Validate() != nil || o.NativeThreadID.Validate() != nil || o.NativeTurnID != "" && o.NativeTurnID.Validate() != nil || o.Sequence == 0 || o.Sequence > MaxExecutionEvents || !slices.Contains([]NativeShellDelivery{NativeShellAcknowledged, NativeShellUncertainDelivery, NativeShellRejected}, o.Delivery) || o.Processes == nil || len(o.Processes) > 128 || o.Terminal && (o.NativeTurnID == "" || len(o.Processes) == 0) || o.CleanupVerified && !o.Terminal {
		return NativeShellUncertain()
	}
	seen := map[string]bool{}
	total := 0
	for _, p := range o.Processes {
		if Text(p.ItemID, "native shell item", 1024, true) != nil || seen[p.ItemID] || p.ProcessID != nil && Text(*p.ProcessID, "native shell process", 1024, true) != nil || Text(p.Command, "native shell command", 64<<10, true) != nil || Text(p.Cwd, "native shell directory", 4096, true) != nil || Text(p.Output, "native shell output", 256<<10, false) != nil || !slices.Contains([]NativeShellStatus{NativeShellRunning, NativeShellCompleted, NativeShellFailed, NativeShellDeclined}, p.Status) || o.Terminal && p.Status == NativeShellRunning {
			return NativeShellUncertain()
		}
		if p.AggregatedOutput != nil && Text(*p.AggregatedOutput, "native shell aggregate", 256<<10, false) != nil {
			return NativeShellUncertain()
		}
		if p.AggregatedOutput != nil {
			total += len(*p.AggregatedOutput)
		}
		seen[p.ItemID] = true
		total += len(p.Output) + len(p.Command)
		if total > 512<<10 {
			return NativeShellUncertain()
		}
	}
	return nil
}
func NativeShellUncertain() *Error {
	return Fail(RecoveryRequired, "The original native shell action requires reconciliation.", "Read its original operation and retain uncertainty; do not resend the command or adopt unrelated processes.")
}
