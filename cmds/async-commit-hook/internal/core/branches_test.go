package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestBranchPagesBoundLargeListingsAndRetainCoverage(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"echo test\"\n")
	ctx := context.Background()
	sha, err := ResolveCommit(ctx, repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	_, wt, err := s.Init(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	writeRefs := func(count, length int) {
		t.Helper()
		var packed strings.Builder
		for i := 0; i < count; i++ {
			fmt.Fprintf(&packed, "%s refs/heads/page-%04d-%s\n", sha, i, strings.Repeat("a", length))
		}
		if count > 2000 && packed.Len() < 8<<20 {
			t.Fatal("fixture must exceed the API response budget")
		}
		if err := os.WriteFile(filepath.Join(repo, ".git", "packed-refs"), []byte(packed.String()), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeRefs(2200, 4096)
	api := &API{s: s}
	result, err := api.ListBranches(ctx, connect.NewRequest(&pb.ListBranchesRequest{WorktreeId: wt.ID}))
	if err != nil {
		t.Fatal(err)
	}
	b, err := protojson.Marshal(result.Msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 1<<20 || len(result.Msg.Branches) >= 50 || result.Msg.NextCursor == "" {
		t.Fatalf("unbounded response: %d bytes, %d branches", len(b), len(result.Msg.Branches))
	}
	writeRefs(103, 20)
	cursor := ""
	seen := map[string]bool{}
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("pagination did not progress")
		}
		result, err := api.ListBranches(ctx, connect.NewRequest(&pb.ListBranchesRequest{WorktreeId: wt.ID, Cursor: cursor, Limit: 25}))
		if err != nil {
			t.Fatal(err)
		}
		for _, branch := range result.Msg.Branches {
			if seen[branch.Name] || branch.Commit != sha {
				t.Fatalf("invalid or duplicate branch: %+v", branch)
			}
			seen[branch.Name] = true
		}
		cursor = result.Msg.NextCursor
		if cursor == "" {
			break
		}
		if _, _, err := BranchPage(ctx, t.TempDir(), cursor, 25); err == nil {
			t.Fatal("accepted cursor in another worktree")
		}
	}
	if len(seen) != 104 {
		t.Fatalf("lost branches: %d", len(seen))
	}
	for _, cursor := range []string{"garbage", strings.Repeat("x", 2*branchRecordLimit+1), base64.RawURLEncoding.EncodeToString([]byte("branches-v1:" + Hash([]byte(repo)) + "\x00refs/heads/a\n"))} {
		if _, _, err := BranchPage(ctx, repo, cursor, 0); err == nil {
			t.Fatal("accepted malformed cursor")
		}
	}
	if _, _, err := BranchPage(ctx, repo, "", 51); err == nil {
		t.Fatal("accepted unbounded limit")
	}
	// One excessively long record is rejected without buffering the listing.
	writeRefs(1, branchRecordLimit)
	if _, _, err := BranchPage(ctx, repo, "", 0); err == nil {
		t.Fatal("accepted oversized record")
	}
}
