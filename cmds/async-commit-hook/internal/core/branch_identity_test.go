package core

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestBranchIdentitiesPreserveCollidingLabelsAndUnavailableHistory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires arbitrary Git ref bytes")
	}
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand='unused'\n")
	ctx := context.Background()
	base, err := ResolveCommit(ctx, repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(repo, "changed"), []byte("change"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "changed"}, {"commit", "--quiet", "-m", "second"}} {
		if _, err = Git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	head, err := ResolveCommit(ctx, repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"bad-\xfe", "bad-\xff"}
	commits := []string{base, head}
	packed := ""
	wantRuns := map[string]string{}
	var worktree string
	for i, name := range names {
		packed += commits[i] + " refs/heads/" + name + "\n"
		plan, err := s.Plan(ctx, repo, commits[i])
		if err != nil {
			t.Fatal(err)
		}
		plan.Branch = name
		worktree = plan.WorktreeID
		if _, err = s.Store.InsertRun(&plan, ""); err != nil {
			t.Fatal(err)
		}
		wantRuns[commits[i]] = plan.ID
	}
	if err = os.WriteFile(filepath.Join(repo, ".git", "packed-refs"), []byte(packed), 0600); err != nil {
		t.Fatal(err)
	}
	api := &API{s: s}
	branches, err := api.ListBranches(ctx, connect.NewRequest(&pb.ListBranchesRequest{WorktreeId: worktree}))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, branch := range branches.Msg.Branches {
		if branch.Name != "bad-�" {
			continue
		}
		if branch.Id == "" || ids[branch.Id] {
			t.Fatal("colliding branch identity", branch)
		}
		ids[branch.Id] = true
		history, err := api.ListCommits(ctx, connect.NewRequest(&pb.ListCommitsRequest{WorktreeId: worktree, BranchId: branch.Id}))
		if err != nil {
			t.Fatal(err)
		}
		if len(history.Msg.Commits) == 0 || history.Msg.Commits[0].Id != branch.Commit {
			t.Fatal("wrong commit", history)
		}
		changes, err := api.GetChanges(ctx, connect.NewRequest(&pb.GetChangesRequest{WorktreeId: worktree, BranchId: branch.Id, Base: base}))
		if err != nil {
			t.Fatal(err)
		}
		if changes.Msg.Head != branch.Commit {
			t.Fatal("wrong diff", changes)
		}
		runs, err := api.ListRuns(ctx, connect.NewRequest(&pb.ListRunsRequest{WorktreeId: worktree, BranchId: branch.Id}))
		if err != nil {
			t.Fatal(err)
		}
		if len(runs.Msg.Runs) != 1 || runs.Msg.Runs[0].Id != wantRuns[branch.Commit] {
			t.Fatal("wrong raw history", runs)
		}
	}
	if len(ids) != 2 {
		t.Fatal("normalized labels collapsed", ids)
	}
	if err = os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/"+names[0]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	repos, err := api.ListRepositories(ctx, connect.NewRequest(&pb.ListRepositoriesRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	tree := repos.Msg.Repositories[0].Worktrees[0]
	if !ids[tree.BranchId] || tree.Branch != "bad-�" {
		t.Fatal("checkout identity lost", tree)
	}
	if _, err = protojson.Marshal(repos.Msg); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(repo, repo+"-moved"); err != nil {
		t.Fatal(err)
	}
	runs, err := api.ListRuns(ctx, connect.NewRequest(&pb.ListRunsRequest{WorktreeId: worktree, BranchId: tree.BranchId}))
	if err != nil || len(runs.Msg.Runs) != 1 || runs.Msg.Runs[0].Id != wantRuns[base] {
		t.Fatal("unavailable source blocked history", runs, err)
	}
}

func TestBranchIdentityRejectsInvalidOrConflictingScope(t *testing.T) {
	tree := ID()
	for _, id := range []string{"!", strings.Repeat("A", 2*branchRecordLimit+1), branchIdentity(ID(), "main"), branchIdentity(tree, "bad\x00ref"), branchIdentity(tree, strings.Repeat("x", branchRecordLimit+1))} {
		if _, err := branchSelection(tree, id, "", false); err == nil {
			t.Fatal("invalid identity accepted")
		}
	}
	if _, err := branchSelection(tree, branchIdentity(tree, "main"), "main", false); err == nil {
		t.Fatal("conflicting selectors accepted")
	}
	if got, err := branchSelection(tree, "", "legacy", true); err != nil || got != "legacy" {
		t.Fatal(got, err)
	}
}
