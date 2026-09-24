//go:build linux

package credentials

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/godbus/dbus/v5"
)

func TestPrivateBusAddressValidation(t *testing.T) {
	for input, want := range map[string]string{
		"unix:path=/run/user/1000/bus":          "/run/user/1000/bus",
		"unix:path=/private/a%2Cb%20c,guid=123": "/private/a,b c",
		"unix:abstract=/tmp/private":            "\x00/tmp/private",
	} {
		got, err := parseBusAddress(input)
		if err != nil || got != want {
			t.Errorf("local address: %q %v", got, err)
		}
	}
	for _, input := range []string{"", "autolaunch:", "tcp:host=localhost,port=1234", "unix:path=relative", "unix:path=/one;unix:path=/two", "unix:path=/one,path=/two", "unix:path=/one,abstract=two", "unix:path=/one%00two", "unix:path=/one%ZZ", "unix:tmpdir=/tmp", "unix:path=/one,arbitrary=x"} {
		if _, err := parseBusAddress(input); err == nil {
			t.Errorf("accepted unsafe address %q", input)
		}
	}
}
func TestSecretServiceSafeErrors(t *testing.T) {
	ctx := context.Background()
	wantCode(t, secretServiceError(ctx, dbus.Error{Name: "org.freedesktop.Secret.Error.IsLocked", Body: []any{"secret"}}), domain.ConfirmationRequired)
	wantCode(t, secretServiceError(ctx, dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownObject", Body: []any{"secret"}}), domain.NotFound)
	wantCode(t, secretServiceError(ctx, errors.New("secret")), domain.Unavailable)
}
func TestUnavailableLocalBusNeverFallsBackToDisk(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+t.TempDir()+"/missing-bus")
	backend := linuxStore{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := backend.get(ctx, "test-reference")
	wantCode(t, err, domain.Unavailable)
	wantCode(t, backend.create(ctx, "test-reference", make([]byte, nativeMaterialSize)), domain.Unavailable)
	wantCode(t, backend.remove(ctx, "test-reference"), domain.Unavailable)
}

// Run only inside an explicitly provisioned disposable Secret Service session.
// The container fixture owns its HOME, bus and default collection; ordinary host
// tests never opt in, unlock a collection, or inspect the user's saved accounts.
func TestLinuxIsolatedSecretService(t *testing.T) {
	if os.Getenv("DELIDEV_TEST_ISOLATED_SECRET_SERVICE") != "1" {
		t.Skip("requires a disposable Secret Service session")
	}
	ctx := context.Background()
	backend := linuxStore{}
	name := string(domain.NewID()) + "/" + string(domain.NewID()) + "/" + string(domain.NewID()) + "/account-api"
	defer func() {
		if err := backend.remove(ctx, name); err != nil {
			t.Errorf("temporary Secret Service cleanup: %v", err)
		}
	}()
	material := make([]byte, nativeMaterialSize)
	if _, err := rand.Read(material); err != nil {
		t.Fatal(err)
	}
	defer clear(material)
	_, err := backend.get(ctx, name)
	wantCode(t, err, domain.NotFound)
	if err = backend.create(ctx, name, material); err != nil {
		t.Fatal(err)
	}
	wantCode(t, backend.create(ctx, name, make([]byte, nativeMaterialSize)), domain.Conflict)
	got, err := backend.get(ctx, name)
	if err != nil || !bytes.Equal(got, material) {
		t.Fatalf("native Secret Service round trip: %v", err)
	}
	clear(got)
	if err = backend.remove(ctx, name); err != nil {
		t.Fatal(err)
	}
	if err = backend.remove(ctx, name); err != nil {
		t.Fatal(err)
	}
	vault, err := open(filepath.Join(t.TempDir(), "vault"), domain.NewID(), backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	ref := reference()
	defer vault.Delete(ctx, ref)
	secret := bytes.Repeat([]byte("temporary-large-oauth-payload-"), 1000)
	if _, err = vault.Put(ctx, ref, secret); err != nil {
		t.Fatal(err)
	}
	got, err = vault.Get(ctx, ref)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("native Secret Service envelope: %v", err)
	}
	clear(got)
	if err = vault.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	// Finish by locking a new test item. This deliberately leaves only test
	// wrapping material in the disposable container; its entire keyring is removed
	// with the container. Never run this against a user's shared default collection.
	lockedRef := reference()
	if _, err = vault.Put(ctx, lockedRef, []byte("temporary-locked-secret")); err != nil {
		t.Fatal(err)
	}
	bus, err := connectBus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	path, err := findItem(ctx, bus, vault.name(lockedRef))
	if err != nil {
		t.Fatal(err)
	}
	var lockedPaths []dbus.ObjectPath
	var prompt dbus.ObjectPath
	err = bus.Object(serviceName, servicePath).CallWithContext(ctx, serviceInterface+".Lock", 0, []dbus.ObjectPath{path}).Store(&lockedPaths, &prompt)
	if err != nil || prompt != "/" || len(lockedPaths) == 0 {
		t.Fatalf("isolated keyring lock: %v", err)
	}
	_, err = backend.get(ctx, vault.name(lockedRef))
	wantCode(t, err, domain.ConfirmationRequired)
	wantCode(t, vault.Delete(ctx, lockedRef), domain.ConfirmationRequired)
	_, err = vault.Get(ctx, lockedRef)
	wantCode(t, err, domain.NotFound)

}
