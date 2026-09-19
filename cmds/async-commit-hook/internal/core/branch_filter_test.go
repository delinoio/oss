package core

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
)

func TestDetachedRunFilterAndCursorScope(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	var worktree string
	for _, branch := range []string{"", "main", "", "feature"} {
		r, err := s.Plan(context.Background(), repo, "")
		if err != nil {
			t.Fatal(err)
		}
		r.Branch = branch
		worktree = r.WorktreeID
		if _, err := s.Store.InsertRun(&r, ""); err != nil {
			t.Fatal(err)
		}
	}
	a := &API{s: s}
	get := func(branch string, detached bool, cursor string, limit uint32) (*connect.Response[pb.ListRunsResponse], error) {
		return a.ListRuns(context.Background(), connect.NewRequest(&pb.ListRunsRequest{WorktreeId: worktree, Branch: branch, Detached: detached, Cursor: cursor, Limit: limit}))
	}
	all, err := get("", false, "", 50)
	if err != nil || len(all.Msg.Runs) != 4 {
		t.Fatal("unfiltered runs missing", err)
	}
	first, err := get("", true, "", 1)
	if err != nil || len(first.Msg.Runs) != 1 || first.Msg.Runs[0].Branch != "" || first.Msg.NextCursor == "" {
		t.Fatal("incorrect detached page", err)
	}
	second, err := get("", true, first.Msg.NextCursor, 1)
	if err != nil || len(second.Msg.Runs) != 1 || second.Msg.Runs[0].Branch != "" || second.Msg.Runs[0].Id == first.Msg.Runs[0].Id || second.Msg.NextCursor != "" {
		t.Fatal("incorrect detached pagination", err)
	}
	if _, err := get("", false, first.Msg.NextCursor, 1); err == nil {
		t.Fatal("detached cursor accepted without its filter")
	}
	if _, err := get("main", true, "", 50); err == nil {
		t.Fatal("contradictory filters accepted")
	}
	named, err := get("main", false, "", 50)
	if err != nil || len(named.Msg.Runs) != 1 || named.Msg.Runs[0].Branch != "main" {
		t.Fatal("named filter failed", err)
	}
}
