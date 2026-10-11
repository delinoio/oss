// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
)

func backgroundInventoryFixture() NativeBackgroundInventory {
	return NativeBackgroundInventory{Version: 1, JobID: NewID(), ExecutionID: NewID(), NativeThreadID: NativeIdentity(NewID()), Sequence: 1, Terminals: []NativeBackgroundTerminal{{ItemID: "native-one", ProcessID: "500", Command: "first", Cwd: "/original", Present: true}, {ItemID: "native-two", ProcessID: "501", Command: "second", Cwd: "/original", Present: true}}}
}
func TestNativeBackgroundInventoryCannotRebindOrResurrect(t *testing.T) {
	original := backgroundInventoryFixture()
	if original.Validate() != nil {
		t.Fatal("invalid original fixture")
	}
	for _, change := range []func(*NativeBackgroundInventory){func(i *NativeBackgroundInventory) { i.JobID = NewID() }, func(i *NativeBackgroundInventory) { i.NativeThreadID = NativeIdentity(NewID()) }, func(i *NativeBackgroundInventory) { i.Terminals[0].ProcessID = "999" }, func(i *NativeBackgroundInventory) { i.Terminals[0].Command = "replacement" }, func(i *NativeBackgroundInventory) { i.Terminals[0].Cwd = "/borrowed" }, func(i *NativeBackgroundInventory) { i.Terminals[1].ProcessID = i.Terminals[0].ProcessID }, func(i *NativeBackgroundInventory) { i.NativeEmpty = true }, func(i *NativeBackgroundInventory) {
		i.Terminals[0].Output = strings.Repeat("x", MaxNativeBackgroundOutput+1)
	}} {
		next := original
		next.Sequence++
		next.Terminals = append([]NativeBackgroundTerminal{}, original.Terminals...)
		change(&next)
		if CheckNativeBackgroundInventory(original, next) == nil {
			t.Fatal("changed original background identity accepted")
		}
	}
	absent := original
	absent.Sequence++
	absent.Terminals = append([]NativeBackgroundTerminal{}, original.Terminals...)
	absent.Terminals[0].Present = false
	if CheckNativeBackgroundInventory(original, absent) != nil {
		t.Fatal("native absence was rejected")
	}
	restored := absent
	restored.Sequence++
	restored.Terminals = append([]NativeBackgroundTerminal{}, absent.Terminals...)
	restored.Terminals[0].Present = true
	if CheckNativeBackgroundInventory(absent, restored) == nil {
		t.Fatal("stale native process resurrected")
	}
}
func TestNativeBackgroundActionAcknowledgmentCannotBecomeExitOrCleanup(t *testing.T) {
	i := backgroundInventoryFixture()
	a := NativeBackgroundAction{Version: 1, ActionType: NativeBackgroundActionType, ActionID: NewID(), SessionID: NewID(), JobID: i.JobID, ExecutionID: i.ExecutionID, NativeThreadID: i.NativeThreadID, InventorySequence: 1, Operation: NativeBackgroundTerminate, ItemID: i.Terminals[0].ItemID, ProcessID: i.Terminals[0].ProcessID, Targets: i.Terminals[:1], Actor: Principal{Type: OwnerDevice}, State: NativeBackgroundClaimed, Claim: &NativeShellClaim{RequestID: NewID(), MachineID: NewID(), InstanceID: NewID(), DeviceID: NewID()}}
	if a.Validate() != nil {
		t.Fatal("invalid claimed fixture")
	}
	yes := true
	a.State = NativeBackgroundObserved
	a.Observation = &NativeBackgroundObservation{ActionID: a.ActionID, Acknowledged: true, Delivery: NativeBackgroundAcknowledged, Terminated: &yes}
	if a.Validate() != nil || a.Targets[0].ExitCode != nil || a.Observation.NativeEmpty {
		t.Fatal("ACK fabricated native exit or cleanup")
	}
	a.Observation.Acknowledged = false
	if a.Validate() == nil {
		t.Fatal("terminated fact without acknowledgment accepted")
	}
	a.Operation = NativeBackgroundList
	if a.Validate() == nil {
		t.Fatal("mixed list/terminate request accepted")
	}
}
