// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func codexChild(parent string, source string) domain.SubagentObservation {
	return domain.SubagentObservation{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: parent, Status: domain.SubagentRunning, Source: domain.CodexCollaborationSource, SourceID: source}
}

func withNames(req *pb.PublishExecutionRequest, names ...*pb.ExecutionChildDisplayName) *pb.PublishExecutionRequest {
	req.ChildDisplayNames = names
	return req
}

func (f *publicationFixture) children(t *testing.T) map[string]*pb.Resource {
	t.Helper()
	resources := delidevv1connect.NewResourceServiceClient(http.DefaultClient, f.http.URL)
	rows, err := resources.ListResources(context.Background(), ownerRequest(f.service.Identity, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_SUBAGENT, SessionId: string(f.input.SessionID), PageSize: 50}}))
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]*pb.Resource{}
	for _, row := range rows.Msg.Resources {
		result[row.Id] = row
	}
	return result
}

func TestSubagentDisplayNamesAreAtomicReceiptBoundAndProjectedPrivately(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	first, second := codexChild(string(f.thread), "spawn-one"), codexChild(string(f.thread), "spawn-two")
	e := f.event(domain.ExecutionSubagentObserved, 3)
	e.Subagents = []domain.SubagentObservation{first, second}

	// Invalid entries reject the complete publication before any retention and
	// do not consume the sequence.
	long := strings.Repeat("가", 257)
	for name, names := range map[string][]*pb.ExecutionChildDisplayName{
		"duplicate": {{ChildId: string(first.ID), Name: "Atlas"}, {ChildId: string(first.ID), Name: "Nova"}},
		"foreign":   {{ChildId: string(domain.NewID()), Name: "Atlas"}},
		"native id": {{ChildId: first.NativeID, Name: "Atlas"}},
		"blank":     {{ChildId: string(first.ID), Name: "  "}},
		"too long":  {{ChildId: string(first.ID), Name: long}},
		"control":   {{ChildId: string(first.ID), Name: "a\nb"}},
		"nil":       {nil},
	} {
		if _, err := f.call(withNames(f.requestEvent(t, e), names...)); err == nil {
			t.Fatalf("%s display metadata was accepted", name)
		}
	}
	if len(f.children(t)) != 0 {
		t.Fatal("rejected display metadata partially published children")
	}
	other := f.event(domain.ExecutionInputAccepted, 3)
	if _, err := f.call(withNames(f.requestEvent(t, other), &pb.ExecutionChildDisplayName{ChildId: string(first.ID), Name: "Atlas"})); err == nil {
		t.Fatal("display metadata attached to a non-subagent event")
	}

	// Two children keep separate identities even when their names are equal.
	named := withNames(f.requestEvent(t, e), &pb.ExecutionChildDisplayName{ChildId: string(first.ID), Name: "Atlas"}, &pb.ExecutionChildDisplayName{ChildId: string(second.ID), Name: "Atlas"})
	if response, err := f.call(named); err != nil || response.Msg.Replayed {
		t.Fatal("valid display metadata publication failed", err)
	}
	if response, err := f.call(named); err != nil || !response.Msg.Replayed {
		t.Fatal("exact display metadata receipt did not replay", err)
	}
	// The receipt identity binds the exact metadata bytes.
	changed := withNames(f.requestEvent(t, e), &pb.ExecutionChildDisplayName{ChildId: string(first.ID), Name: "Nova"}, &pb.ExecutionChildDisplayName{ChildId: string(second.ID), Name: "Atlas"})
	changed.Mutation = named.Mutation
	if _, err := f.call(changed); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("changed display metadata reused the original receipt", err)
	}
	dropped := f.requestEvent(t, e)
	dropped.Mutation = named.Mutation
	if _, err := f.call(dropped); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("dropped display metadata reused the original receipt", err)
	}

	rows := f.children(t)
	if len(rows) != 2 || rows[string(first.ID)].DisplayName != "Atlas" || rows[string(second.ID)].DisplayName != "Atlas" {
		t.Fatal("equal names merged children or were not projected", len(rows))
	}
	for _, row := range rows {
		if strings.Contains(string(row.DocumentJson), "display_name") || strings.Contains(string(row.DocumentJson), "Atlas") {
			t.Fatal("private display name leaked into the closed schema-1 document")
		}
	}

	// A later observation without a name preserves the last valid one; a later
	// valid observation updates it.
	omitted := f.event(domain.ExecutionSubagentObserved, 4)
	first.SourceID = "later-source"
	omitted.Subagents = []domain.SubagentObservation{first}
	f.publish(t, omitted)
	if f.children(t)[string(first.ID)].DisplayName != "Atlas" {
		t.Fatal("omitted name erased the last valid name")
	}
	updated := f.event(domain.ExecutionSubagentObserved, 5)
	first.SourceID = "update-source"
	updated.Subagents = []domain.SubagentObservation{first}
	if _, err := f.call(withNames(f.requestEvent(t, updated), &pb.ExecutionChildDisplayName{ChildId: string(first.ID), Name: "Nova"})); err != nil {
		t.Fatal(err)
	}
	rows = f.children(t)
	if rows[string(first.ID)].DisplayName != "Nova" || rows[string(second.ID)].DisplayName != "Atlas" {
		t.Fatal("later valid name did not update only its child")
	}
}

func TestSubagentPublicationWithoutDisplayNamesKeepsHistoricalProjection(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	child := codexChild(string(f.thread), "spawn-one")
	e := f.event(domain.ExecutionSubagentObserved, 3)
	e.Subagents = []domain.SubagentObservation{child}
	request := f.publish(t, e)
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("nameless receipt did not replay", err)
	}
	row := f.children(t)[string(child.ID)]
	if row == nil || row.DisplayName != "" {
		t.Fatal("historical nameless record gained a display name")
	}
}
