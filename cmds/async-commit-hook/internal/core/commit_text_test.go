package core

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"fmt"
	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCommitSubjectsNormalizeDisplayWithoutChangingObjectIDs(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"true\"\n")
	ctx := context.Background()
	tree, err := Git(ctx, repo, "rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := ResolveCommit(ctx, repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	subject := "bad-\xff Korean 한국어 emoji 🙂"
	raw := fmt.Sprintf("tree %s\nparent %s\nauthor Fixture <fixture@example.invalid> 1 +0000\ncommitter Fixture <fixture@example.invalid> 1 +0000\n\n%s\n", tree, parent, subject)
	cmd := gitCommand(ctx, repo, "hash-object", "-t", "commit", "-w", "--stdin")
	cmd.Stdin = bytes.NewBufferString(raw)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(string(out))
	if _, err = Git(ctx, repo, "update-ref", "HEAD", id); err != nil {
		t.Fatal(err)
	}
	displayed, err := Git(ctx, repo, "log", "-1", "--format=%s")
	if err != nil || utf8.ValidString(displayed) {
		t.Fatal("fixture did not preserve invalid Git bytes", err)
	}
	_, wt, err := s.Init(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	result, err := (&API{s: s}).ListCommits(ctx, connect.NewRequest(&pb.ListCommitsRequest{WorktreeId: wt.ID, Ref: "HEAD"}))
	if err != nil {
		t.Fatal(err)
	}
	c := result.Msg.Commits[0]
	if c.Id != id || len(c.Parents) != 1 || c.Parents[0] != parent || c.Subject != strings.ToValidUTF8(subject, "\uFFFD") {
		t.Fatalf("commit identity or display changed incorrectly: %+v", c)
	}
	if _, err = proto.Marshal(result.Msg); err != nil {
		t.Fatal(err)
	}
	if _, err = protojson.Marshal(result.Msg); err != nil {
		t.Fatal(err)
	}
}
