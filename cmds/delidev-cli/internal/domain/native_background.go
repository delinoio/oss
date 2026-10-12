// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strconv"
	"strings"
)

const MaxNativeBackgroundTerminals = 16
const MaxNativeBackgroundOutput = 16 << 10
const MaxNativeBackgroundActions = 128

type NativeBackgroundOperation string

const (
	NativeBackgroundList      NativeBackgroundOperation = "list"
	NativeBackgroundTerminate NativeBackgroundOperation = "terminate"
	NativeBackgroundClean     NativeBackgroundOperation = "clean"
)

type NativeBackgroundState string

const (
	NativeBackgroundRejected  NativeBackgroundState = "rejected"
	NativeBackgroundCanceled  NativeBackgroundState = "canceled"
	NativeBackgroundQueued    NativeBackgroundState = "queued"
	NativeBackgroundClaimed   NativeBackgroundState = "claimed"
	NativeBackgroundObserved  NativeBackgroundState = "observed"
	NativeBackgroundUncertain NativeBackgroundState = "uncertain"
)

type NativeBackgroundTerminal struct {
	ItemID          string `json:"item_id"`
	ProcessID       string `json:"process_id"`
	Command         string `json:"command"`
	Cwd             string `json:"cwd"`
	Output          string `json:"output"`
	OutputTruncated bool   `json:"output_truncated"`
	ExitCode        *int32 `json:"exit_code,omitempty"`
	Present         bool   `json:"present"`
}

func NativeBackgroundProcess(id string) bool {
	n, e := strconv.ParseInt(id, 10, 32)
	return e == nil && strconv.FormatInt(n, 10) == id
}
func (t NativeBackgroundTerminal) Validate() error {
	if !NativeBackgroundProcess(t.ProcessID) || Text(t.ItemID, "native background item", 1024, true) != nil || Text(t.Command, "native background command", 8<<10, true) != nil || Text(t.Cwd, "native background cwd", 4<<10, true) != nil || Text(t.Output, "native background output", MaxNativeBackgroundOutput, false) != nil {
		return NativeBackgroundRecovery()
	}
	return nil
}

type NativeBackgroundInventory struct {
	Version         uint32                     `json:"version"`
	JobID           ID                         `json:"job_id"`
	ExecutionID     ID                         `json:"execution_id"`
	NativeThreadID  NativeIdentity             `json:"native_thread_id"`
	Sequence        uint64                     `json:"sequence,string"`
	Terminals       []NativeBackgroundTerminal `json:"terminals"`
	OwnerLost       bool                       `json:"owner_lost"`
	Actions         uint32                     `json:"actions"`
	PendingActionID ID                         `json:"pending_action_id,omitempty"`
	// Native absence is not operating-system exit or joined process cleanup.
	NativeEmpty bool `json:"native_empty"`
}

func (i NativeBackgroundInventory) Validate() error {
	if i.Version != 1 || UniqueIDs([]ID{i.JobID, i.ExecutionID}) != nil || i.NativeThreadID.Validate(Codex, NativeThreadIdentity) != nil || i.Sequence == 0 || len(i.Terminals) > MaxNativeBackgroundTerminals || i.Actions > MaxNativeBackgroundActions || i.PendingActionID != "" && i.PendingActionID.Validate() != nil {
		return NativeBackgroundRecovery()
	}
	items, processes := map[string]bool{}, map[string]bool{}
	present := false
	for _, t := range i.Terminals {
		if t.Validate() != nil || items[t.ItemID] || processes[t.ProcessID] {
			return NativeBackgroundRecovery()
		}
		items[t.ItemID], processes[t.ProcessID] = true, true
		present = present || t.Present
	}
	raw, _ := json.Marshal(i)
	if len(raw) > 512<<10 {
		return NativeBackgroundRecovery()
	}
	if i.NativeEmpty == present {
		return NativeBackgroundRecovery()
	}
	return nil
}

type NativeActionType string

const NativeBackgroundActionType NativeActionType = "native-background"

type NativeBackgroundAction struct {
	Version           uint32                       `json:"version"`
	ActionType        NativeActionType             `json:"action_type"`
	ActionID          ID                           `json:"action_id"`
	SessionID         ID                           `json:"session_id"`
	JobID             ID                           `json:"job_id"`
	ExecutionID       ID                           `json:"execution_id"`
	NativeThreadID    NativeIdentity               `json:"native_thread_id"`
	InventorySequence uint64                       `json:"inventory_sequence,string"`
	Operation         NativeBackgroundOperation    `json:"operation"`
	ItemID            string                       `json:"item_id,omitempty"`
	ProcessID         string                       `json:"process_id,omitempty"`
	Targets           []NativeBackgroundTerminal   `json:"targets"`
	Actor             Principal                    `json:"actor"`
	State             NativeBackgroundState        `json:"state"`
	Claim             *NativeShellClaim            `json:"claim,omitempty"`
	Observation       *NativeBackgroundObservation `json:"observation,omitempty"`
}
type NativeBackgroundDelivery string

const (
	NativeBackgroundNotSent      NativeBackgroundDelivery = "not-sent"
	NativeBackgroundSendUnknown  NativeBackgroundDelivery = "uncertain"
	NativeBackgroundAcknowledged NativeBackgroundDelivery = "acknowledged"
)

type NativeBackgroundObservation struct {
	Delivery NativeBackgroundDelivery `json:"delivery"`
	ActionID ID                       `json:"action_id"`
	// Acknowledgment never proves exit, OS cleanup, or native rejection.
	Acknowledged bool  `json:"acknowledged"`
	Terminated   *bool `json:"terminated,omitempty"`
	NativeEmpty  bool  `json:"native_empty"`
	ProblemCode  Code  `json:"problem_code,omitempty"`
}

func (a NativeBackgroundAction) Validate() error {
	if a.Version != 1 || a.ActionType != NativeBackgroundActionType || UniqueIDs([]ID{a.ActionID, a.SessionID, a.JobID, a.ExecutionID}) != nil || a.NativeThreadID.Validate(Codex, NativeThreadIdentity) != nil || a.InventorySequence == 0 || len(a.Targets) > MaxNativeBackgroundTerminals || (a.Actor.Type != OwnerDevice && a.Actor.Type != ClientDevice || a.Actor.MachineID != "" || a.Actor.Type == ClientDevice && a.Actor.DeviceID.Validate() != nil || a.Actor.DeviceID != "" && a.Actor.DeviceID.Validate() != nil) {
		return NativeBackgroundRecovery()
	}
	switch a.Operation {
	case NativeBackgroundList:
		if a.ItemID != "" || a.ProcessID != "" || len(a.Targets) != 0 {
			return NativeBackgroundRecovery()
		}
	case NativeBackgroundTerminate:
		if len(a.Targets) != 1 || a.Targets[0].ItemID != a.ItemID || a.Targets[0].ProcessID != a.ProcessID {
			return NativeBackgroundRecovery()
		}
	case NativeBackgroundClean:
		if a.ItemID != "" || a.ProcessID != "" || len(a.Targets) == 0 {
			return NativeBackgroundRecovery()
		}
	default:
		return NativeBackgroundRecovery()
	}
	seen := map[string]bool{}
	for _, t := range a.Targets {
		if t.Validate() != nil || !t.Present || t.Output != "" || t.OutputTruncated || t.ExitCode != nil || seen[t.ProcessID] {
			return NativeBackgroundRecovery()
		}
		seen[t.ProcessID] = true
	}
	if a.Claim != nil && UniqueIDs([]ID{a.Claim.RequestID, a.Claim.MachineID, a.Claim.InstanceID, a.Claim.DeviceID}) != nil {
		return NativeBackgroundRecovery()
	}
	switch a.State {
	case NativeBackgroundQueued, NativeBackgroundCanceled:
		if a.Claim != nil || a.Observation != nil {
			return NativeBackgroundRecovery()
		}
	case NativeBackgroundClaimed:
		if a.Claim == nil || a.Observation != nil {
			return NativeBackgroundRecovery()
		}
	case NativeBackgroundObserved, NativeBackgroundUncertain, NativeBackgroundRejected:
		if a.Claim == nil || a.Observation == nil || a.Observation.ActionID != a.ActionID {
			return NativeBackgroundRecovery()
		}
		if a.Observation.Delivery != NativeBackgroundNotSent && a.Observation.Delivery != NativeBackgroundSendUnknown && a.Observation.Delivery != NativeBackgroundAcknowledged {
			return NativeBackgroundRecovery()
		}
		if a.Observation.Acknowledged != (a.Observation.Delivery == NativeBackgroundAcknowledged) {
			return NativeBackgroundRecovery()
		}
		if a.State == NativeBackgroundObserved && (!a.Observation.Acknowledged || a.Observation.ProblemCode != "") {
			return NativeBackgroundRecovery()
		}
		if a.State == NativeBackgroundRejected && (a.Observation.Delivery != NativeBackgroundNotSent || a.Observation.ProblemCode == "") {
			return NativeBackgroundRecovery()
		}
		if a.Observation.Terminated != nil && (a.Operation != NativeBackgroundTerminate || !a.Observation.Acknowledged) {
			return NativeBackgroundRecovery()
		}
		if a.State == NativeBackgroundUncertain && a.Observation.ProblemCode == "" {
			return NativeBackgroundRecovery()
		}
	default:
		return NativeBackgroundRecovery()
	}
	return nil
}

type NativeBackgroundPublication struct {
	Inventory NativeBackgroundInventory    `json:"inventory"`
	Action    *NativeBackgroundObservation `json:"action,omitempty"`
}

func NativeBackgroundRecovery() *Error {
	return Fail(RecoveryRequired, "The original native background terminal scope requires reconciliation.", "Read retained original observations; never resend a claimed control or adopt another process.")
}
func SameNativeBackgroundTarget(a, b NativeBackgroundTerminal) bool {
	return a.ItemID == b.ItemID && a.ProcessID == b.ProcessID && a.Command == b.Command && a.Cwd == b.Cwd
}
func CheckNativeBackgroundInventory(previous, next NativeBackgroundInventory) error {
	if previous.Validate() != nil || next.Validate() != nil || previous.JobID != next.JobID || previous.ExecutionID != next.ExecutionID || previous.NativeThreadID != next.NativeThreadID || previous.OwnerLost && !next.OwnerLost || next.Sequence != previous.Sequence+1 {
		return NativeBackgroundRecovery()
	}
	for _, p := range previous.Terminals {
		found := false
		for _, n := range next.Terminals {
			if n.ItemID == p.ItemID {
				found = true
				if !SameNativeBackgroundTarget(p, n) || !p.Present && n.Present || p.ExitCode != nil && (n.ExitCode == nil || *p.ExitCode != *n.ExitCode) || !strings.HasPrefix(n.Output, p.Output) || p.OutputTruncated && !n.OutputTruncated {
					return NativeBackgroundRecovery()
				}
			}
		}
		if !found {
			return NativeBackgroundRecovery()
		}
	}
	return nil
}
