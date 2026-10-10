// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"strings"
	"testing"
)

func TestDynamicReplyJournalRejectsRetainedIntentAndScopeDrift(t *testing.T) {
	c, _ := codexTerminalStatusFixture(t)
	c.publisher.release = func() error { return nil }
	c.publisher.state.ServerID = domain.NewID()
	c.publisher.state.DeviceID = domain.NewID()
	c.publisher.state.Revision = 1
	c.publisher.state.AssignmentDigest = strings.Repeat("b", 64)
	recorder := dynamicUnavailableRecorder(c.publisher)
	state := codex.DynamicReplyState{RequestKey: "n:7", ArrivalID: domain.NewID(), ThreadID: c.thread, TurnID: c.turn, CallID: "dynamic-call", RequestDigest: strings.Repeat("a", 64), Delivery: codex.DynamicReplyIntent}
	if err := recorder(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if err := recorder(context.Background(), state); err == nil {
		t.Fatal("retained intent acquired replay authority")
	}
	state.Delivery = codex.DynamicReplyTransmitted
	if err := recorder(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	state.Resolved = true
	if err := recorder(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	state.ArrivalID = domain.NewID()
	if err := recorder(context.Background(), state); err == nil {
		t.Fatal("replaced original arrival adopted journal")
	}
}
