package core

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCommittedSymlinksHaveDeterministicPlatformRepresentation(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.ok]\ncommand=\"exit 0\"\n")
	ctx := context.Background()
	targets := map[string]string{"link": "source.txt", "dangling": "missing-target", "outside": "../outside-target", "unicode": "한국어/🙂"}
	// Construct index entries directly: this fixture itself needs no symlink
	// privilege on Windows and never creates or follows an external target.
	for name, target := range targets {
		cmd := gitCommand(ctx, repo, "hash-object", "-w", "--stdin")
		cmd.Stdin = strings.NewReader(target)
		raw, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		oid := strings.TrimSpace(string(raw))
		if _, err := Git(ctx, repo, "update-index", "--add", "--cacheinfo", "120000,"+oid+","+name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Git(ctx, repo, "-c", "commit.gpgsign=false", "commit", "-qm", "symlinks"); err != nil {
		t.Fatal(err)
	}
	plan, err := s.Plan(ctx, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := s.Store.Prepare(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	verify := func(dir string, representation sourceRepresentation) {
		t.Helper()
		for name, target := range targets {
			path := filepath.Join(dir, name)
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if representation == symlinkTextFiles {
				if !info.Mode().IsRegular() {
					t.Fatal("Windows representation requires regular files")
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != target {
					t.Fatalf("link text changed: %q %v", got, err)
				}
			} else {
				got, err := os.Readlink(path)
				if err != nil || got != target {
					t.Fatalf("native link changed: %q %v", got, err)
				}
			}
		}
	}
	verify(workspace, sourceRepresentationForOS(runtime.GOOS))
	if runtime.GOOS == "windows" {
		setting, err := Git(ctx, workspace, "config", "--get", "core.symlinks")
		if err != nil || setting != "false" {
			t.Fatalf("managed Git representation: %q %v", setting, err)
		}
	}
	// Exercise the Windows materializer on every host, independently of whether
	// the test account can create privileged links.
	plain := t.TempDir()
	root, err := os.OpenRoot(plain)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := materializeBlobsWithRepresentation(ctx, repo, root, plan.Commit, symlinkTextFiles); err != nil {
		t.Fatal(err)
	}
	verify(plain, symlinkTextFiles)
	run := runFixture(t, s, repo)
	if run.State != Passed {
		t.Fatalf("symlink source never executed: %+v", run)
	}
	if runtime.GOOS == "windows" {
		// A legacy native-link result must not become an inherited success under
		// the new source representation, or prepare new source under its old hash.
		run.Fingerprint = "legacy-native-link-context"
		if err := s.Store.SaveRun(run); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Rerun(run.ID, false); err == nil {
			t.Fatal("inherited legacy representation")
		}
		plan.Fingerprint = run.Fingerprint
		plan.ID = ID()
		if _, err := s.Store.Prepare(ctx, plan); err == nil {
			t.Fatal("prepared new representation under an old fingerprint")
		}
	}
}

func TestSourceRepresentationSeparatesWindowsLegacyFingerprints(t *testing.T) {
	p, err := ParseProject([]byte("version=1\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, osName := range []string{"darwin", "linux", "windows"} {
		legacy := Hash(Encode(struct {
			Project     Project
			Environment map[string]string
			OS, Arch    string
		}{p, nil, osName, "amd64"}))
		current := fingerprintForPlatform(p, nil, osName, "amd64")
		if (legacy == current) != (osName != "windows") {
			t.Fatal("incorrect compatibility boundary", osName)
		}
		r := Run{Config: p, Fingerprint: current}
		if err := validateSourceRepresentation(r, osName, "amd64"); err != nil {
			t.Fatal(err)
		}
		r.Fingerprint = legacy
		if (validateSourceRepresentation(r, osName, "amd64") != nil) != (osName == "windows") {
			t.Fatal("legacy representation guard", osName)
		}
	}
}
