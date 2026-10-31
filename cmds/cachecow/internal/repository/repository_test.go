// SPDX-License-Identifier: Apache-2.0
package repository

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("git %v: %s %v", args, b, e)
	}
}
func fixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "primary 한 글")
	if e := os.Mkdir(p, 0700); e != nil {
		t.Fatal(e)
	}
	runGit(t, p, "init")
	runGit(t, p, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "init")
	return p
}
func TestDiscoveryAndSourcePrecedence(t *testing.T) {
	ctx := context.Background()
	p := fixture(t)
	w := filepath.Join(t.TempDir(), "linked space 한")
	runGit(t, p, "worktree", "add", "-b", "linked", w)
	r, e := Discover(ctx, w)
	if e != nil {
		t.Fatal(e)
	}
	primary, e := Discover(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	if r.CommonDir != primary.CommonDir || r.Primary != primary.Root || r.ID == primary.ID {
		t.Fatalf("bad identity: %+v %+v", r, primary)
	}
	sub := filepath.Join(w, "sub")
	os.Mkdir(sub, 0700)
	nested, e := Discover(ctx, sub)
	if e != nil || nested.ID != r.ID {
		t.Fatalf("unstable identity %v", e)
	}
	for _, tc := range []struct {
		explicit, saved string
		origin          SourceOrigin
		self            bool
	}{{"", "", Primary, false}, {"", w, Saved, true}, {p, w, Invocation, false}} {
		s, e := SelectSource(ctx, r, tc.explicit, tc.saved)
		if e != nil || s.Origin != tc.origin || s.Self != tc.self {
			t.Fatalf("source: %+v %v", s, e)
		}
	}
	other := fixture(t)
	if _, e = SelectSource(ctx, r, other, p); e == nil {
		t.Fatal("accepted unrelated repository")
	}
	if _, e = SelectSource(ctx, r, filepath.Join(w, "missing"), p); e == nil {
		t.Fatal("invalid explicit source fell back")
	}
	t.Setenv("GIT_DIR", primary.GitDir)
	actual, e := Discover(ctx, w)
	if e != nil || actual.ID != r.ID {
		t.Fatalf("ambient Git override: %v", e)
	}
}
func TestBareAndProtectedPaths(t *testing.T) {
	ctx := context.Background()
	p := fixture(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	runGit(t, p, "clone", "--bare", p, bare)
	w := filepath.Join(t.TempDir(), "worktree")
	runGit(t, bare, "worktree", "add", w)
	r, e := Discover(ctx, w)
	if e != nil {
		t.Fatal(e)
	}
	if r.Primary != "" {
		t.Fatal("invented primary")
	}
	if _, e = SelectSource(ctx, r, "", ""); e == nil {
		t.Fatal("bare default accepted")
	}
	if _, e = SelectSource(ctx, r, w, ""); e != nil {
		t.Fatal(e)
	}
	b, e := Discover(ctx, bare)
	if e != nil || b.Kind != Bare {
		t.Fatalf("bare: %+v %v", b, e)
	}
	tracked := filepath.Join(w, "source.txt")
	os.WriteFile(tracked, []byte("source"), 0600)
	runGit(t, w, "add", "source.txt")
	settings := filepath.Join(w, ".ccow")
	os.Mkdir(settings, 0700)
	for _, bad := range []string{w, filepath.Dir(w), r.CommonDir, r.GitDir, filepath.Join(w, ".git"), tracked, settings} {
		if _, e = r.ValidateCache(ctx, bad, settings); e == nil {
			t.Errorf("accepted protected %s", bad)
		}
	}
	if _, e = r.ValidateCache(ctx, filepath.Join(w, "target"), settings); e != nil {
		t.Fatal(e)
	}
	alias := filepath.Join(w, "alias")
	if e = os.Symlink(settings, alias); e == nil {
		if _, e = r.ValidateCache(ctx, filepath.Join(alias, "nested"), settings); e == nil {
			t.Fatal("accepted symlink escape")
		}
	}
}
func TestCanonicalAndOwnership(t *testing.T) {
	p := t.TempDir()
	link := filepath.Join(p, "link")
	if e := os.Symlink(p, link); e != nil {
		t.Skip(e)
	}
	a, e := Canonical(filepath.Join(link, "missing", "한"))
	if e != nil {
		t.Fatal(e)
	}
	b, e := Canonical(filepath.Join(p, "missing", "한"))
	if e != nil || a != b {
		t.Fatal("aliases differ")
	}
	dangling := filepath.Join(p, "dangling")
	os.Symlink(filepath.Join(p, "absent"), dangling)
	if _, e = Canonical(dangling); e == nil {
		t.Fatal("accepted dangling link")
	}
	if e = CheckOwner(filepath.Join(p, "absent")); e != nil {
		t.Fatal(e)
	}
	if (runtime.GOOS == "darwin" || runtime.GOOS == "linux") && os.Geteuid() != 0 {
		if e = CheckOwner(string(filepath.Separator)); e == nil {
			t.Fatal("accepted other user's root")
		}
	}
}

func TestProtectOtherWorktreeAndUntrackedSource(t *testing.T) {
	ctx := context.Background()
	p := fixture(t)
	w := filepath.Join(t.TempDir(), "other")
	runGit(t, p, "worktree", "add", "-b", "other", w)
	os.WriteFile(filepath.Join(p, "untracked.txt"), []byte("keep"), 0600)
	r, e := Discover(ctx, w)
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{filepath.Join(p, "untracked.txt"), filepath.Join(p, ".git"), filepath.Join(p, ".ccow")} {
		if _, e = r.ValidateCache(ctx, path); e == nil {
			t.Errorf("accepted unrelated source %s", path)
		}
	}
	// A build cache must be explicitly ignored so unrelated untracked source is
	// never silently promoted into deletable cache storage.
	os.WriteFile(filepath.Join(w, ".gitignore"), []byte("/target/\n"), 0600)
	os.Mkdir(filepath.Join(w, "target"), 0700)
	os.WriteFile(filepath.Join(w, "target", "artifact"), []byte("cache"), 0600)
	if _, e = r.ValidateCache(ctx, filepath.Join(w, "target")); e != nil {
		t.Fatal(e)
	}
}

func TestFilesystemOnlyVerification(t *testing.T) {
	ctx := context.Background()
	p := fixture(t)
	w := filepath.Join(t.TempDir(), "linked")
	runGit(t, p, "worktree", "add", "-b", "linked", w)
	r, e := Discover(ctx, w)
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", t.TempDir()) // No Git or tool subprocess can run from this point.
	if e = Verify(r); e != nil {
		t.Fatal(e)
	}
	cache, e := Canonical(filepath.Join(w, "target"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyCacheClaim(r, cache); e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyCacheClaim(r, r.Root); e == nil {
		t.Fatal("accepted checkout as cache")
	}
	forged := r
	forged.CommonDir = r.GitDir
	if e = Verify(forged); e == nil {
		t.Fatal("accepted changed common directory")
	}
	forged = r
	forged.ID = "forged"
	if e = Verify(forged); e == nil {
		t.Fatal("accepted forged identity")
	}
	os.WriteFile(filepath.Join(r.GitDir, "gitdir"), []byte(filepath.Join(p, ".git")+"\n"), 0600)
	if e = Verify(r); e == nil {
		t.Fatal("accepted stale backlink")
	}
}

func TestSnapshotVerificationWithoutGit(t *testing.T) {
	for _, change := range []string{"unchanged", "index", "ignore", "new-source", "cache-write"} {
		t.Run(change, func(t *testing.T) {
			p := fixture(t)
			os.WriteFile(filepath.Join(p, ".gitignore"), []byte("/target/\n"), 0600)
			r, e := Discover(context.Background(), p)
			if e != nil {
				t.Fatal(e)
			}
			cache := filepath.Join(r.Root, "target")
			s, e := CaptureSnapshot(context.Background(), r, []string{cache})
			if e != nil {
				t.Fatal(e)
			}
			t.Setenv("PATH", t.TempDir())
			switch change {
			case "index":
				os.WriteFile(filepath.Join(r.GitDir, "index"), []byte("changed"), 0600)
			case "ignore":
				os.WriteFile(filepath.Join(p, ".gitignore"), []byte("/other/\n"), 0600)
			case "new-source":
				os.WriteFile(filepath.Join(p, "new.go"), []byte("source"), 0600)
			case "cache-write":
				os.Mkdir(cache, 0700)
				os.WriteFile(filepath.Join(cache, "artifact"), []byte("cache"), 0600)
			}
			e = s.Verify()
			if change == "unchanged" || change == "cache-write" {
				if e != nil {
					t.Fatal(e)
				}
			} else if e == nil {
				t.Fatal("accepted changed discovery evidence")
			}
		})
	}
}

func TestSnapshotIndependentIgnoredChurn(t *testing.T) {
	for _, change := range []string{"churn", "ignore", "index", "symlink", "tracked"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			p := fixture(t)
			w := filepath.Join(t.TempDir(), "independent")
			runGit(t, p, "worktree", "add", "-b", "independent", w)
			os.WriteFile(filepath.Join(w, ".gitignore"), []byte("target/\n"), 0600)
			cache := filepath.Join(w, "target")
			os.Mkdir(cache, 0700)
			os.WriteFile(filepath.Join(cache, "artifact"), []byte("old"), 0600)
			if change == "tracked" {
				runGit(t, w, "add", "-f", "target/artifact")
			}
			r, e := Discover(ctx, p)
			if e != nil {
				t.Fatal(e)
			}
			s, e := CaptureSnapshot(ctx, r, []string{filepath.Join(r.Root, "target")})
			if e != nil {
				t.Fatal(e)
			}
			t.Setenv("PATH", t.TempDir())
			switch change {
			case "churn", "tracked":
				os.Remove(filepath.Join(cache, "artifact"))
				os.WriteFile(filepath.Join(cache, "new-artifact"), []byte("new"), 0600)
			case "ignore":
				os.WriteFile(filepath.Join(w, ".gitignore"), []byte("other/\n"), 0600)
			case "index":
				os.WriteFile(filepath.Join(s.Worktrees[1].GitDir, "index"), []byte("changed"), 0600)
			case "symlink":
				os.RemoveAll(cache)
				if e := os.Symlink(t.TempDir(), cache); e != nil {
					t.Skip(e)
				}
			}
			e = s.Verify()
			if change == "churn" && e != nil {
				t.Fatal(e)
			}
			if change != "churn" && e == nil {
				t.Fatal("accepted changed safety evidence")
			}
		})
	}
}

func TestUnrelatedDanglingSourceLinks(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		t.Run(fmt.Sprint(tracked), func(t *testing.T) {
			p := fixture(t)
			other := filepath.Join(t.TempDir(), "other")
			runGit(t, p, "worktree", "add", "--detach", other)
			os.WriteFile(filepath.Join(p, ".gitignore"), []byte("target/\n"), 0600)
			link := filepath.Join(other, "dangling")
			if err := os.Symlink("missing", link); err != nil {
				t.Skip(err)
			}
			if tracked {
				runGit(t, other, "add", "dangling")
			}
			r, err := Discover(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(p, "target")
			if _, err := r.ValidateCache(context.Background(), cache); err != nil {
				t.Fatal(err)
			}
			os.Remove(link)
			os.Symlink(filepath.Join(cache, "missing"), link)
			if _, err := r.ValidateCache(context.Background(), cache); err == nil {
				t.Fatal("accepted source alias into cache")
			}
			os.Remove(link)
			os.Symlink("missing", cache)
			if _, err := r.ValidateCache(context.Background(), cache); err == nil {
				t.Fatal("accepted dangling cache alias")
			}
		})
	}
}
