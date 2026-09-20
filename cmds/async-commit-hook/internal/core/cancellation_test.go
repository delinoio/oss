package core

import (
	"context"
	"path/filepath"
	"testing"
)

func TestUnstartedCancellationCompletesWithoutWorker(t *testing.T) {
	for _, config := range []string{"version=1\n", "version=1\n[checks.test]\ncommand=\"unused\"\n"} {
		s, repo := fixture(t, config)
		receipt, err := s.Submit(context.Background(), repo, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Store.Cancel(receipt.RunID, Cancelled); err != nil {
			t.Fatal(err)
		}
		r, err := s.Wait(context.Background(), receipt.RunID)
		if err != nil || r.State != Cancelled || r.FinishedAt == nil {
			t.Fatal("queued cancellation did not finish", err)
		}
		for _, c := range r.Checks {
			if c.State != Cancelled || c.FinishedAt == nil || c.StartedAt != nil {
				t.Fatalf("check not cancelled before start: %+v", c)
			}
		}
		if err := s.RunOne(r.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.Store.Cancel(r.ID, Cancelled); err != nil {
			t.Fatal(err)
		}
		after, _ := s.Store.Run(r.ID)
		if !after.FinishedAt.Equal(*r.FinishedAt) {
			t.Fatal("idempotent cancellation changed completion")
		}
		pending, err := s.Store.Pending()
		if err != nil || len(pending) != 0 {
			t.Fatal("cancelled run remains pending", err)
		}
	}
}

func TestCancellationDoesNotFinalizeRunOwnedByWorker(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"unused\"\n")
	receipt, err := s.Submit(context.Background(), repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := TryLock(filepath.Join(s.Store.Root, "locks", receipt.RunID+".lock"))
	if err != nil || owner == nil {
		t.Fatal("cannot acquire worker ownership", err)
	}
	defer owner.Close()
	if err := s.Store.Cancel(receipt.RunID, Cancelled); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Store.Run(receipt.RunID)
	if r.State != Queued || r.FinishedAt != nil || s.Store.Cancellation(r.Checks[0].ID) != Cancelled {
		t.Fatal("worker ownership bypassed")
	}
}
