package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func seedSharedCodexHistory(t *testing.T, config Config, account domain.ID) (home, rollout string) {
	t.Helper()
	home, err := codexAccountHistoryHome(config.Root, account)
	if err != nil {
		t.Fatal(err)
	}
	rollout = filepath.Join(home, "sessions", "rollout.jsonl")
	if err := security.PrivateDir(filepath.Dir(rollout)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rollout, []byte("shared-history"), 0600); err != nil {
		t.Fatal(err)
	}
	return home, rollout
}

// The pre-registry shared account home is registered where it is; its history
// is neither moved nor merged, and repeated resolution after an owner restart
// returns the same profile.
func TestSharedCodexHomeIsRegisteredInPlaceAndStableAcrossRestart(t *testing.T) {
	config, machine := profileTestConfig(t)
	account := domain.NewID()
	home, rollout := seedSharedCodexHistory(t, config, account)
	ctx := context.Background()
	first, err := resolveSharedCodexHome(ctx, config, machine, account)
	if err != nil {
		t.Fatal(err)
	}
	if first.Root != home || first.Origin != nativeprofile.OriginLegacy || first.Key.Purpose != nativeprofile.PurposeSharedOwner || first.Key.Harness != domain.Codex {
		t.Fatalf("shared home was not registered in place: %+v", first)
	}
	restartProfileOwner(config, machine)
	second, err := resolveSharedCodexHome(ctx, config, machine, account)
	if err != nil || second.ID != first.ID || second.Root != home {
		t.Fatalf("restart allocated another shared home: %v", err)
	}
	if raw, err := os.ReadFile(rollout); err != nil || string(raw) != "shared-history" {
		t.Fatal("shared history changed")
	}
	if countProfileDirectories(t, config.Root) != 0 {
		t.Fatal("registering the shared home allocated a managed profile")
	}
	// A first resolution of a new account creates only the fixed directory.
	fresh := domain.NewID()
	created, err := resolveSharedCodexHome(ctx, config, machine, fresh)
	if err != nil || created.ID == first.ID || created.Root == home {
		t.Fatalf("accounts share a Codex history home: %v", err)
	}
	freshHome, _ := codexAccountHistoryHome(config.Root, fresh)
	if created.Root != freshHome {
		t.Fatal("new account home is not the fixed account path")
	}
}

// A live owner lock blocks another acquire with the closed busy cause while
// retained history, caches and helper links never do.
func TestSharedCodexHomeLockBlocksOnlyWhileHeld(t *testing.T) {
	config, machine := profileTestConfig(t)
	account := domain.NewID()
	home, rollout := seedSharedCodexHistory(t, config, account)
	ctx := context.Background()
	held, err := acquireSharedCodexHome(ctx, config, machine, account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireSharedCodexHome(ctx, config, machine, account); err == nil {
		t.Fatal("two owners locked the shared account home")
	} else if reason, ok := nativeprofile.Reasoned(err); !ok || reason != nativeprofile.ReasonBusy {
		t.Fatalf("blocked without the busy cause: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rollout, filepath.Join(home, "helper-link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	restartProfileOwner(config, machine)
	next, err := acquireSharedCodexHome(ctx, config, machine, account)
	if err != nil {
		t.Fatalf("retained helper link blocked the next owner: %v", err)
	}
	defer next.Close()
	if next.ID != held.ID || next.Root != home {
		t.Fatal("restart changed the shared profile")
	}
}

// A replaced home directory blocks with recovery required, distinct from a
// held lock and from retained data, and is never repaired or recreated.
func TestSharedCodexHomeReplacedDirectoryIsInvalidOwner(t *testing.T) {
	config, machine := profileTestConfig(t)
	account := domain.NewID()
	home, _ := seedSharedCodexHistory(t, config, account)
	ctx := context.Background()
	lease, err := acquireSharedCodexHome(ctx, config, machine, account)
	if err != nil {
		t.Fatal(err)
	}
	_ = lease.Close()
	moved := home + "-moved"
	if err := os.Rename(home, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, home); err != nil {
		t.Skip("symlinks unavailable")
	}
	_, err = acquireSharedCodexHome(ctx, config, machine, account)
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("replaced home did not block as recovery required: %v", err)
	}
	if reason, ok := nativeprofile.Reasoned(err); ok && reason == nativeprofile.ReasonBusy {
		t.Fatal("a replaced home was reported as a held lock")
	}
	if stat, err := os.Lstat(home); err != nil || stat.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the replaced path was repaired")
	}
}

// Checkpoint readers accept an unregistered legacy home and a registered home
// at the derived path, and reject a home that differs from the registration.
func TestSharedCodexHomeVerificationRejectsMismatchedRegistration(t *testing.T) {
	config, machine := profileTestConfig(t)
	account := domain.NewID()
	home, _ := seedSharedCodexHistory(t, config, account)
	if err := verifySharedCodexHome(config.Root, machine, account, home); err != nil {
		t.Fatalf("unregistered legacy home was rejected: %v", err)
	}
	if _, err := resolveSharedCodexHome(context.Background(), config, machine, account); err != nil {
		t.Fatal(err)
	}
	if err := verifySharedCodexHome(config.Root, machine, account, home); err != nil {
		t.Fatalf("registered home was rejected: %v", err)
	}
	if err := verifySharedCodexHome(config.Root, machine, account, filepath.Join(config.Root, "elsewhere", "codex")); err == nil {
		t.Fatal("a different home was accepted for a registered account")
	}
}

// Deleting a session removes its conversation profiles but never the
// account-wide shared Codex history another session continues from.
func TestSessionDeletionKeepsSharedCodexHome(t *testing.T) {
	config, machine := profileTestConfig(t)
	account, session := domain.NewID(), domain.NewID()
	home, rollout := seedSharedCodexHistory(t, config, account)
	ctx := context.Background()
	if _, err := resolveSharedCodexHome(ctx, config, machine, account); err != nil {
		t.Fatal(err)
	}
	lease, err := executionProfile(ctx, config, domain.Codex, profileExecution(machine, account, session))
	if err != nil {
		t.Fatal(err)
	}
	_ = lease.Close()
	registry, err := nativeProfiles(config, machine)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Delete(ctx, nativeprofile.DeleteScope{Conversation: session}); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(rollout); err != nil || string(raw) != "shared-history" {
		t.Fatal("session deletion removed the shared account history")
	}
	if info, ok, err := registry.Lookup(ctx, sharedCodexOwnerKey(account)); err != nil || !ok || info.Root != home {
		t.Fatal("session deletion unregistered the shared account home")
	}
}
