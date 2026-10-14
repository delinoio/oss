// SPDX-License-Identifier: Apache-2.0
package subscription

import (
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

func TestGrokAuthFileWriteReadRetire(t *testing.T) {
	home := grokHomeForTest(t)
	bundle, err := ParseGrokAuthFile(grokFixtureFile(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteGrokAuthFile(home, bundle); err != nil {
		t.Fatal("write failed")
	}
	info, err := os.Lstat(filepath.Join(home, GrokAuthFileName))
	if err != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("auth.json is not a regular owner-only file")
	}
	read, err := ReadGrokAuthFile(home)
	if err != nil || !read.Equal(bundle) {
		t.Fatal("read did not round trip")
	}
	if present, err := GrokAuthFilePresent(home); err != nil || !present {
		t.Fatal("presence not reported")
	}
	// An interrupted earlier write is attributed to its target and retired.
	leftover := filepath.Join(home, security.OwnedTemporaryPrefix(filepath.Join(home, GrokAuthFileName))+"old")
	if err := os.WriteFile(leftover, []byte(grokTestRefresh), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RetireGrokAuthFile(home); err != nil {
		t.Fatal("retire failed")
	}
	if present, err := GrokAuthFilePresent(home); err != nil || present {
		t.Fatal("credential material survived retirement")
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatal("retirement left files behind")
	}
	// Retiring an absent file is idempotent.
	if err := RetireGrokAuthFile(home); err != nil {
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
		if _, err := ReadGrokAuthFile(home); err == nil || err.Error() != GrokBundleInvalid().Error() {
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
		if _, err := ReadGrokAuthFile(home); err == nil {
			t.Fatal("a symlinked auth.json was read")
		}
		// A non-regular entry cannot be proven removable and is never followed.
		if err := RetireGrokAuthFile(home); err == nil {
			t.Fatal("a symlinked auth.json entry was treated as retired")
		}
		if _, err := os.Stat(target); err != nil {
			t.Fatal("retirement followed the link")
		}
		if present, err := GrokAuthFilePresent(home); err == nil || !present {
			t.Fatal("a linked credential entry was reported clean")
		}
	}
}

func TestSealGrokFinishBindsOriginalIdentity(t *testing.T) {
	home := grokHomeForTest(t)
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	original, _ := ParseGrokAuthFile(grokFixtureFile(t, nil))
	if err := WriteGrokAuthFile(home, original); err != nil {
		t.Fatal(err)
	}
	sealed, _, err := SealGrokFinish(home, server, account, generation, original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenGrokBundle(sealed, server, account, generation); err != nil {
		t.Fatal("sealed finish bundle does not reopen")
	}
	other, _ := ParseGrokAuthFile(grokFixtureFile(t, func(e map[string]any) { e["principal_id"] = "synthetic-principal-0002" }))
	if _, _, err := SealGrokFinish(home, server, account, generation, other); err == nil {
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
