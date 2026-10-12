// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func nativeShellDomainObservation() NativeShellObservation {
	return NativeShellObservation{ActionID: NewID(), SessionID: NewID(), ExecutionID: NewID(), NativeThreadID: NativeIdentity(NewID()), Sequence: 1, State: NativeShellClaimed}
}
func TestNativeShellRequiresExplicitFullAccessAndClosedVariant(t *testing.T) {
	command := NativeShellCommand{Command: "  printf 'original'  ", FullAccess: true, Actor: Principal{Type: OwnerDevice}}
	if command.Validate() != nil {
		t.Fatal("valid exact command rejected")
	}
	command.FullAccess = false
	if command.Validate() == nil {
		t.Fatal("implicit full access accepted")
	}
	command.FullAccess = true
	timeout := uint64(3600001)
	command.TimeoutMS = &timeout
	if command.Validate() == nil {
		t.Fatal("unbounded command accepted")
	}
	for _, input := range []SessionCompactionInput{{Version: 5}, {Version: 2, NativeShell: &command}, {Version: 5, NativeShell: &command, Revert: &SessionRevertTarget{}}} {
		if input.Validate() == nil {
			t.Fatal("missing or mixed action discriminant accepted")
		}
	}
}
func TestNativeShellTransitionCannotAdoptProcessOrRewriteOutput(t *testing.T) {
	claimed := nativeShellDomainObservation()
	running := claimed
	running.Sequence++
	running.State = NativeShellRunning
	running.Acknowledged = true
	running.NativeTurnID = NativeIdentity(NewID())
	running.NativeItemID = "original-native-item"
	if CheckNativeShellTransition(claimed, running) != nil {
		t.Fatal("owned lifecycle rejected")
	}
	for _, scenario := range []string{"foreign", "sequence", "output", "turn", "item", "ack", "regress"} {
		t.Run(scenario, func(t *testing.T) {
			previous := running
			previous.Output = "prefix"
			next := previous
			next.Sequence++
			switch scenario {
			case "foreign":
				next.ActionID = NewID()
			case "sequence":
				next.Sequence++
			case "output":
				next.Output = "replacement"
			case "turn":
				next.NativeTurnID = NativeIdentity(NewID())
			case "item":
				next.NativeItemID = "other"
			case "ack":
				next.Acknowledged = false
			case "regress":
				next.State = NativeShellClaimed
			}
			if CheckNativeShellTransition(previous, next) == nil {
				t.Fatal("incompatible observation acquired action authority")
			}
		})
	}
}
func TestNativeShellReceiptCannotInventClaimOrCleanup(t *testing.T) {
	a := NativeShellAction{Version: 1, ActionID: NewID(), JobID: NewID(), SessionID: NewID(), ExecutionID: NewID(), Command: NativeShellCommand{Command: "true", FullAccess: true, Actor: Principal{Type: OwnerDevice}}, Disclosure: NativeShellDisclosure, State: NativeShellQueued}
	if a.Validate() != nil {
		t.Fatal("queued receipt rejected")
	}
	a.State = NativeShellExited
	if a.Validate() == nil {
		t.Fatal("exit without original claim/observation accepted")
	}
	o := nativeShellDomainObservation()
	o.CleanupVerified = true
	if o.Validate() == nil {
		t.Fatal("native acknowledgment proved cleanup")
	}
}

func TestNativeShellForExecutionPrefersLaterCompactionCheckpoint(t *testing.T) {
	execution := NewID()
	shell := &NativeShellRef{JobID: NewID(), ActionID: NewID(), ExecutionID: execution}
	compaction := &SessionCompactionRef{JobID: NewID(), ActionID: NewID(), ExecutionID: execution}
	session := Session{NativeShell: shell, Compaction: compaction}
	if got := session.NativeShellForExecution(execution); got != nil {
		t.Fatal("superseded native-shell checkpoint remained eligible after compaction")
	}
	compaction.ExecutionID = NewID()
	if got := (Session{NativeShell: shell, Compaction: compaction}).NativeShellForExecution(execution); got != shell {
		t.Fatal("compaction from another execution suppressed the native-shell checkpoint")
	}
}
