package core

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
	"testing"
)

func TestRepositoryListingObservesCurrentWorktreeBranch(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	ctx := context.Background()
	before, err := s.Submit(ctx, repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.Store.Run(before.RunID)
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		if _, err := Git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	assertBranch := func(want string) {
		t.Helper()
		result, err := (&API{s: s}).ListRepositories(ctx, connect.NewRequest(&pb.ListRepositoriesRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Msg.Repositories) != 1 || len(result.Msg.Repositories[0].Worktrees) != 1 {
			t.Fatal("registration changed")
		}
		w := result.Msg.Repositories[0].Worktrees[0]
		if !w.Available || w.Branch != want {
			t.Fatalf("stale checkout: %+v", w)
		}
	}
	git("checkout", "-qb", "new-branch")
	git("branch", "-D", original.Branch)
	assertBranch("new-branch")
	git("checkout", "--detach", "HEAD")
	assertBranch("")
	stored, err := s.Store.Run(before.RunID)
	if err != nil || stored.Branch != original.Branch {
		t.Fatal("history branch was rewritten", err)
	}
}
