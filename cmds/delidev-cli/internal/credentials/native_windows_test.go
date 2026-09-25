//go:build windows

package credentials

import (
	"bytes"
	"context"
	"crypto/rand"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Touch only a fresh UUID namespace. Never enumerate Credential Manager or read
// an existing login. The native entry is deleted even if a later assertion fails.
func TestWindowsTemporaryCredential(t *testing.T) {
	ctx := context.Background()
	native := windowsStore{}
	name := string(domain.NewID()) + "/" + string(domain.NewID()) + "/" + string(domain.NewID()) + "/account-api"
	defer func() {
		if err := native.remove(ctx, name); err != nil {
			t.Errorf("temporary credential cleanup: %v", err)
		}
	}()
	_, err := native.get(ctx, name)
	wantCode(t, err, domain.NotFound)
	value := make([]byte, nativeMaterialSize)
	if _, err = rand.Read(value); err != nil {
		t.Fatal(err)
	}
	defer clear(value)
	if err = native.create(ctx, name, value); err != nil {
		t.Fatal(err)
	}
	wantCode(t, native.create(ctx, name, bytes.Repeat([]byte{7}, nativeMaterialSize)), domain.Conflict)
	got, err := native.get(ctx, name)
	if err != nil || !bytes.Equal(got, value) {
		t.Fatalf("native round trip: %v", err)
	}
	clear(got)
	if err = native.remove(ctx, name); err != nil {
		t.Fatal(err)
	}
	if err = native.remove(ctx, name); err != nil {
		t.Fatal(err)
	}
	vault, err := open(filepath.Join(t.TempDir(), "vault"), domain.NewID(), native, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	ref := reference()
	defer vault.Delete(ctx, ref)
	secret := bytes.Repeat([]byte("long-temporary-oauth-document-"), 1000)
	if _, err = vault.Put(ctx, ref, secret); err != nil {
		t.Fatal(err)
	}
	got, err = vault.Get(ctx, ref)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("native envelope: %v", err)
	}
	clear(got)
	if err = vault.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
}
