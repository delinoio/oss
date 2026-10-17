// SPDX-License-Identifier: Apache-2.0
package security

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// A lock binds the object it holds open; anything else at the name is pending.
func TestLockBindPathIdentityBindsOnlyTheLockedObject(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "owner.recovery.lock")
	lock, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	bound, err := lock.BindPathIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	defer bound.Close()
	if !bound.Present() || !bound.Held() || bound.Record().Kind != ObjectRegular || bound.Record().Size != 0 {
		t.Fatal("bound identity does not describe the empty regular lock", bound.Record())
	}
	if err := bound.StillAt(path); err != nil {
		t.Fatal("the locked object no longer matches its own binding", err)
	}
	// Take moves the handle; the source can no longer close it.
	taken := bound.Take()
	bound.Close()
	if !taken.Held() || bound.Present() {
		t.Fatal("Take did not transfer the identity")
	}
	taken.Close()

	for name, replace := range map[string]func(string){
		"empty file":   func(p string) { _ = os.WriteFile(p, nil, 0600) },
		"nonempty":     func(p string) { _ = os.WriteFile(p, []byte("x"), 0600) },
		"directory":    func(p string) { _ = os.Mkdir(p, 0700) },
		"missing name": func(string) {},
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.Rename(path, path+".moved"); err != nil {
				// Windows locks deny delete sharing, so an open lock also prevents
				// replacing its name. A different candidate must still be refused.
				if runtime.GOOS != "windows" || !errors.Is(err, syscall.Errno(32)) {
					t.Fatal(err)
				}
				candidate := filepath.Join(root, "candidate")
				t.Cleanup(func() { _ = os.RemoveAll(candidate) })
				replace(candidate)
				if replaced, err := lock.BindPathIdentity(candidate); err == nil {
					replaced.Close()
					t.Fatal("a different candidate was bound to the held lock")
				}
				return
			}
			t.Cleanup(func() { _ = os.RemoveAll(path); _ = os.Rename(path+".moved", path) })
			replace(path)
			if replaced, err := lock.BindPathIdentity(path); err == nil {
				replaced.Close()
				t.Fatal("a different object at the name was bound")
			}
		})
	}
}
