// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
)

func dynamicIdentityFixture() DynamicToolIdentity {
	return DynamicToolIdentity{Tool: "fixture_tool", ArgumentsDigest: strings.Repeat("a", 64)}
}
func TestDynamicFactsRemainIndependentAndOnceOnly(t *testing.T) {
	id := NewID()
	identity := dynamicIdentityFixture()
	request := &DynamicToolRequest{Token: NewID(), IDKind: InteractionNumberID, IDValue: "9223372036854775807", Identity: identity}
	started := DynamicToolUpdate{ID: id, NativeID: "dynamic-call", Stage: DynamicStarted, Snapshot: &DynamicToolSnapshot{Identity: identity, Status: "inProgress"}}
	state, err := ApplyDynamicTool(nil, started)
	if err != nil {
		t.Fatal(err)
	}
	requested := DynamicToolUpdate{ID: id, NativeID: started.NativeID, Stage: DynamicRequested, Request: request}
	state, err = ApplyDynamicTool(&state, requested)
	if err != nil {
		t.Fatal(err)
	}
	reply := DynamicToolUpdate{ID: id, NativeID: started.NativeID, Stage: DynamicReplied, Reply: &DynamicToolReply{Token: request.Token, ClaimID: NewID(), Delivery: DynamicTransmitted, Unavailable: true}}
	state, err = ApplyDynamicTool(&state, reply)
	if err != nil || state.Settled() || state.Resolved || state.Snapshot.Status != "inProgress" {
		t.Fatal("negative reply settled unrelated facts", err)
	}
	if _, err := ApplyDynamicTool(&state, reply); err == nil {
		t.Fatal("reply repeated")
	}
	success := false
	completed := started
	completed.Stage = DynamicCompleted
	completed.Snapshot = &DynamicToolSnapshot{Identity: identity, Status: "completed", Success: &success}
	state, err = ApplyDynamicTool(&state, completed)
	if err != nil || state.Settled() || state.Resolved || state.Snapshot.Success == nil || *state.Snapshot.Success {
		t.Fatal("item completion invented native resolution", err)
	}
	resolved := DynamicToolUpdate{ID: id, NativeID: started.NativeID, Stage: DynamicResolved, Token: request.Token}
	state, err = ApplyDynamicTool(&state, resolved)
	if err != nil || !state.Settled() {
		t.Fatal(err)
	}
	for _, replay := range []DynamicToolUpdate{reply, requested, completed, resolved} {
		if _, err := ApplyDynamicTool(&state, replay); err == nil {
			t.Fatal("settled original replay gained authority")
		}
	}
}
func TestDynamicUncertainDeliveryCannotBeOverwrittenByResolution(t *testing.T) {
	identity := dynamicIdentityFixture()
	request := &DynamicToolRequest{Token: NewID(), IDKind: InteractionTextID, IDValue: "7", Identity: identity}
	state := DynamicTool{Snapshot: &DynamicToolSnapshot{Identity: identity, Status: "completed"}, Request: request, Reply: &DynamicToolReply{Token: request.Token, ClaimID: NewID(), Delivery: DynamicUncertain, Unavailable: true}}
	next, err := ApplyDynamicTool(&state, DynamicToolUpdate{ID: NewID(), NativeID: "call", Stage: DynamicResolved, Token: request.Token})
	if err != nil || !next.Resolved || next.Settled() || next.Reply.Delivery != DynamicUncertain {
		t.Fatal("resolution erased uncertain attempt", err)
	}
	request.IDKind = InteractionNumberID
	request.IDValue = "9223372036854775808"
	if request.Validate() == nil {
		t.Fatal("out-of-range numeric ID accepted")
	}
}
