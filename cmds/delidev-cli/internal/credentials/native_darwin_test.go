//go:build darwin

package credentials

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/ebitengine/purego"
)

// Native integration uses a new password-protected temporary keychain. Every
// query explicitly names it; no login keychain/default credentials are read.
func TestMacTemporaryKeychain(t *testing.T) {
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
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	password := []byte(hex.EncodeToString(raw))
	defer clear(raw)
	defer clear(password)
	var keychain uintptr
	keychainDir := filepath.Join(t.TempDir(), "keychain")
	if err = security.PrivateDir(keychainDir); err != nil {
		t.Fatal(err)
	}
	if status := create(filepath.Join(keychainDir, "test.keychain"), uint32(len(password)), &password[0], 0, 0, &keychain); status != 0 {
		t.Fatalf("temporary keychain creation: OSStatus %d", status)
	}
	defer func() {
		if status := remove(keychain); status != 0 {
			t.Errorf("temporary keychain cleanup: OSStatus %d", status)
		}
		a.release(keychain)
	}()
	backend := &macStore{api: a, keychain: keychain}
	ctx := context.Background()
	name := string(domain.NewID()) + "/" + string(domain.NewID()) + "/" + string(domain.NewID()) + "/account-api"
	material := make([]byte, nativeMaterialSize)
	if _, err = rand.Read(material); err != nil {
		t.Fatal(err)
	}
	defer clear(material)
	_, err = backend.get(ctx, name)
	wantCode(t, err, domain.NotFound)
	if err = backend.create(ctx, name, material); err != nil {
		t.Fatal(err)
	}
	wantCode(t, backend.create(ctx, name, material), domain.Conflict)
	value, err := backend.get(ctx, name)
	if err != nil || !bytes.Equal(value, material) {
		t.Fatalf("native round trip: %v", err)
	}
	clear(value)
	if status := lock(keychain); status != 0 {
		t.Fatalf("lock: OSStatus %d", status)
	}
	_, err = backend.get(ctx, name)
	wantCode(t, err, domain.ConfirmationRequired)
	if status := unlock(keychain, uint32(len(password)), &password[0], 1); status != 0 {
		t.Fatalf("unlock: OSStatus %d", status)
	}
	if err = backend.remove(ctx, name); err != nil {
		t.Fatal(err)
	}
	if err = backend.remove(ctx, name); err != nil {
		t.Fatal(err)
	}
	_, err = backend.get(ctx, name)
	wantCode(t, err, domain.NotFound)
	// The envelope vault must also work with native wrapping keys for values larger
	// than Windows' generic-credential limit, with the same wire format on all OSes.
	vault, err := open(filepath.Join(t.TempDir(), "vault"), domain.NewID(), backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	ref := reference()
	secret := bytes.Repeat([]byte("private-oauth-envelope-"), 1000)
	if _, err = vault.Put(ctx, ref, secret); err != nil {
		t.Fatal(err)
	}
	result, err := vault.Get(ctx, ref)
	if err != nil || !bytes.Equal(result, secret) {
		t.Fatalf("native vault: %v", err)
	}
	clear(result)
	if err = vault.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
}
