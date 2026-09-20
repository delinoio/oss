package core

import (
	"context"
	"testing"
)

func TestQueueRetainsOrderWhileEarlierDependencyRuns(t *testing.T) {
	s, repo := fixture(t, `version=1
[checks.prep]
command="echo prep"
[checks.test]
command="echo test"
depends_on=["prep"]
policy="queue"
`)
	otherStore, err := OpenStore(s.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer otherStore.Close()
	other := &Service{Store: otherStore}
	runs := make([]Run, 2)
	for i := range runs {
		receipt, err := s.Submit(context.Background(), repo, "", false)
		if err != nil {
			t.Fatal(err)
		}
		runs[i], err = s.Store.Run(receipt.RunID)
		if err != nil {
			t.Fatal(err)
		}
	}
	find := func(r Run, name string) Check {
		for _, c := range r.Checks {
			if c.Name == name {
				return c
			}
		}
		t.Fatalf("missing check %s", name)
		return Check{}
	}
	older, newer := find(runs[0], "test"), find(runs[1], "test")
	prep := find(runs[0], "prep")
	prep.State = Running
	if err := s.Store.SaveCheck(prep); err != nil {
		t.Fatal(err)
	}
	newerPrep := find(runs[1], "prep")
	newerPrep.State = Passed
	if err := otherStore.SaveCheck(newerPrep); err != nil {
		t.Fatal(err)
	}
	claim := func(worker *Service, r Run, c Check, want bool) {
		t.Helper()
		got, err := worker.claim(r, c)
		if err != nil || got != want {
			t.Fatalf("claim %s = %v, %v; want %v", r.ID, got, err, want)
		}
	}
	claim(other, runs[1], newer, false)
	prep.State = Passed
	if err := s.Store.SaveCheck(prep); err != nil {
		t.Fatal(err)
	}
	claim(s, runs[0], older, true)
	claim(other, runs[1], newer, false)
	if err := s.finishCheck(older, Passed, "", ""); err != nil {
		t.Fatal(err)
	}
	claim(other, runs[1], newer, true)
	// Terminal blocked entries and queued cancellation requests no longer reserve
	// a position, but claimed entries above retain ownership until reconciled.
	for _, state := range []State{Blocked, Queued} {
		older.State = state
		newer.State = Queued
		if err := s.Store.SaveCheck(older); err != nil {
			t.Fatal(err)
		}
		if err := otherStore.SaveCheck(newer); err != nil {
			t.Fatal(err)
		}
		if state == Queued {
			if _, err := s.Store.DB.Exec("UPDATE checks SET cancel='cancelled' WHERE id=?", older.ID); err != nil {
				t.Fatal(err)
			}
		}
		claim(other, runs[1], newer, true)
	}
}
