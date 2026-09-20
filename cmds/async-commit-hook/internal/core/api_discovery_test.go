package core

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
)

// Re-execute the test binary as a stalled Git executable. No native shell,
// real user repository is involved. Unselected commands delegate to real Git;
// the selected stalled process has no children when its readiness is published.
func init() {
	if len(os.Args) < 2 || os.Args[1] != "--no-replace-objects" {
		return
	}
	for i := 2; i+1 < len(os.Args); i++ {
		if os.Args[i] != "-C" {
			continue
		}
		root := os.Args[i+1]
		data, err := os.ReadFile(filepath.Join(root, ".git", "ach-test-stalled-git"))
		if err != nil {
			return
		}
		if len(data) != 0 {
			var config struct {
				RealGit string
				Match   []string
			}
			if json.Unmarshal(data, &config) != nil {
				os.Exit(96)
			}
			args := os.Args[i+2:]
			if len(args) < len(config.Match) || !slices.Equal(args[:len(config.Match)], config.Match) {
				cmd := exec.Command(config.RealGit, os.Args[1:]...)
				cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
				if err := cmd.Run(); err != nil {
					if exit, ok := err.(*exec.ExitError); ok {
						os.Exit(exit.ExitCode())
					}
					os.Exit(95)
				}
				os.Exit(0)
			}
		}
		if err := AtomicWrite(filepath.Join(root, ".git", "ach-test-git-ready"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(97)
		}
		for {
			time.Sleep(time.Hour)
		}
	}
}

func TestSourceRPCRequestCancellationReapsDiscovery(t *testing.T) {
	for _, operation := range []string{"branches", "commits", "changes"} {
		t.Run(operation, func(t *testing.T) { testSourceRPCCancellation(t, operation, nil, false) })
	}
}

func TestSourceRPCRequestCancellationAfterDiscovery(t *testing.T) {
	for _, scenario := range []struct {
		name, operation string
		match           []string
	}{
		{"branches", "branches", []string{"for-each-ref"}},
		{"commit-resolution", "commits", []string{"rev-parse", "--verify"}},
		{"history-stream", "commits", []string{"log"}},
		{"diff-resolution", "changes", []string{"rev-parse", "--verify"}},
		{"diff-config", "changes", []string{"cat-file"}},
		{"diff-default-base", "changes", []string{"symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"}},
		{"merge-base", "changes-base", []string{"merge-base"}},
		{"diff-stream", "changes-base", []string{"diff"}},
	} {
		t.Run(scenario.name, func(t *testing.T) { testSourceRPCCancellation(t, scenario.operation, scenario.match, false) })
	}
}

// Deliver deadline expiry at the process-readiness barrier without imposing a
// wall-clock startup requirement on a supported platform's Git installation.
type expiredGitContext struct{ context.Context }

func (c expiredGitContext) Err() error {
	if c.Context.Err() != nil {
		return context.DeadlineExceeded
	}
	return nil
}

func TestSourceRPCDeadlineAfterDiscovery(t *testing.T) {
	t.Run("history", func(t *testing.T) { testSourceRPCCancellation(t, "commits", []string{"log"}, true) })
	t.Run("merge-base", func(t *testing.T) { testSourceRPCCancellation(t, "changes-base", []string{"merge-base"}, true) })
}

func testSourceRPCCancellation(t *testing.T, operation string, match []string, deadline bool) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(target, source); err != nil {
		target.Close()
		t.Fatal(err)
	}
	if err = target.Close(); err != nil {
		t.Fatal(err)
	}
	s, repo := fixture(t, "version=1\n")
	plan, err := s.Plan(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	var config []byte
	if len(match) != 0 {
		config = Encode(struct {
			RealGit string
			Match   []string
		}{realGit, match})
	}
	if err = os.WriteFile(filepath.Join(repo, ".git", "ach-test-stalled-git"), config, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ctx context.Context = owner
	want := connect.CodeCanceled
	if deadline {
		ctx, want = expiredGitContext{owner}, connect.CodeDeadlineExceeded
	}
	api := &API{s: s}
	done := make(chan error, 1)
	go func() {
		var err error
		switch operation {
		case "branches":
			_, err = api.ListBranches(ctx, connect.NewRequest(&pb.ListBranchesRequest{WorktreeId: plan.WorktreeID}))
		case "commits":
			_, err = api.ListCommits(ctx, connect.NewRequest(&pb.ListCommitsRequest{WorktreeId: plan.WorktreeID}))
		case "changes", "changes-base":
			base := ""
			if operation == "changes-base" {
				base = "HEAD"
			}
			_, err = api.GetChanges(ctx, connect.NewRequest(&pb.GetChangesRequest{WorktreeId: plan.WorktreeID, Base: base}))
		}
		done <- err
	}()
	watchdog := time.NewTimer(30 * time.Second)
	defer watchdog.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	var child Process
	for child.PID == 0 {
		select {
		case err := <-done:
			t.Fatalf("discovery did not reach Git: %v", err)
		case <-watchdog.C:
			t.Fatal("Git readiness watchdog expired")
		case <-ticker.C:
			data, err := os.ReadFile(filepath.Join(repo, ".git", "ach-test-git-ready"))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			child, err = ProcessIdentity(pid)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	defer func() {
		if ProcessAlive(child) {
			p, _ := os.FindProcess(child.PID)
			_ = p.Kill()
		}
	}()
	cancel()
	select {
	case err := <-done:
		if connect.CodeOf(err) != want {
			t.Fatalf("wrong cancellation response: %v", err)
		}
	case <-watchdog.C:
		t.Fatal("cancelled request left discovery running")
	}
	if ProcessAlive(child) {
		t.Fatal("discovery process was not reaped")
	}
}

func TestWorktreeDiscoveryPreservesExpiredRequestStatus(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	plan, err := s.Plan(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err = (&API{s: s}).ListBranches(ctx, connect.NewRequest(&pb.ListBranchesRequest{WorktreeId: plan.WorktreeID}))
	if connect.CodeOf(err) != connect.CodeDeadlineExceeded {
		t.Fatalf("wrong expired request response: %v", err)
	}
}
