package core

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"connectrpc.com/connect"
	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
	"google.golang.org/protobuf/proto"
)

func TestChangesNormalizesRawGitBytesAfterTruncation(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid-path-and-content", true: "split-rune-at-limit"}[large], func(t *testing.T) {
			s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"unused\"\n")
			ctx := context.Background()
			git := func(args ...string) string {
				t.Helper()
				v, err := Git(ctx, repo, args...)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			base := git("rev-parse", "HEAD")
			name, contents := "text", "invalid: \xff\n"
			if runtime.GOOS == "linux" && !large {
				name = "path-\xff"
			}
			if large {
				contents = strings.Repeat("🙂", 600000) + "\n"
			}
			if err := os.WriteFile(filepath.Join(repo, name), []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			git("add", ".")
			git("-c", "commit.gpgsign=false", "commit", "-qm", "diff fixture")
			raw := git("diff", "--no-ext-diff", "--no-textconv", "--no-color", base, "HEAD", "--")
			if large && utf8.ValidString(raw[:2*1024*1024]) {
				// Shift the content boundary by one byte when Git's header happens to align.
				if err := os.WriteFile(filepath.Join(repo, name), []byte("x"+contents), 0600); err != nil {
					t.Fatal(err)
				}
				git("add", ".")
				git("-c", "commit.gpgsign=false", "commit", "-qm", "shift rune")
				raw = git("diff", "--no-ext-diff", "--no-textconv", "--no-color", base, "HEAD", "--")
			}
			if large {
				raw = raw[:2*1024*1024]
			}
			if utf8.ValidString(raw) {
				t.Fatal("fixture must contain invalid UTF-8 at transport boundary")
			}
			_, wt, err := s.Init(ctx, repo)
			if err != nil {
				t.Fatal(err)
			}
			result, err := (&API{s: s}).GetChanges(ctx, connect.NewRequest(&pb.GetChangesRequest{WorktreeId: wt.ID, Ref: "HEAD", Base: base}))
			if err != nil {
				t.Fatal(err)
			}
			if !utf8.ValidString(result.Msg.Diff) || result.Msg.Truncated != large || !strings.ContainsRune(result.Msg.Diff, utf8.RuneError) {
				t.Fatalf("invalid diff result: truncated=%v", result.Msg.Truncated)
			}
			if _, err := proto.Marshal(result.Msg); err != nil {
				t.Fatal(err)
			}
		})
	}
}
