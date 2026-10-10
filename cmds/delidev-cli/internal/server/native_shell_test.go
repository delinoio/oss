// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestNativeShellProgressPreservesOriginalProcess(t *testing.T) {
	before := domain.NativeShellObservation{Version: 1, ActionID: domain.NewID(), NativeThreadID: domain.NewID(), Delivery: domain.NativeShellAcknowledged, Sequence: 1, Processes: []domain.NativeShellProcess{{ItemID: "original", Command: "pwd", Cwd: "/original", Status: domain.NativeShellRunning, Output: "/"}}}
	after := before
	after.Sequence++
	after.Processes = append([]domain.NativeShellProcess{}, before.Processes...)
	after.Processes[0].Output = "/original"
	if !nativeShellProgress(before, after) {
		t.Fatal("monotonic original output rejected")
	}
	after.Processes[0].ItemID = "replacement"
	if nativeShellProgress(before, after) {
		t.Fatal("replacement process accepted")
	}
	after.Processes[0] = before.Processes[0]
	after.Processes[0].Output = "changed"
	if nativeShellProgress(before, after) {
		t.Fatal("changed original output accepted")
	}
	after.Processes = nil
	if nativeShellProgress(before, after) {
		t.Fatal("original process erased")
	}
}
func TestNativeShellCannotBeAuthorizedByMissingPrincipal(t *testing.T) {
	if nativeShellActor(context.Background()) == nil {
		t.Fatal("unauthenticated shell authority accepted")
	}
}
