// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestManagedCloneRemoteHeadObservation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX owned-command fixture")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, outcome string
		wantError     bool
	}{
		{"absent", "exit 1", false},
		{"fatal", "exit 128", true},
		{"authentication", "printf 'Authentication failed\\n' >&2; exit 1", true},
		{"valid", "printf 'refs/remotes/origin/topic/nested\\n'", false},
		{"foreign", "printf 'refs/remotes/other/main\\n'", true},
		{"malformed", "printf 'refs/remotes/origin/bad..name\\n'", true},
		{"empty", "printf 'refs/remotes/origin/\\n'", true},
		{"self", "printf 'refs/remotes/origin/HEAD\\n'", true},
		{"multiple", "printf 'refs/remotes/origin/main\\nrefs/remotes/origin/other\\n'", true},
		{"timeout", "exec sleep 30", true},
		{"canceled", "touch \"$2/entered\"; exec sleep 30", true},
		{"launch", "exit 0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := repository(t)
			wrapper := filepath.Join(t.TempDir(), "git-fixture")
			beforeHead := "exit 0"
			if tc.name == "launch" {
				beforeHead = "rm -- \"$0\"; exit 0"
			}
			script := "#!/bin/sh\nprintf '%s\\n' \"$7\" >> \"$2/commands\"\ncase \"$7\" in\nremote) exit 0;;\nfor-each-ref) " + beforeHead + ";;\nsymbolic-ref) if [ \"$8\" = --quiet ]; then " + tc.outcome + "; else printf '%s %s\\n' \"$8\" \"$9\" >> \"$2/aliases\"; fi;;\ncheck-ref-format) exec '" + realGit + "' \"$@\";;\n*) exit 128;;\nesac\n"
			if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			g := Git{Executable: wrapper, ProcessRoot: filepath.Join(t.TempDir(), "processes"), OwnerID: domain.NewID(), Timeout: 2 * time.Second, cloneDiagnostics: true}
			spec := RepositorySpec{PreferredRemote: "upstream", Starting: domain.Reference{Type: domain.RemoteBranch, Remote: "secondary", Name: "main"}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.name == "canceled" {
				go func() {
					for ctx.Err() == nil {
						if _, err := os.Stat(filepath.Join(root, "entered")); err == nil {
							cancel()
							return
						}
						time.Sleep(5 * time.Millisecond)
					}
				}()
			}
			err := provisionManagedCloneRemotes(ctx, g, root, "https://example.invalid/fixed.git", spec, "origin")
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v, want error=%v", err, tc.wantError)
			}
			aliases, _ := os.ReadFile(filepath.Join(root, "aliases"))
			if tc.name == "valid" {
				want := "refs/remotes/upstream/HEAD refs/remotes/upstream/topic/nested\nrefs/remotes/secondary/HEAD refs/remotes/secondary/topic/nested\n"
				if string(aliases) != want {
					t.Fatalf("aliases: %q", aliases)
				}
			} else if len(aliases) != 0 {
				t.Fatalf("failed/absent observation published aliases: %q", aliases)
			}
			commands, _ := os.ReadFile(filepath.Join(root, "commands"))
			if strings.Contains(string(commands), "clone") || strings.Contains(string(commands), "fetch") {
				t.Fatal("observation retried network work")
			}
			if tc.name == "authentication" && domain.SafeError(err).Code != domain.PermissionDenied {
				t.Fatalf("authentication classification lost: %v", err)
			}
			if tc.name == "fatal" && domain.SafeError(err).Cause != "git_exit" {
				t.Fatalf("fatal classification lost: %v", err)
			}
			if tc.name == "canceled" && domain.SafeError(err).Code != domain.Canceled {
				t.Fatalf("cancellation classification lost: %v", err)
			}
		})
	}
}

func TestManagedCloneRemoteHeadFailureBlocksReady(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX owned-command fixture")
	}
	m, input, marker := managedCloneFixture(t)
	original := m.Git.Executable
	wrapper := filepath.Join(t.TempDir(), "git-fixture")
	script := "#!/bin/sh\nfor arg in \"$@\"; do if [ \"$arg\" = symbolic-ref ]; then exit 128; fi; done\nexec '" + original + "' \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	m.Git.Executable = wrapper
	input.Repositories[0].Starting = domain.Reference{Type: domain.RemoteBranch, Remote: "upstream", Name: "main"}
	manifest, err := m.Prepare(context.Background(), input)
	if err == nil || manifest.State == "ready" || domain.SafeError(err).Cause != "git_exit" {
		t.Fatalf("failed HEAD admitted readiness: %+v %v", manifest, err)
	}
	raw, _ := os.ReadFile(marker)
	if string(raw) != "invoked" {
		t.Fatalf("clone repeated/absent: %q", raw)
	}
}
