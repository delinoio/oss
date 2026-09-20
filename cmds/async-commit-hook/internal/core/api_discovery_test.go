package core

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
)

// Re-execute the test binary as a stalled Git executable. No native shell,
// external child or real user repository is involved in this cancellation test.
func init() {
	if len(os.Args) < 2 || os.Args[1] != "--no-replace-objects" {
		return
	}
	for i := 2; i+1 < len(os.Args); i++ {
		if os.Args[i] != "-C" {
			continue
		}
		root := os.Args[i+1]
		if _, err := os.Stat(filepath.Join(root, ".git", "ach-test-stalled-git")); err != nil {
			return
		}
		if err := os.WriteFile(filepath.Join(root, ".git", "ach-test-git-ready"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(97)
		}
		for {
			time.Sleep(time.Hour)
		}
	}
}

func TestSourceRPCRequestCancellationReapsDiscovery(t *testing.T) {
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
	for _, operation := range []string{"branches", "commits", "changes"} {
		t.Run(operation, func(t *testing.T) {
			s, repo := fixture(t, "version=1\n")
			plan, err := s.Plan(context.Background(), repo, "")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(repo, ".git", "ach-test-stalled-git"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			api := &API{s: s}
			done := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "branches":
					_, err = api.ListBranches(ctx, connect.NewRequest(&pb.ListBranchesRequest{WorktreeId: plan.WorktreeID}))
				case "commits":
					_, err = api.ListCommits(ctx, connect.NewRequest(&pb.ListCommitsRequest{WorktreeId: plan.WorktreeID}))
				case "changes":
					_, err = api.GetChanges(ctx, connect.NewRequest(&pb.GetChangesRequest{WorktreeId: plan.WorktreeID}))
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
				if connect.CodeOf(err) != connect.CodeCanceled {
					t.Fatalf("wrong cancellation response: %v", err)
				}
			case <-watchdog.C:
				t.Fatal("cancelled request left discovery running")
			}
			if ProcessAlive(child) {
				t.Fatal("discovery process was not reaped")
			}
		})
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
