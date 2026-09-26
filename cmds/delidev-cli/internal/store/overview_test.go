package store

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestOverviewCountsCurrentOwnershipAndUnansweredRequests(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	_, err := s.Mutate(ctx, domain.NewID(), "overview.fixture", nil, func(tx *Tx) (any, error) {
		for _, scenario := range []string{"pending", "answered", "approved", "closed", "old-execution", "archived", "cleanup-uncertain", "idle"} {
			id, execution := domain.NewID(), domain.NewID()
			v := domain.Session{Archive: domain.NotArchived, ActiveExecutionID: execution}
			if scenario == "idle" {
				v.ActiveExecutionID = ""
			}
			if scenario == "archived" {
				v.Archive = domain.Archived
			}
			if scenario == "cleanup-uncertain" {
				v.Outcome = domain.ExecutionFailed
			}
			if _, err := tx.Put(domain.SessionKind, id, 0, id, "", v); err != nil {
				return nil, err
			}
			interaction := domain.ExecutionInteraction{ExecutionID: execution, Closure: domain.InteractionOpen}
			switch scenario {
			case "answered":
				interaction.Response = &domain.QuestionResponse{ID: domain.NewID()}
			case "approved":
				interaction.ApprovalResponse = &domain.ApprovalResponse{ID: domain.NewID()}
			case "closed", "cleanup-uncertain":
				interaction.Closure = domain.InteractionNativeClosed
			case "old-execution":
				interaction.ExecutionID = domain.NewID()
			}
			if _, err := tx.Put(domain.InteractionKind, domain.NewID(), 0, id, "", interaction); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, before, err := s.Snapshot(ctx, Filter{Kind: domain.SessionKind, Limit: MaxPage})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Read(ctx, func(tx *Tx) error {
		v, err := tx.Overview(nil, time.Now().UTC())
		if err == nil && (v.ActiveSessions != 7 || v.PendingInteractions != 1 || v.RegisteredWorkers != 0 || v.ConnectedWorkers != 0) {
			t.Fatalf("wrong ownership counts: %+v", v)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, after, err := s.Snapshot(ctx, Filter{Kind: domain.SessionKind, Limit: MaxPage})
	if err != nil || before != after {
		t.Fatal("overview mutated retained state")
	}
}

func TestOverviewRequiresLiveAuthorizedFreshWorker(t *testing.T) {
	s, _ := openTest(t)
	ctx, now := context.Background(), time.Now().UTC()
	active := map[domain.ID]bool{}
	_, err := s.Mutate(ctx, domain.NewID(), "overview.workers", nil, func(tx *Tx) (any, error) {
		for _, scenario := range []string{"connected", "offline", "expired", "future", "revoked", "disabled", "missing-device"} {
			id := domain.NewID()
			active[id] = scenario != "offline"
			if _, err := tx.Put(domain.MachineKind, id, 0, "", "", domain.Machine{Disabled: scenario == "disabled"}); err != nil {
				return nil, err
			}
			if scenario != "missing-device" {
				if _, err := tx.Put(domain.DeviceKind, domain.NewID(), 0, "", "", domain.Device{Type: domain.WorkerDevice, MachineID: id, Revoked: scenario == "revoked"}); err != nil {
					return nil, err
				}
			}
			seen := now
			if scenario == "expired" {
				seen = now.Add(-time.Minute)
			}
			if scenario == "future" {
				seen = now.Add(time.Minute)
			}
			if err := tx.SetWorkerInstance(id, domain.NewID(), seen); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Read(ctx, func(tx *Tx) error {
		v, err := tx.Overview(active, now)
		if err == nil && (v.RegisteredWorkers != 7 || v.ConnectedWorkers != 1) {
			t.Fatalf("connection is not readiness: %+v", v)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
