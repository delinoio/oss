package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestRepositoryPagesBoundLargeRegistryAndPreserveEveryWorktree(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	plan, err := s.Plan(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{plan.WorktreeID: true}
	const count = 2051
	other := ID()
	longPath := filepath.Join(t.TempDir(), strings.Repeat("p", 4000))
	if err := s.Store.Transaction(func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO repositories(id,common_dir,name,local_identity) VALUES(?,?,?,?)", other, filepath.Join(t.TempDir(), "unavailable"), strings.Repeat("\x01", 5000), ID()); err != nil {
			return err
		}
		stmt, err := tx.Prepare("INSERT INTO worktrees(id,repository_id,path,branch) VALUES(?,?,?,?)")
		if err != nil {
			return err
		}
		defer stmt.Close()
		for i := 1; i < count; i++ {
			id, owner := ID(), plan.RepositoryID
			if i > 2000 {
				owner = other
			}
			ids[id] = true
			if _, err := stmt.Exec(id, owner, longPath+id, strings.Repeat("\x01", 5000)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	code, err := s.PairingCode()
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Pair(code, "registry-test")
	if err != nil {
		t.Fatal(err)
	}
	cursor := ""
	seen := map[string]bool{}
	pages := 0
	for {
		payload, err := protojson.Marshal(&pb.ListRepositoriesRequest{Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "http://127.0.0.1:46309/async_commit_hook.v1.LocalService/ListRepositories", bytes.NewReader(payload))
		req.Header.Set("Origin", "https://ach.delino.io")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		if response.Code != 200 || response.Body.Len() >= 8*1024*1024 {
			t.Fatalf("unbounded registry response: status=%d bytes=%d", response.Code, response.Body.Len())
		}
		var page pb.ListRepositoriesResponse
		if err := protojson.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		size := 0
		for _, repository := range page.Repositories {
			if len(repository.Name) > 4096 || !utf8.ValidString(repository.Name) {
				t.Fatal("unbounded repository label")
			}
			for _, tree := range repository.Worktrees {
				size++
				if seen[tree.Id] || !ids[tree.Id] {
					t.Fatal("duplicate or unexpected tree", tree.Id)
				}
				seen[tree.Id] = true
				if len(tree.Path) > 4096 || len(tree.Branch) > 4096 || !utf8.ValidString(tree.Path+tree.Branch) {
					t.Fatal("unbounded worktree labels")
				}
				if tree.Id == plan.WorktreeID && (!tree.Available || tree.Path != plan.Source || tree.Branch != plan.Branch) {
					t.Fatalf("current worktree discovery changed: %#v, expected %s on %s", tree, plan.Source, plan.Branch)
				}
			}
		}
		if size == 0 || size > 50 {
			t.Fatal("wrong worktree page size", size)
		}
		pages++
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			t.Fatal("cursor did not advance")
		}
		cursor = page.NextCursor
	}
	if len(seen) != count || pages != 42 {
		t.Fatal("registry pagination lost history", len(seen), pages)
	}
	var raw string
	if err := s.Store.DB.QueryRow("SELECT path FROM worktrees WHERE repository_id=? LIMIT 1", other).Scan(&raw); err != nil || !strings.HasPrefix(raw, longPath) {
		t.Fatal("display bounding rewrote raw path", err)
	}
}

func TestRepositoryCursorAndLabelsAreBounded(t *testing.T) {
	s, _ := fixture(t, "version=1\n")
	if err := s.Store.DB.Close(); err != nil {
		t.Fatal(err)
	}
	for _, cursor := range []string{"!", strings.Repeat("A", 257), base64.RawURLEncoding.EncodeToString([]byte("registry-v2:" + ID() + ":" + ID())), base64.RawURLEncoding.EncodeToString([]byte("registry-v1:bad:bad"))} {
		_, _, err := s.Store.RepositoryPage(context.Background(), cursor, 50)
		var typed *Error
		if !errors.As(err, &typed) || typed.Code != "invalid-cursor" {
			t.Fatal("invalid cursor reached storage", err)
		}
	}
	for _, limit := range []int{-1, 51} {
		_, _, err := s.Store.RepositoryPage(context.Background(), "", limit)
		var typed *Error
		if !errors.As(err, &typed) || typed.Code != "invalid-page-size" {
			t.Fatal("invalid page size reached storage", err)
		}
	}
	for _, value := range []string{"short", strings.Repeat("界", 2000), strings.Repeat("\xffx", 3000)} {
		got := registryLabel(value)
		if len(got) > 4096 || !utf8.ValidString(got) {
			t.Fatal("invalid bounded label")
		}
		if len(value) < 4096 && got != value {
			t.Fatal("short label changed")
		}
	}
}
