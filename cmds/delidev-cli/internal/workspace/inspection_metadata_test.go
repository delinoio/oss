// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestInspectionGitHubURLSafety(t *testing.T) {
	for _, value := range []string{"https://github.com/Owner/repo.git", "ssh://git@github.com/Owner/repo.git", "git@github.com:Owner/repo", "https://github.com/Owner/repo"} {
		got, ok := parseInspectionGitHub(value)
		if !ok || got != (GitHubRepository{Owner: "Owner", Name: "repo"}) {
			t.Fatalf("supported form %q: %+v %v", value, got, ok)
		}
	}
	for _, value := range []string{"http://github.com/o/r", "https://token@github.com/o/r", "https://git@github.com/o/r", "ssh://user@github.com/o/r", "ssh://git@github.com:22/o/r", "https://github.com:443/o/r", "https://github.com.evil/o/r", "https://github.com/o/r/extra", "https://github.com/o/r/", "https://github.com/o/r?x", "https://github.com/o/r#x", "https://github.com/o/%72", "https://github.com/o/r\n", "https://github.com/o/r\x00", "https://github.com/o/..", "https://github.com/o/r\nhttps://github.com/o/r"} {
		if _, ok := parseInspectionGitHub(value); ok {
			t.Fatalf("unsafe form accepted %q", value)
		}
	}
}

func TestInspectionGitHubMetadataBoundsAndClosedDecoding(t *testing.T) {
	value := Inspection{Remotes: []string{"origin"}, GitHubRepositories: map[string]GitHubRepository{"origin": {Owner: "owner", Name: "repo"}}}
	if err := value.ValidateGitHubRepositories(); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"root":"/repo","name":"repo","remotes":["origin"],"default_refs":{},"github_repositories":{"origin":{"owner":"owner","name":"repo","url":"private"}}}`, `{"github_repositories":{"origin":{"owner":"owner","name":"repo"}},"raw_url":"private"}`} {
		var decoded Inspection
		if domain.Decode([]byte(raw), &decoded) == nil {
			t.Fatal("unknown/raw URL fields accepted")
		}
	}
	value.GitHubRepositories = map[string]GitHubRepository{"foreign": {Owner: "owner", Name: "repo"}}
	if value.ValidateGitHubRepositories() == nil {
		t.Fatal("foreign remote accepted")
	}
	value.GitHubRepositories = map[string]GitHubRepository{"origin": {Owner: "bad/owner", Name: "repo"}}
	if value.ValidateGitHubRepositories() == nil {
		t.Fatal("invalid identity accepted")
	}
	value.GitHubRepositories = map[string]GitHubRepository{}
	for n := 0; n < 129; n++ {
		value.GitHubRepositories[strings.Repeat("a", n+1)] = GitHubRepository{Owner: "owner", Name: "repo"}
	}
	if value.ValidateGitHubRepositories() == nil {
		t.Fatal("129-entry map accepted")
	}
}

func TestInspectionMetadataUsesEffectiveURLsWithoutChangingGit(t *testing.T) {
	root := repository(t)
	gitTest(t, root, "remote", "add", "origin", "gh-test:owner/repo.git")
	gitTest(t, root, "config", "url.https://github.com/.insteadOf", "gh-test:")
	gitTest(t, root, "remote", "add", "ambiguous", "https://github.com/owner/first")
	gitTest(t, root, "config", "--add", "remote.ambiguous.url", "https://github.com/owner/second")
	gitTest(t, root, "remote", "add", "helper", "sentinel::private")
	marker := filepath.Join(t.TempDir(), "helper-invoked")
	helper := filepath.Join(t.TempDir(), "git-remote-sentinel")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf invoked > '"+marker+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(helper)+string(os.PathListSeparator)+os.Getenv("PATH"))
	gitTest(t, root, "config", "credential.helper", helper)
	nested := filepath.Join(root, "nested with spaces")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	before := gitTest(t, root, "show-ref")
	indexBefore, _ := os.ReadFile(filepath.Join(root, ".git", "index"))
	var logs bytes.Buffer
	g := Git{ProcessRoot: filepath.Join(t.TempDir(), "processes"), OwnerID: domain.NewID(), Logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	inspection, err := g.Inspect(context.Background(), nested)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.GitHubRepositories != nil {
		t.Fatal("legacy inspection enriched itself")
	}
	if err := g.EnrichInspection(context.Background(), &inspection); err != nil {
		t.Fatal(err)
	}
	expectedRoot, _ := filepath.EvalSymlinks(root)
	if inspection.Root != expectedRoot || !reflect.DeepEqual(inspection.GitHubRepositories, map[string]GitHubRepository{"origin": {Owner: "owner", Name: "repo"}}) {
		t.Fatalf("wrong effective metadata %+v", inspection)
	}
	raw, _ := json.Marshal(inspection)
	for _, private := range []string{"https://github.com/", "gh-test:", "sentinel::private"} {
		if bytes.Contains(raw, []byte(private)) || strings.Contains(logs.String(), private) {
			t.Fatal("raw remote escaped Worker parser")
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("credential or remote helper was invoked")
	}
	indexAfter, _ := os.ReadFile(filepath.Join(root, ".git", "index"))
	if !bytes.Equal(indexBefore, indexAfter) || before != gitTest(t, root, "show-ref") {
		t.Fatal("inspection changed Git state")
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "FETCH_HEAD")); !os.IsNotExist(err) {
		t.Fatal("inspection fetched")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if g.EnrichInspection(ctx, &inspection) == nil {
		t.Fatal("cancellation became missing metadata")
	}
}
