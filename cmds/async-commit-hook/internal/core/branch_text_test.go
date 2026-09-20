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
	"google.golang.org/protobuf/proto"
)

func TestBranchLabelsNormalizeDisplayWithoutChangingObjectIDs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows Git cannot create arbitrary non-UTF-8 ref path bytes")
	}
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"true\"\n")
	ctx := context.Background()
	id, err := ResolveCommit(ctx, repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	name := "bad-\xff-한국어-🙂"
	// Packed refs can retain arbitrary ref bytes even on filesystems such as
	// APFS that reject the equivalent loose-ref filename.
	if err = os.WriteFile(filepath.Join(repo, ".git", "packed-refs"), []byte(id+" refs/heads/"+name+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	branches, _, err := BranchPage(ctx, repo, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range branches {
		found = found || b.Name == name && b.Commit == id
	}
	if !found {
		t.Fatal("fixture did not preserve raw branch bytes")
	}
	_, wt, err := s.Init(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	result, err := (&API{s: s}).ListBranches(ctx, connect.NewRequest(&pb.ListBranchesRequest{WorktreeId: wt.ID}))
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, b := range result.Msg.Branches {
		found = found || b.Name == strings.ToValidUTF8(name, "\uFFFD") && b.Commit == id
	}
	if !found || len(result.Msg.Branches) != len(branches) {
		t.Fatalf("branch identity or display changed incorrectly: %+v", result.Msg)
	}
	if _, err = proto.Marshal(result.Msg); err != nil {
		t.Fatal(err)
	}
	if _, err = protojson.Marshal(result.Msg); err != nil {
		t.Fatal(err)
	}
}
