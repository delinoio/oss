package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryResumesOnlyEntirelyUnclaimedRuns(t *testing.T) {
	for _, state := range []State{Preparing, Running} {
		for _, claimed := range []bool{false, true} {
			t.Run(string(state)+map[bool]string{true: "-claimed", false: "-unclaimed"}[claimed], func(t *testing.T) {
				s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"echo recovered\"\n")
				receipt, err := s.Submit(context.Background(), repo, "", false)
				if err != nil {
					t.Fatal(err)
				}
				r, _ := s.Store.Run(receipt.RunID)
				r.State = state
				if err := s.Store.SaveRun(r); err != nil {
					t.Fatal(err)
				}
				if claimed {
					c := r.Checks[0]
					c.State = Preparing
					if err := s.Store.SaveCheck(c); err != nil {
						t.Fatal(err)
					}
				}
				workspace := filepath.Join(s.Store.Root, "workspaces", r.ID)
				if err := os.MkdirAll(workspace, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(workspace, "partial"), []byte("partial source"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := s.RunOne(r.ID); err != nil {
					t.Fatal(err)
				}
				result, err := s.Store.Run(r.ID)
				if err != nil {
					t.Fatal(err)
				}
				want := Passed
				if claimed {
					want = Interrupted
				}
				if result.State != want || result.Checks[0].State != want {
					t.Fatalf("unexpected recovery: %+v", result)
				}
				if _, err := os.Stat(workspace); !os.IsNotExist(err) {
					t.Fatal("workspace not cleaned", err)
				}
				if !claimed && !s.GateRun(result).Passed {
					t.Fatal("recovered execution did not validate")
				}
			})
		}
	}
}
