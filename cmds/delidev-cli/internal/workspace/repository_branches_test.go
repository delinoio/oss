// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestRepositoryBranchInventoryCompleteBoundsAndSyntax(t *testing.T) {
	sha := strings.Repeat("a", 40)
	branches, err := parseRepositoryBranches([]byte(sha+"\trefs/heads/feature/topic\n"+sha+"\trefs/heads/main\n"), "origin")
	if err != nil || len(branches) != 2 || branches[0] != "feature/topic" {
		t.Fatal(branches, err)
	}
	for _, raw := range []string{sha + "\trefs/tags/main\n", sha + "\trefs/heads/x..y\n", sha + "\trefs/heads/x.lock\n", sha + "\trefs/heads/a\n" + sha + "\trefs/heads/a\n", "not-sha\trefs/heads/main\n"} {
		if _, err := parseRepositoryBranches([]byte(raw), "origin"); err == nil {
			t.Fatal("accepted malformed inventory")
		}
	}
	var out strings.Builder
	for i := 0; i < domain.MaxRepositoryBranches; i++ {
		fmt.Fprintf(&out, "%s\trefs/heads/b%05d\n", sha, i)
	}
	if names, err := parseRepositoryBranches([]byte(out.String()), "origin"); err != nil || len(names) != domain.MaxRepositoryBranches {
		t.Fatal(len(names), err)
	}
	fmt.Fprintf(&out, "%s\trefs/heads/overflow\n", sha)
	if _, err := parseRepositoryBranches([]byte(out.String()), "origin"); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal(err)
	}
	if _, err := parseRepositoryBranches(bytes.Repeat([]byte("x"), domain.MaxRepositoryBranchesBytes+1), "origin"); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal(err)
	}
}
func TestRepositoryBranchesUseWorkerNativeCredentialProfileWithoutCheckoutWrites(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX Git wrapper fixture")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'" }
	for _, source := range []string{"https://github.com/fixture/repo.git", "git@github.com:fixture/repo.git"} {
		t.Run(source, func(t *testing.T) {
			var logs bytes.Buffer
			root := t.TempDir()
			marker := filepath.Join(root, "calls")
			binary := filepath.Join(root, "git-fixture")
			config := filepath.Join(root, "config")
			if err := os.WriteFile(config, []byte("[credential]\n helper = fixture-helper\n[core]\n sshCommand = fixture-ssh\n"), 0600); err != nil {
				t.Fatal(err, logs.String())
			}
			t.Setenv("GIT_CONFIG_GLOBAL", config)
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			script := "#!/bin/sh\ncase \" $* \" in\n*' ls-remote --heads '*)\n" + "[ \"$GIT_CONFIG_GLOBAL\" = /dev/null ] || exit 13\n[ \"$GIT_CONFIG_COUNT\" = 2 ] || exit 14\n[ \"$GIT_OPTIONAL_LOCKS\" = 0 ] || exit 15\nprintf 'heads\\n' >> " + quote(marker) + "\nprintf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\trefs/heads/main\\n'\n;;\n*' fetch '*|*' clone '*) exit 16 ;;\n*) exec " + quote(realGit) + " \"$@\" ;;\nesac\n"
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err, logs.String())
			}
			identity, err := domain.RepositoryCloneSourceIdentity(source)
			if err != nil {
				t.Fatal(err, logs.String())
			}
			input := domain.RepositoryBranchesInput{ProjectID: domain.NewID(), ProjectRevision: 1, RepositoryID: domain.NewID(), RepositoryRevision: 1, MachineID: domain.NewID(), MachineRevision: 1, Source: source, SourceIdentity: identity, Remote: "upstream"}
			git := Git{Executable: binary, ProcessRoot: filepath.Join(root, "processes"), OwnerID: domain.NewID(), Logger: slog.New(slog.NewJSONHandler(&logs, nil))}
			result, err := git.DiscoverRepositoryBranches(context.Background(), root, input)
			if err != nil {
				t.Fatal(err, logs.String())
			}
			if len(result.Branches) != 1 || result.Remote != "upstream" || result.Validate(input) != nil {
				t.Fatal(result)
			}
			raw, err := os.ReadFile(marker)
			if err != nil || string(raw) != "heads\n" {
				t.Fatal(string(raw), err)
			}
			for _, path := range []string{filepath.Join(root, ".git"), filepath.Join(root, "FETCH_HEAD")} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("unexpected checkout write", path)
				}
			}
			if strings.HasPrefix(source, "https://") {
				if err := os.WriteFile(config, []byte("[url \"https://example.invalid/\"]\n insteadOf = https://github.com/\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := git.DiscoverRepositoryBranches(context.Background(), root, input); domain.SafeError(err).Code != domain.InvalidArgument {
					t.Fatal("source rewrite admitted", err)
				}
				after, _ := os.ReadFile(marker)
				if string(after) != string(raw) {
					t.Fatal("rewritten source reached native discovery")
				}
			}
			if strings.Contains(logs.String(), source) || strings.Contains(logs.String(), "fixture-helper") {
				t.Fatal("private Git content entered logs")
			}
		})
	}
}
