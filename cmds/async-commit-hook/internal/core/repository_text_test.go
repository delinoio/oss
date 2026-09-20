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

func assertRepositoryText(t *testing.T, s *Service, repo, worktree, name, path string, available bool) {
	t.Helper()
	response, err := (&API{s: s}).ListRepositories(context.Background(), connect.NewRequest(&pb.ListRepositoriesRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = proto.Marshal(response.Msg); err != nil {
		t.Fatal(err)
	}
	if _, err = protojson.Marshal(response.Msg); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range response.Msg.Repositories {
		if r.Id != repo {
			continue
		}
		if r.Name != strings.ToValidUTF8(name, "\uFFFD") {
			t.Fatal("wrong display name", r)
		}
		for _, w := range r.Worktrees {
			if w.Id == worktree {
				found = true
				if w.Path != strings.ToValidUTF8(path, "\uFFFD") || w.Available != available {
					t.Fatal("wrong worktree display", w)
				}
			}
		}
	}
	if !found {
		t.Fatal("worktree disappeared")
	}
	var rawName, rawPath string
	if err = s.Store.DB.QueryRow("SELECT name FROM repositories WHERE id=?", repo).Scan(&rawName); err != nil {
		t.Fatal(err)
	}
	if err = s.Store.DB.QueryRow("SELECT path FROM worktrees WHERE id=?", worktree).Scan(&rawPath); err != nil {
		t.Fatal(err)
	}
	if rawName != name || rawPath != path {
		t.Fatal("transport changed raw stored identifiers")
	}
}

func TestRepositoryDisplayNormalizesUnavailableRawPathRecords(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	r, w, err := s.Init(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	name, path := "repo-\xff-한국어", filepath.Join(repo, "missing-\xff")
	if _, err = s.Store.DB.Exec("UPDATE repositories SET name=? WHERE id=?", name, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.DB.Exec("UPDATE worktrees SET path=?,branch=? WHERE id=?", path, "branch-\xff", w.ID); err != nil {
		t.Fatal(err)
	}
	assertRepositoryText(t, s, r.ID, w.ID, name, path, false)
}

func TestRepositoryDisplayKeepsNativeRawPathAccess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("native arbitrary path bytes require the Linux filesystem fixture")
	}
	s, repo := fixture(t, "version=1\n")
	path := filepath.Join(filepath.Dir(repo), "repo-\xff-한국어")
	if err := os.Rename(repo, path); err != nil {
		t.Fatal(err)
	}
	r, w, err := s.Init(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	assertRepositoryText(t, s, r.ID, w.ID, filepath.Base(path), path, true)
	if _, err = (&API{s: s}).ListCommits(context.Background(), connect.NewRequest(&pb.ListCommitsRequest{WorktreeId: w.ID, Ref: "HEAD"})); err != nil {
		t.Fatal("raw source access failed", err)
	}
}
