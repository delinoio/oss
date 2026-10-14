package worker

import (
	"context"
	"os"
	"runtime"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// blockRegistryPublication makes every later registry publication of the
// Worker registry fail while reads keep working, and restores it on cleanup.
func blockRegistryPublication(t *testing.T, config Config, machine domain.ID) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not block publication here")
	}
	registry, err := nativeProfiles(config, machine)
	if err != nil {
		t.Fatal(err)
	}
	// Create the registry lock file first: opening an existing one needs no
	// write access to the directory.
	if _, _, err := registry.Lookup(context.Background(), sharedCodexOwnerKey(domain.NewID())); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(registry.Base(), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(registry.Base(), 0o700) })
}

func restoreRegistryPublication(t *testing.T, config Config, machine domain.ID) {
	t.Helper()
	registry, err := nativeProfiles(config, machine)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(registry.Base(), 0o700); err != nil {
		t.Fatal(err)
	}
}

// Registration timeline of a fresh account home: a publication failure after
// the registry accepted the key leaves neither the empty home nor an entry, so
// a retry creates it again instead of adopting a leftover.
func TestResolveSharedCodexHomeRollsBackACreatedHomeWhenPublicationFails(t *testing.T) {
	ctx := context.Background()
	config, machine := profileTestConfig(t)
	account := domain.NewID()
	home, err := codexAccountHistoryHome(config.Root, account)
	if err != nil {
		t.Fatal(err)
	}
	blockRegistryPublication(t, config, machine)
	if _, err := resolveSharedCodexHome(ctx, config, machine, account); err == nil {
		t.Fatal("a failed registry publication was reported as success")
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatalf("the failed attempt left an unregistered home behind: %v", err)
	}
	registry, err := nativeProfiles(config, machine)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := registry.Lookup(ctx, sharedCodexOwnerKey(account)); err != nil || ok {
		t.Fatalf("the failed attempt registered the account: ok=%v err=%v", ok, err)
	}
	restoreRegistryPublication(t, config, machine)
	info, err := resolveSharedCodexHome(ctx, config, machine, account)
	if err != nil {
		t.Fatal(err)
	}
	if info.Root != home || security.CheckPrivateDir(home) != nil {
		t.Fatal("the retry did not create and register the home")
	}
}

// An existing unregistered home belongs to the owner's history: a failed
// publication leaves it and its files exactly as they were.
func TestResolveSharedCodexHomeKeepsAnExistingHomeWhenPublicationFails(t *testing.T) {
	ctx := context.Background()
	config, machine := profileTestConfig(t)
	account := domain.NewID()
	home, rollout := seedSharedCodexHistory(t, config, account)
	blockRegistryPublication(t, config, machine)
	if _, err := resolveSharedCodexHome(ctx, config, machine, account); err == nil {
		t.Fatal("a failed registry publication was reported as success")
	}
	if raw, err := os.ReadFile(rollout); err != nil || string(raw) != "shared-history" {
		t.Fatal("an existing home lost history", err)
	}
	restoreRegistryPublication(t, config, machine)
	info, err := resolveSharedCodexHome(ctx, config, machine, account)
	if err != nil || info.Root != home {
		t.Fatalf("the retry did not register the existing home: %v", err)
	}
}
