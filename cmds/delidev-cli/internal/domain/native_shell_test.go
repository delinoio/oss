// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestNativeShellRequiresExplicitFullAccessAndPreservesZeroTimeout(t *testing.T) {
	zero := uint64(0)
	command := NativeShellCommand{Command: "printf 'hello'", TimeoutMS: &zero}
	if command.Validate() == nil {
		t.Fatal("missing human confirmation accepted")
	}
	command.FullAccessConfirmed = true
	if command.Validate() != nil {
		t.Fatal("explicit zero timeout rejected")
	}
	tooLarge := uint64(3600001)
	command.TimeoutMS = &tooLarge
	if command.Validate() == nil {
		t.Fatal("unbounded timeout accepted")
	}
}
func TestNativeShellTerminalIsSeparateFromCleanup(t *testing.T) {
	o := NativeShellObservation{Version: 1, ActionID: NewID(), NativeThreadID: NewID(), Delivery: NativeShellAcknowledged, Processes: []NativeShellProcess{}, Sequence: 1}
	if o.Validate() != nil {
		t.Fatal("delivery observation rejected")
	}
	o.CleanupVerified = true
	if o.Validate() == nil {
		t.Fatal("cleanup without terminal accepted")
	}
	o.CleanupVerified = false
	o.Terminal = true
	o.NativeTurnID = NewID()
	o.Processes = []NativeShellProcess{{ItemID: "original", Command: "sleep 1", Cwd: "/workspace", Status: NativeShellRunning}}
	if o.Validate() == nil {
		t.Fatal("running original child counted terminal")
	}
	o.Processes[0].Status = NativeShellCompleted
	if o.Validate() != nil {
		t.Fatal("original terminal process rejected")
	}
	o.Sequence = MaxExecutionEvents + 1
	if o.Validate() == nil {
		t.Fatal("unsafe sequence accepted")
	}
}
