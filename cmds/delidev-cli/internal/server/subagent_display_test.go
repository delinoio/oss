// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func displayChild(f *publicationFixture, source string) domain.SubagentObservation {
	return domain.SubagentObservation{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(f.thread), Status: domain.SubagentRunning, Source: domain.CodexHistorySource, SourceID: source}
}

func bindForDisplay(t *testing.T) *publicationFixture {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	return f
}

func retainedDisplayName(t *testing.T, f *publicationFixture, id domain.ID) *string {
	t.Helper()
	var name *string
	err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		row, err := tx.Get(domain.SubagentKind, id)
		if err != nil {
			return err
		}
		record, err := store.Decode[domain.SubagentRecord](row)
		name = record.DisplayName
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func TestSubagentDisplayNamesRetainProjectAndKeepDocumentUnchanged(t *testing.T) {
	f := bindForDisplay(t)
	named, plain := displayChild(f, "named"), displayChild(f, "plain")
	e := f.event(domain.ExecutionSubagentObserved, 3)
	e.Subagents = []domain.SubagentObservation{named, plain}
	e.SubagentNames = []domain.SubagentDisplayName{{ID: named.ID, Name: "Atlas"}}
	original := f.publish(t, e)
	if reply, err := f.call(original); err != nil || !reply.Msg.Replayed {
		t.Fatal("exact name metadata did not replay", err)
	}
	if name := retainedDisplayName(t, f, named.ID); name == nil || *name != "Atlas" {
		t.Fatal("name not retained privately")
	}
	if retainedDisplayName(t, f, plain.ID) != nil {
		t.Fatal("nameless child gained a name")
	}
	resources := delidevv1connect.NewResourceServiceClient(http.DefaultClient, f.http.URL)
	rows, err := resources.ListResources(context.Background(), ownerRequest(f.service.Identity, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_SUBAGENT, SessionId: string(f.input.SessionID), PageSize: 50}}))
	if err != nil || len(rows.Msg.Resources) != 2 {
		t.Fatal("child read failed", err)
	}
	seen := 0
	for _, resource := range rows.Msg.Resources {
		if bytes.Contains(resource.DocumentJson, []byte("display_name")) {
			t.Fatal("private name leaked into the closed child document")
		}
		switch resource.Id {
		case string(named.ID):
			seen++
			if resource.SubagentDisplay == nil || resource.SubagentDisplay.Name != "Atlas" || resource.SubagentDisplay.ChildId != string(named.ID) {
				t.Fatal("name was not projected through the Resource boundary")
			}
		case string(plain.ID):
			seen++
			if resource.SubagentDisplay != nil {
				t.Fatal("historical nameless child projected a name")
			}
		}
	}
	if seen != 2 {
		t.Fatal("children missing from read")
	}
}

func TestSubagentDisplayNameReplayWithChangedBytesIsRejected(t *testing.T) {
	f := bindForDisplay(t)
	child := displayChild(f, "named")
	e := f.event(domain.ExecutionSubagentObserved, 3)
	e.Subagents = []domain.SubagentObservation{child}
	e.SubagentNames = []domain.SubagentDisplayName{{ID: child.ID, Name: "Atlas"}}
	original := f.publish(t, e)
	changed := f.requestEvent(t, e)
	changed.Mutation = original.Mutation
	changed.SubagentNames[0].Name = "Nova"
	if _, err := f.call(changed); err == nil {
		t.Fatal("changed name bytes were accepted under the original receipt")
	}
	dropped := f.requestEvent(t, e)
	dropped.Mutation = original.Mutation
	dropped.SubagentNames = nil
	if _, err := f.call(dropped); err == nil {
		t.Fatal("dropped name metadata was accepted under the original receipt")
	}
	if name := retainedDisplayName(t, f, child.ID); name == nil || *name != "Atlas" {
		t.Fatal("rejected replay changed retention")
	}
}

func TestSubagentDisplayNamesRejectInvalidJoinsWithoutPartialPublication(t *testing.T) {
	long := strings.Repeat("가", domain.MaxSubagentDisplayNameRunes+1)
	cases := map[string]func(a, b domain.SubagentObservation) []domain.SubagentDisplayName{
		"foreign": func(a, b domain.SubagentObservation) []domain.SubagentDisplayName {
			return []domain.SubagentDisplayName{{ID: domain.NewID(), Name: "Atlas"}}
		},
		"duplicate": func(a, b domain.SubagentObservation) []domain.SubagentDisplayName {
			return []domain.SubagentDisplayName{{ID: a.ID, Name: "Atlas"}, {ID: a.ID, Name: "Nova"}}
		},
		"blank": func(a, b domain.SubagentObservation) []domain.SubagentDisplayName {
			return []domain.SubagentDisplayName{{ID: a.ID, Name: " \t "}}
		},
		"long": func(a, b domain.SubagentObservation) []domain.SubagentDisplayName {
			return []domain.SubagentDisplayName{{ID: a.ID, Name: long}}
		},
		"control": func(a, b domain.SubagentObservation) []domain.SubagentDisplayName {
			return []domain.SubagentDisplayName{{ID: a.ID, Name: "At\nlas"}}
		},
		"overflow": func(a, b domain.SubagentObservation) []domain.SubagentDisplayName {
			return []domain.SubagentDisplayName{{ID: a.ID, Name: "A"}, {ID: b.ID, Name: "B"}, {ID: domain.NewID(), Name: "C"}}
		},
	}
	for name, names := range cases {
		t.Run(name, func(t *testing.T) {
			f := bindForDisplay(t)
			a, b := displayChild(f, "one"), displayChild(f, "two")
			e := f.event(domain.ExecutionSubagentObserved, 3)
			e.Subagents = []domain.SubagentObservation{a, b}
			e.SubagentNames = names(a, b)
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("invalid display metadata accepted")
			}
			// Nothing was retained, so the same sequence remains publishable.
			e.SubagentNames = nil
			f.publish(t, e)
			if retainedDisplayName(t, f, a.ID) != nil {
				t.Fatal("rejected metadata was partially retained")
			}
		})
	}
}

func TestSubagentDisplayNamePreservedOnOmissionAndUpdatedByLaterObservation(t *testing.T) {
	f := bindForDisplay(t)
	child := displayChild(f, "named")
	e := f.event(domain.ExecutionSubagentObserved, 3)
	e.Subagents = []domain.SubagentObservation{child}
	e.SubagentNames = []domain.SubagentDisplayName{{ID: child.ID, Name: "Atlas"}}
	f.publish(t, e)
	child.Status = domain.SubagentCompleted
	e = f.event(domain.ExecutionSubagentObserved, 4)
	e.Subagents = []domain.SubagentObservation{child}
	f.publish(t, e)
	if name := retainedDisplayName(t, f, child.ID); name == nil || *name != "Atlas" {
		t.Fatal("omitted later observation erased the last valid name")
	}
	e = f.event(domain.ExecutionSubagentObserved, 5)
	e.Subagents = []domain.SubagentObservation{child}
	e.SubagentNames = []domain.SubagentDisplayName{{ID: child.ID, Name: "Atlas II"}}
	f.publish(t, e)
	if name := retainedDisplayName(t, f, child.ID); name == nil || *name != "Atlas II" {
		t.Fatal("later valid name did not update retention")
	}
}

func TestSubagentDisplayNamesRejectedOnNonSubagentEvents(t *testing.T) {
	f := bindForDisplay(t)
	e := f.event(domain.ExecutionTurnFinished, 3)
	e.Outcome = domain.ExecutionSucceeded
	req := f.requestEvent(t, e)
	req.SubagentNames = []*pb.SubagentDisplayName{{ChildId: string(domain.NewID()), Name: "Atlas"}}
	if _, err := f.call(req); err == nil {
		t.Fatal("names attached to a non-child event were accepted")
	}
}
