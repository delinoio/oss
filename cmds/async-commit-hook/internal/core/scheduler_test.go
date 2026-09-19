//go:build !windows

package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func awaitCheck(t *testing.T, s *Service, id string, state State) Run {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.Store.Run(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range r.Checks {
			if c.State == state {
				return r
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s never reached %s", id, state)
	return Run{}
}
func TestFIFOQueueAcrossWorkers(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"sleep 0.3\"\npolicy=\"queue\"\n")
	a, _ := s.Submit(context.Background(), repo, "", false)
	b, _ := s.Submit(context.Background(), repo, "", false)
	done := make(chan error, 2)
	go func() { done <- s.RunOne(b.RunID) }()
	go func() { done <- s.RunOne(a.RunID) }()
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	ar, _ := s.Store.Run(a.RunID)
	br, _ := s.Store.Run(b.RunID)
	if ar.Checks[0].FinishedAt.After(*br.Checks[0].StartedAt) {
		t.Fatal("FIFO group overlapped")
	}
}
func TestReplaceReapsDescendantsBeforeNextStarts(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child")
	command := fmt.Sprintf("sleep 30 & echo $! > '%s'; wait", marker)
	s, repo := fixture(t, fmt.Sprintf("version=1\n[checks.test]\ncommand=%q\npolicy=\"replace\"\n", command))
	a, _ := s.Submit(context.Background(), repo, "", false)
	done := make(chan error, 2)
	go func() { done <- s.RunOne(a.RunID) }()
	awaitCheck(t, s, a.RunID, Running)
	var pid int
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(marker)
		if _, err := fmt.Sscan(string(b), &pid); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	child, err := ProcessIdentity(pid)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.Submit(context.Background(), repo, "", false)
	go func() { done <- s.RunOne(b.RunID) }()
	awaitCheck(t, s, b.RunID, Running)
	if ProcessAlive(child) {
		t.Fatal("replacement started before descendant exited")
	}
	s.Store.Cancel(b.RunID, Cancelled)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	ar, _ := s.Store.Run(a.RunID)
	if ar.State != Replaced {
		t.Fatalf("old run %s", ar.State)
	}
}
func TestRecoveryInterruptsWithoutReplayAndPIDReuse(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"echo must-not-replay\"\n")
	receipt, _ := s.Submit(context.Background(), repo, "", false)
	r, _ := s.Store.Run(receipt.RunID)
	r.State = Running
	s.Store.SaveRun(r)
	c := r.Checks[0]
	c.State = Running
	c.Process = Process{PID: os.Getpid(), Birth: "different-incarnation", Group: os.Getpid()}
	s.Store.SaveCheck(c)
	if err := s.RunOne(r.ID); err != nil {
		t.Fatal(err)
	}
	r, _ = s.Store.Run(r.ID)
	if r.State != Interrupted || r.Checks[0].Log.ID != "" {
		t.Fatal("interrupted command replayed")
	}
}
func TestPrePushAllTipsUsesExactSHA(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"true\"\n")
	first := runFixture(t, s, repo)
	if _, err := Git(context.Background(), repo, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "next"); err != nil {
		t.Fatal(err)
	}
	head, _ := ResolveCommit(context.Background(), repo, "HEAD")
	zeros := strings.Repeat("0", 40)
	input := fmt.Sprintf("refs/heads/old %s refs/heads/old %s\nrefs/heads/new %s refs/heads/new %s\nrefs/tags/v1 %s refs/tags/v1 %s\n(delete) %s refs/heads/deleted %s\n", first.Commit, zeros, head, zeros, head, zeros, zeros, first.Commit)
	gates, err := s.PrePush(context.Background(), repo, strings.NewReader(input), PushBlock)
	if err == nil || len(gates) != 2 || !gates[0].Passed || gates[1].Passed {
		t.Fatalf("wrong multi-ref gate: %+v %v", gates, err)
	}
	receipt, _ := s.Submit(context.Background(), repo, head, false)
	go s.RunOne(receipt.RunID)
	if _, err = s.PrePush(context.Background(), repo, strings.NewReader(input), PushWait); err != nil {
		t.Fatal(err)
	}
}
