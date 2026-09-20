package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLFSPointerRequiresCompleteBoundedSyntax(t *testing.T) {
	header := "version https://git-lfs.github.com/spec/v1\n"
	oid := "oid sha256:" + strings.Repeat("a", 64) + "\n"
	valid := header + oid + "size 123\n"
	ext := "ext-0-example sha256:" + strings.Repeat("b", 64) + "\n"
	for _, body := range []string{valid, strings.ReplaceAll(valid, "\n", "\r\n"), strings.TrimSuffix(valid, "\n"), header + ext + oid + "size 0\n", strings.Replace(valid, "https://git-lfs.github.com/spec/v1", "https://hawser.github.com/spec/v1", 1), strings.Replace(valid, "https://git-lfs.github.com/spec/v1", "http://git-media.io/v/2", 1)} {
		if !isLFSPointer([]byte(body)) {
			t.Fatal("valid pointer missed", body)
		}
	}
	for _, body := range []string{"", header, header + oid, header + "size 1\n", header + "oid sha256:abc\nsize 1\n", header + oid + "size -1\n", header + oid + "size 9223372036854775808\n", header + ext + ext + oid + "size 1\n", valid + "documentation\n", valid + strings.Repeat(" ", 1024-len(valid)), header + "example text\n", strings.Replace(valid, "sha256:", "sha1:", 1)} {
		if isLFSPointer([]byte(body)) {
			t.Fatal("ordinary or malformed content classified as LFS", body)
		}
	}
}

func TestLFSHeaderExamplesRemainByteExactSource(t *testing.T) {
	header := []byte("version https://git-lfs.github.com/spec/v1\n")
	for _, body := range [][]byte{header, append(bytes.Clone(header), []byte("oid sha256:example\nsize not-a-number\n")...), append(bytes.Clone(header), bytes.Repeat([]byte("documentation\n"), 1024)...)} {
		s, repo := fixture(t, "version=1\n")
		if err := os.WriteFile(filepath.Join(repo, "format-example.txt"), body, 0600); err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		for _, args := range [][]string{{"add", "."}, {"-c", "commit.gpgsign=false", "commit", "-qm", "pointer example"}} {
			if _, err := Git(ctx, repo, args...); err != nil {
				t.Fatal(err)
			}
		}
		plan, err := s.Plan(ctx, repo, "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		workspace, err := s.Store.Prepare(ctx, plan)
		if err != nil {
			t.Fatal("ordinary source refused", err)
		}
		got, err := os.ReadFile(filepath.Join(workspace, "format-example.txt"))
		if err != nil || !bytes.Equal(got, body) {
			t.Fatal("raw example changed", err)
		}
	}
}
