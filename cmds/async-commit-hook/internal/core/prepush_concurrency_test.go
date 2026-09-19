package core

import (
	"context"
	"os"
	"testing"
)

func TestConcurrentPushSelectionReusesOneLatestAttempt(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"echo check\"\n")
	other, err := Open(s.Paths)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ctx := context.Background()
	selectBoth := func(wantCount int) Gate {
		t.Helper()
		start := make(chan struct{})
		type result struct {
			gate Gate
			err  error
		}
		results := make(chan result, 2)
		for _, service := range []*Service{s, other} {
			// Both clients prepare before either can enter attempt selection.
			plan, err := service.Plan(ctx, repo, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			go func() { <-start; gate, err := service.selectPushAttempt(ctx, plan); results <- result{gate, err} }()
		}
		close(start)
		a, b := <-results, <-results
		if a.err != nil || b.err != nil || a.gate.RunID == "" || a.gate.RunID != b.gate.RunID {
			t.Fatalf("duplicate attempts: %+v %+v", a, b)
		}
		var count int
		if err := s.Store.DB.QueryRow("SELECT count(*) FROM runs").Scan(&count); err != nil || count != wantCount {
			t.Fatalf("count=%d want=%d err=%v", count, wantCount, err)
		}
		return a.gate
	}
	first := selectBoth(1)
	if again := selectBoth(1); again.RunID != first.RunID {
		t.Fatal("unfinished attempt not reused")
	}
	r, err := s.Store.Run(first.RunID)
	if err != nil {
		t.Fatal(err)
	}
	r.State = Failed
	if err = s.Store.SaveRun(r); err != nil {
		t.Fatal(err)
	}
	second := selectBoth(2)
	if second.RunID == first.RunID {
		t.Fatal("terminal failure reused")
	}
	r, err = s.Store.Run(second.RunID)
	if err != nil {
		t.Fatal(err)
	}
	c := r.Checks[0]
	c.State = Passed
	c.Log, err = s.SaveEvidence(r.ID, "log", []byte("passed"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.SaveCheck(c); err != nil {
		t.Fatal(err)
	}
	r.State = Passed
	if err = s.Store.SaveRun(r); err != nil {
		t.Fatal(err)
	}
	if passing := selectBoth(2); !passing.Passed || passing.RunID != second.RunID {
		t.Fatal("passing evidence not reused", passing)
	}
	path, err := s.Store.EvidencePath(r.ID, c.Log.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if renewed := selectBoth(3); renewed.RunID == second.RunID {
		t.Fatal("missing evidence reused")
	}
}
