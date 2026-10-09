// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestMachineResourceProjectsOnlyOriginalWorkerHeartbeat(t *testing.T) {
	s, _ := newDoctorFixture(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	ids := []domain.ID{domain.NewID(), domain.NewID(), domain.NewID()}
	seen := []time.Time{now.Add(-5 * time.Second), now.Add(-time.Minute), {}}
	originals := map[domain.ID]store.Record{}
	for i, id := range ids {
		doctorPut(t, s, domain.MachineKind, id, 0, map[string]any{"name": "Original Runner", "os": "linux", "architecture": "amd64", "last_seen": now.Add(-6 * time.Hour), "future_metadata": "preserve"})
		if i < 2 {
			_, err := s.Store.Mutate(context.Background(), domain.NewID(), "fixture.machine-lease", id, func(tx *store.Tx) (any, error) { return nil, tx.SetWorkerInstance(id, domain.NewID(), seen[i]) })
			if err != nil {
				t.Fatal(err)
			}
		}
		record, err := s.Store.Get(context.Background(), domain.MachineKind, id)
		if err != nil {
			t.Fatal(err)
		}
		originals[id] = record
	}
	_, beforeCursor, err := s.Store.Snapshot(context.Background(), store.Filter{Kind: domain.MachineKind, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	check := func(resource *pb.Resource) {
		t.Helper()
		id := domain.ID(resource.Id)
		original := originals[id]
		var fields map[string]json.RawMessage
		if json.Unmarshal(resource.DocumentJson, &fields) != nil || string(fields["future_metadata"]) != `"preserve"` {
			t.Fatal("lost original metadata")
		}
		if resource.Revision != original.Revision || resource.SchemaVersion != 1 {
			t.Fatal("liveness changed entity revision/schema")
		}
		for i, expected := range ids {
			if id != expected {
				continue
			}
			if i == 2 {
				if _, present := fields["last_seen"]; present {
					t.Fatal("missing lease adopted attachment timestamp or another Worker")
				}
				return
			}
			var got time.Time
			if json.Unmarshal(fields["last_seen"], &got) != nil || !got.Equal(seen[i]) {
				t.Fatalf("wrong exact-machine lease: %s", resource.DocumentJson)
			}
			return
		}
		t.Fatal("foreign Machine projected")
	}
	for _, id := range ids {
		response, err := s.GetResource(transferOwner(), connect.NewRequest(&pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, Id: string(id)}))
		if err != nil {
			t.Fatal(err)
		}
		check(response.Msg.Resource)
	}
	page, err := s.ListResources(transferOwner(), connect.NewRequest(&pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_MACHINE}}))
	if err != nil || len(page.Msg.Resources) != 3 {
		t.Fatalf("list: %v", err)
	}
	for _, resource := range page.Msg.Resources {
		check(resource)
	}
	snapshot, err := s.GetSnapshot(transferOwner(), connect.NewRequest(&pb.GetSnapshotRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_MACHINE}}))
	if err != nil || len(snapshot.Msg.Resources) != 3 {
		t.Fatalf("snapshot: %v", err)
	}
	for _, resource := range snapshot.Msg.Resources {
		check(resource)
	}
	for _, id := range ids {
		after, err := s.Store.Get(context.Background(), domain.MachineKind, id)
		if err != nil {
			t.Fatal(err)
		}
		before := originals[id]
		if !bytes.Equal(before.Data, after.Data) || before.Revision != after.Revision || before.UpdatedAt != after.UpdatedAt {
			t.Fatal("projection wrote stored Machine")
		}
	}
	_, afterCursor, err := s.Store.Snapshot(context.Background(), store.Filter{Kind: domain.MachineKind, Limit: 200})
	if err != nil || afterCursor != beforeCursor {
		t.Fatalf("projection emitted durable events: %v", err)
	}
	// Portable exports retain configuration identity only, never lease timestamps.
	portableID := domain.NewID()
	doctorPut(t, s, domain.MachineKind, portableID, 0, domain.Machine{Name: "Portable Runner", OS: "linux", Architecture: "amd64", LastSeen: now.Add(-time.Hour)})
	if _, err := s.Store.Mutate(context.Background(), domain.NewID(), "fixture.portable-lease", nil, func(tx *store.Tx) (any, error) { return nil, tx.SetWorkerInstance(portableID, domain.NewID(), now) }); err != nil {
		t.Fatal(err)
	}
	repository := domain.Repository{Name: "Fixture", Checkouts: []domain.Checkout{{MachineID: portableID, Path: "/fixture/root"}}, PreferredRemote: "origin"}
	doctorPut(t, s, domain.RepositoryKind, domain.NewID(), 0, repository)
	exported, err := s.ExportConfiguration(transferOwner(), connect.NewRequest(&pb.ExportConfigurationRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var bundle map[string]json.RawMessage
	if json.Unmarshal(exported.Msg.DocumentJson, &bundle) != nil || bytes.Contains(bundle["machines"], []byte("last_seen")) || bytes.Contains(bundle["machines"], []byte("instance")) {
		t.Fatal("portable export adopted runtime liveness")
	}
}

func TestMachineHeartbeatProjectionMissingMalformedAndFutureRemainUnknown(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	record := store.Record{Kind: domain.MachineKind, Data: []byte(`{"name":"Runner","last_seen":"2026-10-09T00:00:00Z","opaque":{"keep":true}}`)}
	original := append([]byte(nil), record.Data...)
	for _, lease := range []struct {
		instance domain.ID
		seen     time.Time
	}{{"", now}, {"invalid", now}, {domain.NewID(), time.Time{}}, {domain.NewID(), time.UnixMilli(0)}, {domain.NewID(), now.Add(2 * time.Second)}} {
		projected, err := machineHeartbeatProjection(record, lease.instance, lease.seen, now)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(projected.Data, []byte("last_seen")) || !bytes.Contains(projected.Data, []byte(`"opaque":{"keep":true}`)) || !bytes.Equal(record.Data, original) {
			t.Fatal("unknown lease leaked attachment evidence or rewrote source")
		}
	}
	for _, raw := range []string{"null", "[]", "{"} {
		record.Data = []byte(raw)
		if _, err := machineHeartbeatProjection(record, domain.NewID(), now, now); domain.SafeError(err).Code != domain.RecoveryRequired {
			t.Fatal("malformed source presented live")
		}
	}
}
