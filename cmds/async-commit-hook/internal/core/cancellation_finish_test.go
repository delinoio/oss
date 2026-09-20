package core

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalCheckPublicationHonorsCommittedCancellation(t *testing.T) {
	for _, reason := range []State{Cancelled, Replaced} {
		for _, proposed := range []State{Passed, Failed, Blocked, Interrupted} {
			t.Run(string(reason)+"/"+string(proposed), func(t *testing.T) {
				s, repo := fixture(t, "version=1\n[checks.test]\ncommand='unused'\n")
				receipt, err := s.Submit(context.Background(), repo, "", false)
				if err != nil {
					t.Fatal(err)
				}
				r, err := s.Store.Run(receipt.RunID)
				if err != nil {
					t.Fatal(err)
				}
				owner, err := TryLock(filepath.Join(s.Store.Root, "locks", r.ID+".lock"))
				if err != nil || owner == nil {
					t.Fatal("missing worker ownership", err)
				}
				defer owner.Close()
				c := r.Checks[0]
				c.State, r.State = Collecting, Running
				// Retained valid evidence makes a lost cancellation genuinely capable
				// of producing a passing gate, not merely an evidence-missing failure.
				c.Log, err = s.SaveEvidence(r.ID, "combined.log", []byte("completed"))
				if err != nil {
					t.Fatal(err)
				}
				if err = s.Store.SaveCheck(c); err != nil {
					t.Fatal(err)
				}
				if err = s.Store.SaveRun(r); err != nil {
					t.Fatal(err)
				}
				if s.Store.Cancellation(c.ID) != "" {
					t.Fatal("unexpected initial cancellation")
				}
				other, err := OpenStore(s.Store.Root)
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				// This completion barrier puts cancellation after the worker's stale
				// read but before terminal publication, without timing assumptions.
				cancelled := make(chan error, 1)
				go func() { cancelled <- other.Cancel(r.ID, reason) }()
				if err = <-cancelled; err != nil {
					t.Fatal(err)
				}
				var logs bytes.Buffer
				s.Log = slog.New(slog.NewJSONHandler(&logs, nil))
				if err = s.finishCheck(c, proposed, "", ""); err != nil {
					t.Fatal(err)
				}
				r, err = s.Store.Run(r.ID)
				if err != nil {
					t.Fatal(err)
				}
				if r.Checks[0].State != reason || r.Checks[0].FinishedAt == nil {
					t.Fatal("terminal state lost cancellation", r.Checks[0])
				}
				r.State, _ = aggregateCheckState(r.Checks)
				if err = s.finalize(r); err != nil {
					t.Fatal(err)
				}
				gate, err := s.Gate(context.Background(), repo, r.Commit)
				if err != nil || gate.Passed || gate.State != reason {
					t.Fatal("cancelled run could pass", gate, err)
				}
				if !strings.Contains(logs.String(), `"state":"`+string(reason)+`"`) {
					t.Fatal("log reported the stale state", logs.String())
				}
			})
		}
	}
}

func TestCancellationAfterTerminalPublicationDoesNotRewriteEvidence(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand='unused'\n")
	receipt, err := s.Submit(context.Background(), repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Store.Run(receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	r.State = Running
	if err = s.Store.SaveRun(r); err != nil {
		t.Fatal(err)
	}
	if err = s.finishCheck(r.Checks[0], Passed, "", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Store.Cancel(r.ID, Cancelled); err != nil {
		t.Fatal(err)
	}
	r, err = s.Store.Run(r.ID)
	if err != nil || r.Checks[0].State != Passed || s.Store.Cancellation(r.Checks[0].ID) != "" {
		t.Fatal("late cancellation rewrote terminal result", r, err)
	}
}
