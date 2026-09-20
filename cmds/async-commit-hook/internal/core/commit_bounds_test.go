package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"connectrpc.com/connect"
	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestCommitPagesBoundLargeSubjectsBeforeSerialization(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	ctx := context.Background()
	parent, err := ResolveCommit(ctx, repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := os.Create(filepath.Join(t.TempDir(), "history"))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	// One import builds a full oversized page without 100 native Git launches.
	for i := 0; i < commitPageSize; i++ {
		subject := strings.Repeat("<&>", 32<<10)
		if i == commitPageSize-1 {
			subject = strings.Repeat("x", 12<<20)
		}
		if _, err := fmt.Fprintf(stream, "commit refs/heads/oversized\ncommitter Fixture <fixture@example.invalid> %d +0000\ndata %d\n%s\n", i+1, len(subject), subject); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err := fmt.Fprintf(stream, "from %s\n", parent); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := stream.WriteString("\n"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stream.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	cmd := gitCommand(ctx, repo, "fast-import", "--quiet")
	cmd.Stdin = stream
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("history fixture: %v %s", err, output)
	}
	_, wt, err := s.Init(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&API{s: s}).ListCommits(ctx, connect.NewRequest(&pb.ListCommitsRequest{WorktreeId: wt.ID, Ref: "oversized"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Msg.Commits) != commitPageSize {
		t.Fatalf("lost commits: %d", len(response.Msg.Commits))
	}
	for i, commit := range response.Msg.Commits {
		if !utf8.ValidString(commit.Subject) || len(commit.Subject) > commitSubjectBytes || !strings.HasSuffix(commit.Subject, "…") {
			t.Fatalf("unbounded subject %d: %d bytes", i, len(commit.Subject))
		}
		if len(commit.Parents) != 1 {
			t.Fatalf("lost ancestry %d", i)
		}
		if i+1 < len(response.Msg.Commits) && commit.Parents[0] != response.Msg.Commits[i+1].Id {
			t.Fatal("commit IDs changed")
		}
	}
	wire, err := protojson.Marshal(response.Msg)
	if err != nil || len(wire) >= 5<<20 {
		t.Fatalf("response budget: %d, %v", len(wire), err)
	}
	next, err := Commits(ctx, repo, "oversized", 100)
	if err != nil || len(next) != 1 || next[0].ID != parent || next[0].Subject != "fixture" {
		t.Fatalf("pagination boundary: %+v %v", next, err)
	}
}

func TestCommitStreamBoundsHeadersAndUnicodePrefixes(t *testing.T) {
	id := strings.Repeat("a", 40)
	for _, subject := range []string{"", "tabs\tstay", "invalid-\xff-한국어", strings.Repeat("🙂", 1025), strings.Repeat("x", 4093) + "한국어", strings.Repeat("\x01", 100000), strings.Repeat("\xff", 100000)} {
		page, err := readCommitPage(strings.NewReader(id + "\t\t" + subject + "\n"))
		if err != nil || len(page) != 1 {
			t.Fatalf("read: %+v %v", page, err)
		}
		got := page[0].Subject
		if len(got) > commitSubjectBytes || !utf8.ValidString(got) {
			t.Fatal("invalid bounded Unicode prefix")
		}
		if len(subject) > commitSubjectBytes && !strings.HasSuffix(got, "…") {
			t.Fatal("missing truncation disclosure")
		}
		if len(subject) <= commitSubjectBytes && got != strings.ToValidUTF8(subject, "\uFFFD") {
			t.Fatal("short subject changed")
		}
	}
	for _, input := range []string{id + "\t" + strings.Repeat(id+" ", 500) + "\tsubject\n", id + "\twrong\tsubject\n", id + "\t\tunterminated", strings.Repeat(id+"\t\tx\n", 101)} {
		if _, err := readCommitPage(strings.NewReader(input)); err == nil {
			t.Fatal("accepted malformed/unbounded record")
		}
	}
}
