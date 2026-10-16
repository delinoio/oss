// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func grokHomeForTest(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "grok")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	return home
}

// boundGrokHome binds home and releases the handle with the test.
func boundGrokHome(t *testing.T, home string) GrokHome {
	t.Helper()
	bound, err := BindGrokHome(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bound.Close() })
	return bound
}

func TestGrokAuthFileWriteReadRetire(t *testing.T) {
	home := grokHomeForTest(t)
	bundle, err := ParseGrokAuthFile(grokFixtureFile(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := boundGrokHome(t, home).WriteAuthFile(bundle); err != nil {
		t.Fatal("write failed")
	}
	info, err := os.Lstat(filepath.Join(home, GrokAuthFileName))
	if err != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("auth.json is not a regular owner-only file")
	}
	read, err := boundGrokHome(t, home).ReadAuthFile()
	if err != nil || !read.Equal(bundle) {
		t.Fatal("read did not round trip")
	}
	if present, err := boundGrokHome(t, home).AuthFilePresent(); err != nil || !present {
		t.Fatal("presence not reported")
	}
	// An interrupted earlier write is attributed to its target and retired.
	leftover := filepath.Join(home, security.OwnedTemporaryPrefix(filepath.Join(home, GrokAuthFileName))+"old")
	if err := os.WriteFile(leftover, []byte(grokTestRefresh), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := boundGrokHome(t, home).RetireAuthFile(); err != nil {
		t.Fatal("retire failed")
	}
	if present, err := boundGrokHome(t, home).AuthFilePresent(); err != nil || present {
		t.Fatal("credential material survived retirement")
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatal("retirement left files behind")
	}
	// Retiring an absent file is idempotent.
	if err := boundGrokHome(t, home).RetireAuthFile(); err != nil {
		t.Fatal("repeated retirement failed")
	}
}

func TestReadGrokAuthFileRejectsUnsafeEntries(t *testing.T) {
	home := grokHomeForTest(t)
	path := filepath.Join(home, GrokAuthFileName)
	for name, prepare := range map[string]func() error{
		"missing":   func() error { return nil },
		"empty":     func() error { return os.WriteFile(path, nil, 0o600) },
		"oversize":  func() error { return os.WriteFile(path, []byte(strings.Repeat("a", MaxGrokAuthFile+1)), 0o600) },
		"invalid":   func() error { return os.WriteFile(path, []byte(`{"x":1}`), 0o600) },
		"directory": func() error { return os.Mkdir(path, 0o700) },
	} {
		_ = os.RemoveAll(path)
		if err := prepare(); err != nil {
			t.Fatal(err)
		}
		if _, err := boundGrokHome(t, home).ReadAuthFile(); err == nil || err.Error() != GrokBundleInvalid().Error() {
			t.Fatalf("%s entry was not rejected with the safe error", name)
		}
	}
	if runtime.GOOS != "windows" {
		_ = os.RemoveAll(path)
		target := filepath.Join(t.TempDir(), "target.json")
		if err := os.WriteFile(target, grokFixtureFile(t, nil), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if _, err := boundGrokHome(t, home).ReadAuthFile(); err == nil {
			t.Fatal("a symlinked auth.json was read")
		}
		// A non-regular entry cannot be proven removable and is never followed.
		if err := boundGrokHome(t, home).RetireAuthFile(); err == nil {
			t.Fatal("a symlinked auth.json entry was treated as retired")
		}
		if _, err := os.Stat(target); err != nil {
			t.Fatal("retirement followed the link")
		}
		if present, err := boundGrokHome(t, home).AuthFilePresent(); err == nil || !present {
			t.Fatal("a linked credential entry was reported clean")
		}
	}
}

func TestSealGrokFinishBindsOriginalIdentity(t *testing.T) {
	home := grokHomeForTest(t)
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	original, _ := ParseGrokAuthFile(grokFixtureFile(t, nil))
	if err := boundGrokHome(t, home).WriteAuthFile(original); err != nil {
		t.Fatal(err)
	}
	sealed, _, err := boundGrokHome(t, home).SealFinish(server, account, generation, original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenGrokBundle(sealed, server, account, generation); err != nil {
		t.Fatal("sealed finish bundle does not reopen")
	}
	other, _ := ParseGrokAuthFile(grokFixtureFile(t, func(e map[string]any) { e["principal_id"] = "synthetic-principal-0002" }))
	if _, _, err := boundGrokHome(t, home).SealFinish(server, account, generation, other); err == nil {
		t.Fatal("a different identity was sealed")
	}
}

func TestGrokRefreshedRequiresSameIdentityAndRotation(t *testing.T) {
	before, _ := ParseGrokAuthFile(grokFixtureFile(t, nil))
	rotate := func(mutate func(map[string]any)) GrokBundle {
		b, err := ParseGrokAuthFile(grokFixtureFile(t, mutate))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	good := rotate(func(e map[string]any) { e["refresh_token"], e["expires_at"] = "next-refresh", "2026-10-14T12:00:00Z" })
	if GrokRefreshed(before, good) != nil {
		t.Fatal("complete rotation rejected")
	}
	for name, after := range map[string]GrokBundle{
		"identical":   before,
		"same-expiry": rotate(func(e map[string]any) { e["refresh_token"] = "next-refresh" }),
		"same-tokens": rotate(func(e map[string]any) { e["expires_at"] = "2026-10-14T12:00:00Z" }),
		"other-identity": rotate(func(e map[string]any) {
			e["principal_id"] = "other"
			e["refresh_token"] = "x"
			e["expires_at"] = "2026-10-14T12:00:00Z"
		}),
	} {
		if GrokRefreshed(before, after) == nil {
			t.Fatalf("%s refresh was accepted", name)
		}
	}
	if GrokRefreshed(GrokBundle{}, good) == nil {
		t.Fatal("an empty original was accepted")
	}
}

// A second directory entry keeps the plaintext credential reachable after the
// file's own name is retired, so such a file is never sealed and its cleanup is
// never confirmed.
func TestGrokAuthFileRejectsHardLinkedCredential(t *testing.T) {
	home := grokHomeForTest(t)
	path := filepath.Join(home, GrokAuthFileName)
	bundle, err := ParseGrokAuthFile(grokFixtureFile(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := boundGrokHome(t, home).WriteAuthFile(bundle); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias.json")
	if err := os.Link(path, alias); err != nil {
		t.Skip("hard links are unsupported on this platform")
	}
	if _, err := boundGrokHome(t, home).ReadAuthFile(); err == nil || err.Error() != GrokBundleInvalid().Error() {
		t.Fatal("a hard-linked auth.json was read")
	}
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	if _, _, err := boundGrokHome(t, home).SealFinish(server, account, generation, bundle); err == nil {
		t.Fatal("a hard-linked auth.json was sealed")
	}
	if present, err := boundGrokHome(t, home).AuthFilePresent(); err == nil || !present {
		t.Fatal("a hard-linked credential was reported clean")
	}
	// The name in the private home is still unlinked, but the surviving entry
	// makes the retirement unconfirmed.
	if err := boundGrokHome(t, home).RetireAuthFile(); err == nil {
		t.Fatal("retirement of a hard-linked credential was confirmed")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("the hard-linked name was not unlinked")
	}
	if _, err := os.Stat(alias); err != nil {
		t.Fatal("retirement removed the other directory entry")
	}
	if present, err := boundGrokHome(t, home).AuthFilePresent(); err != nil || present {
		t.Fatal("the home still reports a credential after the name was unlinked")
	}
}

// An interrupted write's temporary can be hard linked as well.
func TestRetireGrokAuthFileReportsHardLinkedTemporary(t *testing.T) {
	home := grokHomeForTest(t)
	temporary := filepath.Join(home, security.OwnedTemporaryPrefix(filepath.Join(home, GrokAuthFileName))+"old")
	if err := os.WriteFile(temporary, []byte(grokTestRefresh), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias.tmp")
	if err := os.Link(temporary, alias); err != nil {
		t.Skip("hard links are unsupported on this platform")
	}
	if err := boundGrokHome(t, home).RetireAuthFile(); err == nil {
		t.Fatal("retirement of a hard-linked temporary was confirmed")
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatal("the temporary name was not unlinked")
	}
}

func TestGrokHomeVerifiesTheBoundDirectoryIdentity(t *testing.T) {
	home := grokHomeForTest(t)
	bound, err := BindGrokHome(home)
	if err != nil || bound.Path() != home || bound.Verify() != nil {
		t.Fatal("a freshly bound home did not verify")
	}
	if (GrokHome{}).Verify() == nil {
		t.Fatal("an unbound home verified")
	}
	if _, err := BindGrokHome(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing home was bound")
	}
	if err := os.Rename(home, home+".moved"); err != nil {
		t.Fatal(err)
	}
	if bound.Verify() == nil {
		t.Fatal("a home renamed away still verified")
	}
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	if bound.Verify() == nil {
		t.Fatal("a replacement directory verified as the original home")
	}
	// The original is only the same object again when it is back at its path.
	if err := os.Remove(home); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(home+".moved", home); err != nil {
		t.Fatal(err)
	}
	if bound.Verify() != nil {
		t.Fatal("the original directory did not verify after it returned")
	}
}

// swapGrokHome renames the bound home away and plants a fresh private directory
// at its path, returning the path the original directory moved to.
func swapGrokHome(t *testing.T, home string) string {
	t.Helper()
	moved := home + ".moved"
	if err := os.Rename(home, moved); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	return moved
}

// The directory is validated and used through one handle: a home that is
// renamed away and replaced after binding must never receive the credential,
// whether the swap is caught by the path check or lands after it.
func TestGrokWriteIsAnchoredToTheBoundHomeNotItsPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory replacement semantics differ")
	}
	bundle, err := ParseGrokAuthFile(grokFixtureFile(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("path-check-rejects-the-substitute", func(t *testing.T) {
		home := grokHomeForTest(t)
		bound := boundGrokHome(t, home)
		moved := swapGrokHome(t, home)
		if err := bound.WriteAuthFile(bundle); err == nil {
			t.Fatal("a credential was written for a home that no longer names the bound directory")
		}
		for _, dir := range []string{home, moved} {
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Fatalf("a rejected write left files in %s", filepath.Base(dir))
			}
		}
	})
	t.Run("swap-after-the-path-check-lands-in-the-bound-directory", func(t *testing.T) {
		home := grokHomeForTest(t)
		bound := boundGrokHome(t, home)
		moved := swapGrokHome(t, home)
		raw, err := bundle.AuthFile()
		if err != nil {
			t.Fatal(err)
		}
		// The unguarded step the path check could not cover.
		if err := bound.writeAnchored(raw); err != nil {
			t.Fatal(err)
		}
		if entries, _ := os.ReadDir(home); len(entries) != 0 {
			t.Fatal("the credential was written into the substitute directory")
		}
		if _, err := os.Lstat(filepath.Join(moved, GrokAuthFileName)); err != nil {
			t.Fatal("the credential was not written into the bound directory")
		}
		// Reading and retiring also stay on the bound directory, and a file
		// planted in the substitute is neither read nor removed.
		planted := filepath.Join(home, GrokAuthFileName)
		if err := os.WriteFile(planted, grokFixtureFile(t, func(e map[string]any) { e["principal_id"] = "synthetic-principal-0002" }), 0o600); err != nil {
			t.Fatal(err)
		}
		read, err := bound.ReadAuthFile()
		if err != nil || !read.Equal(bundle) {
			t.Fatal("a read followed the path into the substitute directory")
		}
		if present, err := bound.AuthFilePresent(); err != nil || !present {
			t.Fatal("presence was judged on the substitute directory")
		}
		if err := bound.RetireAuthFile(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(moved, GrokAuthFileName)); !os.IsNotExist(err) {
			t.Fatal("retirement did not remove the bound directory's credential")
		}
		if _, err := os.Lstat(planted); err != nil {
			t.Fatal("retirement removed a file from the substitute directory")
		}
	})
}

// A planted symlink at auth.json is replaced, never written through.
func TestGrokWriteDoesNotFollowAPlantedAuthFileLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges")
	}
	home := grokHomeForTest(t)
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, GrokAuthFileName)); err != nil {
		t.Fatal(err)
	}
	bundle, _ := ParseGrokAuthFile(grokFixtureFile(t, nil))
	if err := boundGrokHome(t, home).WriteAuthFile(bundle); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(target); string(raw) != "untouched" {
		t.Fatal("the write followed the planted link")
	}
	if info, err := os.Lstat(filepath.Join(home, GrokAuthFileName)); err != nil || !info.Mode().IsRegular() {
		t.Fatal("auth.json was not published as a regular file")
	}
}

// A home is bound only from the opened directory: a link or a shared directory
// is refused, and every operation on a closed or never-bound home fails closed.
func TestGrokHomeBindingFailsClosed(t *testing.T) {
	home := grokHomeForTest(t)
	bound := boundGrokHome(t, home)
	bundle, _ := ParseGrokAuthFile(grokFixtureFile(t, nil))
	if err := bound.Close(); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"verify":  bound.Verify(),
		"write":   bound.WriteAuthFile(bundle),
		"retire":  bound.RetireAuthFile(),
		"zero":    (GrokHome{}).RetireAuthFile(),
		"zero-w":  (GrokHome{}).WriteAuthFile(bundle),
		"zero-v":  (GrokHome{}).Verify(),
		"missing": func() error { _, err := BindGrokHome(filepath.Join(t.TempDir(), "missing")); return err }(),
	} {
		if err == nil {
			t.Fatalf("%s succeeded on a home that is not bound", name)
		}
	}
	if _, err := (GrokHome{}).ReadAuthFile(); err == nil {
		t.Fatal("a never-bound home was read")
	}
	if present, err := (GrokHome{}).AuthFilePresent(); err == nil || !present {
		t.Fatal("a never-bound home was reported clean")
	}
	if runtime.GOOS == "windows" {
		return
	}
	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := BindGrokHome(shared); err == nil {
		t.Fatal("a group-readable home was bound")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	if _, err := BindGrokHome(link); err == nil {
		t.Fatal("a symlinked home was bound")
	}
}

// Loosening the held directory's permissions after binding is detected through
// the handle, so credentials are not written into a directory that became shared.
func TestGrokHomeRejectsALooseningAfterBinding(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not apply")
	}
	home := grokHomeForTest(t)
	bound := boundGrokHome(t, home)
	if err := os.Chmod(home, 0o755); err != nil {
		t.Fatal(err)
	}
	bundle, _ := ParseGrokAuthFile(grokFixtureFile(t, nil))
	if bound.WriteAuthFile(bundle) == nil {
		t.Fatal("a credential was written into a directory that became group-readable")
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatal("a rejected write left files behind")
	}
}

// installGrokBeforeUnlink sets the seam for one test and restores it.
func installGrokBeforeUnlink(t *testing.T, hook func(name string)) {
	t.Helper()
	previous := grokBeforeUnlink
	grokBeforeUnlink = hook
	t.Cleanup(func() { grokBeforeUnlink = previous })
}

func requireCleanupIdentity(t *testing.T, err error) {
	t.Helper()
	var cleanup *RuntimeCleanupError
	if !errors.As(err, &cleanup) || cleanup.Reason != CleanupIdentity {
		t.Fatalf("retirement was not reported as an unconfirmed identity: %v", err)
	}
}

// A link that another process creates after the entry was inspected but before
// its name is unlinked is caught by the link count of the retained handle, in
// this directory or in any other.
func TestRetireGrokAuthFileDetectsALinkCreatedBeforeTheUnlink(t *testing.T) {
	for name, aliasDir := range map[string]func(home string) string{
		"same directory":  func(home string) string { return home },
		"other directory": func(string) string { return t.TempDir() },
	} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" && name == "other directory" {
				t.Skip("Windows reports no exact link count after an unlink; only same-directory links are rescanned")
			}
			home := grokHomeForTest(t)
			bundle, _ := ParseGrokAuthFile(grokFixtureFile(t, nil))
			bound := boundGrokHome(t, home)
			if err := bound.WriteAuthFile(bundle); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(aliasDir(home), "alias.json")
			linkedAfterInspection := false
			installGrokBeforeUnlink(t, func(entry string) {
				if entry != GrokAuthFileName {
					return
				}
				if err := os.Link(filepath.Join(home, GrokAuthFileName), alias); err != nil {
					t.Skip("hard links are unsupported on this platform")
				}
				linkedAfterInspection = true
			})
			err := bound.RetireAuthFile()
			if !linkedAfterInspection {
				t.Fatal("the link was not created in the unlink window")
			}
			requireCleanupIdentity(t, err)
			if _, err := os.Lstat(filepath.Join(home, GrokAuthFileName)); !os.IsNotExist(err) {
				t.Fatal("the credential name was not unlinked")
			}
			if _, err := os.Stat(alias); err != nil {
				t.Fatal("retirement removed the other directory entry")
			}
		})
	}
}

func TestRetireGrokAuthFileDetectsALinkCreatedBeforeTheTemporaryUnlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows reports no exact link count after an unlink")
	}
	home := grokHomeForTest(t)
	temporary := filepath.Join(home, security.OwnedTemporaryPrefix(filepath.Join(home, GrokAuthFileName))+"old")
	if err := os.WriteFile(temporary, []byte(grokTestRefresh), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias.tmp")
	installGrokBeforeUnlink(t, func(entry string) {
		if strings.HasPrefix(entry, grokTemporaryPrefix) {
			if err := os.Link(temporary, alias); err != nil {
				t.Skip("hard links are unsupported on this platform")
			}
		}
	})
	requireCleanupIdentity(t, boundGrokHome(t, home).RetireAuthFile())
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatal("the temporary name was not unlinked")
	}
}

// A retirement that finds no extra entry is confirmed, and a retained handle
// is never confused with a different file of the home.
func TestRetireGrokAuthFileConfirmsASingleLinkedCredentialAmongOtherFiles(t *testing.T) {
	home := grokHomeForTest(t)
	bundle, _ := ParseGrokAuthFile(grokFixtureFile(t, nil))
	bound := boundGrokHome(t, home)
	if err := bound.WriteAuthFile(bundle); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(home, "config.toml")
	if err := os.WriteFile(other, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := bound.RetireAuthFile(); err != nil {
		t.Fatalf("a single-link credential was not confirmed: %v", err)
	}
	if raw, err := os.ReadFile(other); err != nil || string(raw) != "keep" {
		t.Fatal("retirement touched an unrelated file")
	}
}

// A materialization never layers a new credential on an older plaintext that a
// second directory entry keeps reachable.
func TestGrokWriteRefusesAnOlderLinkedCredential(t *testing.T) {
	for name, prepare := range map[string]func(home string) string{
		"auth.json": func(home string) string { return filepath.Join(home, GrokAuthFileName) },
		"temporary": func(home string) string {
			return filepath.Join(home, security.OwnedTemporaryPrefix(filepath.Join(home, GrokAuthFileName))+"old")
		},
	} {
		t.Run(name, func(t *testing.T) {
			home := grokHomeForTest(t)
			older := prepare(home)
			if err := os.WriteFile(older, []byte(grokTestRefresh), 0o600); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(t.TempDir(), "alias")
			if err := os.Link(older, alias); err != nil {
				t.Skip("hard links are unsupported on this platform")
			}
			bundle, _ := ParseGrokAuthFile(grokFixtureFile(t, nil))
			if err := boundGrokHome(t, home).WriteAuthFile(bundle); err == nil {
				t.Fatal("a credential was written over a linked older credential")
			}
			if present, _ := boundGrokHome(t, home).AuthFilePresent(); present {
				t.Fatal("a failed write left credential material in the home")
			}
		})
	}
}

func TestRetireGrokAuthFileRemovesTheNativeLockFile(t *testing.T) {
	home := grokHomeForTest(t)
	bundle, err := ParseGrokAuthFile(grokFixtureFile(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	bound := boundGrokHome(t, home)
	if err := bound.WriteAuthFile(bundle); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(home, GrokLockFileName)
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if present, err := bound.AuthFilePresent(); err != nil || !present {
		t.Fatal("a lock file must count as unretired credential state")
	}
	if err := bound.RetireAuthFile(); err != nil {
		t.Fatal("retire failed")
	}
	for _, name := range []string{GrokAuthFileName, GrokLockFileName} {
		if _, err := os.Lstat(filepath.Join(home, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s survived retirement", name)
		}
	}
	if present, err := bound.AuthFilePresent(); err != nil || present {
		t.Fatal("presence reported after retirement")
	}
	// A lock file alone (no credential) is still retired and reported.
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if present, err := bound.AuthFilePresent(); err != nil || !present {
		t.Fatal("a lone lock file must keep the profile unclean")
	}
	if err := bound.RetireAuthFile(); err != nil {
		t.Fatal("retire of a lone lock failed")
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatal("retirement left files behind")
	}
}

func TestGrokLockFileThatIsNotRegularIsNeverReportedClean(t *testing.T) {
	home := grokHomeForTest(t)
	if err := os.Mkdir(filepath.Join(home, GrokLockFileName), 0o700); err != nil {
		t.Fatal(err)
	}
	bound := boundGrokHome(t, home)
	if present, err := bound.AuthFilePresent(); err == nil || !present {
		t.Fatal("a non-regular lock entry must be an unconfirmed cleanup")
	}
	if err := bound.RetireAuthFile(); err == nil {
		t.Fatal("a non-regular lock entry must not be retired as clean")
	}
}
