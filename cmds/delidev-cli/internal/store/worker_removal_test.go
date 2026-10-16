package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func fixtureMachine(name string, retired bool) domain.Machine {
	return domain.Machine{Name: name, OS: "linux", Architecture: "amd64", Version: "1", Disabled: retired, Retired: retired}
}

func TestRetiredMachinesAreFilteredBeforePaginationAndKeepExactReads(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	var active, retired []domain.ID
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.machines", nil, func(tx *Tx) (any, error) {
		// Retired rows sit between active rows so a post-limit filter would
		// produce short or empty pages.
		for i := 0; i < 9; i++ {
			id := domain.NewID()
			isRetired := i%3 != 0
			if _, err := tx.Put(domain.MachineKind, id, 0, "", "", fixtureMachine("machine", isRetired)); err != nil {
				return nil, err
			}
			if isRetired {
				retired = append(retired, id)
			} else {
				active = append(active, id)
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var seen []domain.ID
	after := domain.ID("")
	for page := 0; ; page++ {
		rows, more, err := s.ListPage(ctx, Filter{Kind: domain.MachineKind, After: after, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if more && len(rows) != 2 {
			t.Fatalf("page %d was short before the end: %d", page, len(rows))
		}
		for _, row := range rows {
			seen = append(seen, row.ID)
			after = row.ID
		}
		if !more {
			break
		}
	}
	if len(seen) != len(active) {
		t.Fatalf("pages returned %d records, want %d active", len(seen), len(active))
	}
	for i := range active {
		if seen[i] != active[i] {
			t.Fatal("pagination order or membership changed")
		}
	}
	snapshot, _, err := s.Snapshot(ctx, Filter{Kind: domain.MachineKind, Limit: MaxPage})
	if err != nil || len(snapshot) != len(active) {
		t.Fatalf("snapshot included retired machines: %d %v", len(snapshot), err)
	}
	listed, err := s.List(ctx, Filter{Kind: domain.MachineKind, Limit: MaxPage})
	if err != nil || len(listed) != len(active) {
		t.Fatalf("list included retired machines: %d %v", len(listed), err)
	}
	for _, id := range retired {
		record, err := s.Get(ctx, domain.MachineKind, id)
		if err != nil {
			t.Fatalf("exact read of a retired machine failed: %v", err)
		}
		value, err := Decode[domain.Machine](record)
		if err != nil || value.Name != "machine" || !value.Retired {
			t.Fatalf("retired machine lost its original record: %+v %v", value, err)
		}
	}
	err = s.Read(ctx, func(tx *Tx) error {
		counts, err := tx.Overview(nil, time.Now().UTC())
		if err == nil && counts.RegisteredWorkers != uint64(len(active)) {
			t.Fatalf("overview counted retired Workers: %d", counts.RegisteredWorkers)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMachinesWithoutRetiredMarkerDefaultToActive(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	// Earlier records never wrote the marker; it must neither decode as retired
	// nor appear when an active record is written again.
	legacy := []byte(`{"name":"legacy","os":"linux","architecture":"amd64","version":"1","installations":null,"last_seen":"0001-01-01T00:00:00Z","disabled":false}`)
	var value domain.Machine
	if err := domain.Decode(legacy, &value); err != nil || value.Retired {
		t.Fatalf("legacy machine decoded as retired: %v", err)
	}
	id := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.legacy", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.MachineKind, id, 0, "", "", value)
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := s.ListPage(ctx, Filter{Kind: domain.MachineKind, Limit: 5})
	if err != nil || len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("legacy machine missing from the active inventory: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rows[0].Data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, present := fields["retired"]; present {
		t.Fatal("active machine gained a retired marker")
	}
}

func TestRetiredMachineMustBeDisabled(t *testing.T) {
	value := fixtureMachine("machine", true)
	value.Disabled = false
	if value.Validate() == nil {
		t.Fatal("a retired machine that is still enabled was accepted")
	}
}

func TestWorkerDevicesForMachineReturnsOnlyWorkerRegistrations(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	machine, worker, revoked, client, other := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.devices", nil, func(tx *Tx) (any, error) {
		for _, device := range []struct {
			id    domain.ID
			value domain.Device
		}{
			{worker, domain.Device{Name: "worker", Type: domain.WorkerDevice, MachineID: machine, PairedAt: time.Now().UTC()}},
			{revoked, domain.Device{Name: "old", Type: domain.WorkerDevice, MachineID: machine, Revoked: true, PairedAt: time.Now().UTC()}},
			{client, domain.Device{Name: "client", Type: domain.ClientDevice, MachineID: machine, PairedAt: time.Now().UTC()}},
			{other, domain.Device{Name: "foreign", Type: domain.WorkerDevice, MachineID: domain.NewID(), PairedAt: time.Now().UTC()}},
		} {
			if _, err := tx.Put(domain.DeviceKind, device.id, 0, "", "", device.value); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Read(ctx, func(tx *Tx) error {
		rows, err := tx.WorkerDevicesForMachine(machine)
		if err != nil {
			return err
		}
		if len(rows) != 2 {
			t.Fatalf("unexpected Worker registrations: %d", len(rows))
		}
		for _, row := range rows {
			if row.ID == client || row.ID == other {
				t.Fatal("client or foreign device matched a machine")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
