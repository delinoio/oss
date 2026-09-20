package core

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPushEvidenceVerificationReleasesWriterAndRevalidatesSelection(t *testing.T) {
	for _, submit := range []bool{false, true} {
		mode := "check"
		if submit {
			mode = "run-and-wait"
		}
		t.Run(mode, func(t *testing.T) {
			for _, mutation := range []string{"unrelated-write", "newer-attempt", "prune"} {
				t.Run(mutation, func(t *testing.T) {
					s, repo := fixture(t, "version=1\n[checks.test]\ncommand='unused'\n")
					other, err := Open(s.Paths)
					if err != nil {
						t.Fatal(err)
					}
					defer other.Close()
					receipt, err := s.Submit(context.Background(), repo, "", false)
					if err != nil {
						t.Fatal(err)
					}
					r, err := s.Store.Run(receipt.RunID)
					if err != nil {
						t.Fatal(err)
					}
					c := r.Checks[0]
					c.State = Passed
					c.Log, err = s.SaveEvidence(r.ID, "log", []byte("passing evidence"))
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
					plan, err := s.Plan(context.Background(), repo, "HEAD")
					if err != nil {
						t.Fatal(err)
					}
					unrelated, err := s.Plan(context.Background(), repo, "HEAD")
					if err != nil {
						t.Fatal(err)
					}
					unrelated.Commit = strings.Repeat("b", 40)
					if _, err = other.Store.InsertRun(&unrelated, ""); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					verifying, release := make(chan struct{}), make(chan struct{}, 1)
					defer close(release)
					type result struct {
						gate Gate
						err  error
					}
					finished := make(chan result, 1)
					go func() {
						first := true
						gate, err := s.selectLatestAttempt(ctx, plan, func(run Run) Gate {
							g := s.GateRun(run)
							if first {
								first = false
								close(verifying)
								select {
								case <-release:
								case <-ctx.Done():
								}
							}
							return g
						}, submit)
						finished <- result{gate, err}
					}()
					select {
					case <-verifying:
					case <-ctx.Done():
						t.Fatal("verification did not start")
					}
					// This write must complete while evidence evaluation remains paused.
					unrelated.Checks[0].State = Collecting
					if err = other.Store.SaveCheck(unrelated.Checks[0]); err != nil {
						t.Fatal("verification held SQLite writer", err)
					}
					wantID := r.ID
					if mutation == "newer-attempt" {
						newer, err := other.Submit(ctx, repo, "", false)
						if err != nil {
							t.Fatal(err)
						}
						wantID = newer.RunID
					} else if mutation == "prune" {
						if _, err = other.Prune(false, 0, 1); err != nil {
							t.Fatal(err)
						}
					}
					release <- struct{}{}
					got := <-finished
					if got.err != nil {
						t.Fatal(got.err)
					}
					if mutation == "prune" {
						if got.gate.Passed || (submit && got.gate.RunID == r.ID) || (!submit && (got.gate.RunID != r.ID || got.gate.State != Expired)) {
							t.Fatal("pruned snapshot reused", got.gate)
						}
					} else if got.gate.RunID != wantID || got.gate.Passed != (mutation == "unrelated-write") {
						t.Fatal("stale selection used", got.gate)
					}
				})
			}
		})
	}
}
