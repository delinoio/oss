//go:build darwin

// SPDX-License-Identifier: Apache-2.0
package credentials

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/ebitengine/purego"
)

// Opt-in interactive acceptance runs alone in a separate test process. Both
// processes select only a newly created temporary keychain; the default/search
// list and user credentials are never read or changed. The password and payload
// are public synthetic fixture values, never account credentials.
func TestMacInteractiveTemporaryKeychain(t *testing.T) {
	if os.Getenv("DELIDEV_MAC_INTERACTIVE_FIXTURE") != "1" {
		t.Skip("requires explicit isolated interactive acceptance")
	}
	a, err := loadMac()
	if err != nil {
		t.Fatal(err)
	}
	var create func(string, uint32, *byte, uint8, uintptr, *uintptr) int32
	var remove func(uintptr) int32
	var lock func(uintptr) int32
	var unlock func(uintptr, uint32, *byte, uint8) int32
	purego.RegisterLibFunc(&create, a.security, "SecKeychainCreate")
	purego.RegisterLibFunc(&remove, a.security, "SecKeychainDelete")
	purego.RegisterLibFunc(&lock, a.security, "SecKeychainLock")
	purego.RegisterLibFunc(&unlock, a.security, "SecKeychainUnlock")
	password := []byte("delidev-isolated-test-password")
	defer clear(password)
	path := filepath.Join(t.TempDir(), "interactive-fixture.keychain")
	var keychain uintptr
	if status := create(path, uint32(len(password)), &password[0], 0, 0, &keychain); status != 0 {
		t.Fatalf("fixture creation status: %d", status)
	}
	defer func() {
		// The original parent retains cleanup authority even if the child times out
		// while an OS prompt is open. Unlock only this owned fixture programmatically.
		unlock(keychain, uint32(len(password)), &password[0], 1)
		if status := remove(keychain); status != 0 {
			t.Errorf("fixture cleanup status: %d", status)
		}
		a.release(keychain)
	}()
	name := string(domain.NewID()) + "/" + string(domain.NewID()) + "/" + string(domain.NewID()) + "/account-api"
	backend := &macStore{api: a, keychain: keychain}
	material := bytes.Repeat([]byte{0x2d}, nativeMaterialSize)
	defer clear(material)
	if err := backend.create(context.Background(), name, material); err != nil {
		t.Fatal(err)
	}
	if status := lock(keychain); status != 0 {
		t.Fatalf("fixture lock status: %d", status)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestMacInteractiveReadHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "DELIDEV_MAC_INTERACTIVE_CHILD=1", "DELIDEV_INTERACTIVE_KEYCHAIN="+path, "DELIDEV_INTERACTIVE_REFERENCE="+name)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("isolated interactive child did not complete: %v", err)
	}
}

func TestMacInteractiveReadHelper(t *testing.T) {
	if os.Getenv("DELIDEV_MAC_INTERACTIVE_CHILD") != "1" {
		t.Skip("original parent-owned temporary keychain only")
	}
	a, err := loadMac()
	if err != nil {
		t.Fatal(err)
	}
	var open func(string, *uintptr) int32
	purego.RegisterLibFunc(&open, a.security, "SecKeychainOpen")
	var keychain uintptr
	if status := open(os.Getenv("DELIDEV_INTERACTIVE_KEYCHAIN"), &keychain); status != 0 {
		t.Fatalf("fixture open status: %d", status)
	}
	defer a.release(keychain)
	backend := &macStore{api: a, keychain: keychain}
	t.Log("interactive_fixture phase=cancel-original-read")
	value, err := backend.get(context.Background(), os.Getenv("DELIDEV_INTERACTIVE_REFERENCE"))
	clear(value)
	wantCode(t, err, domain.ConfirmationRequired)
	t.Log("interactive_fixture phase=approve-explicit-retry")
	value, err = backend.get(context.Background(), os.Getenv("DELIDEV_INTERACTIVE_REFERENCE"))
	defer clear(value)
	if err != nil || !bytes.Equal(value, bytes.Repeat([]byte{0x2d}, nativeMaterialSize)) {
		t.Fatalf("original reference retry: %v", err)
	}
	t.Log("interactive_fixture phase=original-read-completed")
}
