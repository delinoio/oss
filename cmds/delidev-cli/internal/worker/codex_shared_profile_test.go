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

// Registry state is validated before the derived account home is created. Every
// row below leaves the file system and the registry exactly as it found them,
// except the rows that register a new home.
func TestSharedCodexHomeValidatesRegistryBeforeCreatingDirectory(t *testing.T) {
	ctx := context.Background()
	type row struct {
		name string
		// arrange prepares the registry and returns whether the derived home must
		// exist afterwards and the closed reason expected from resolution.
		arrange func(t *testing.T, config Config, machine, account domain.ID, home string)
		reason  nativeprofile.Reason
		code    domain.Code
		created bool
	}
	otherPath := func(account domain.ID) string { return "runtimes/accounts/" + string(account) + "/codex-restored" }
	rows := []row{
		{name: "key bound to another valid legacy path while the derived home is absent", reason: nativeprofile.ReasonKeyConflict, code: domain.Conflict,
			arrange: func(t *testing.T, config Config, machine, account domain.ID, home string) {
				registry, err := nativeProfiles(config, machine)
				if err != nil {
					t.Fatal(err)
				}
				if err := security.PrivateDir(filepath.Join(config.Root, filepath.FromSlash(otherPath(account)))); err != nil {
					t.Fatal(err)
				}
				if _, err := registry.RegisterLegacy(ctx, sharedCodexOwnerKey(account), otherPath(account)); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "derived path bound to another profile while the derived home is absent", reason: nativeprofile.ReasonKeyConflict, code: domain.Conflict,
			arrange: func(t *testing.T, config Config, machine, account domain.ID, home string) {
				registry, err := nativeProfiles(config, machine)
				if err != nil {
					t.Fatal(err)
				}
				if err := security.PrivateDir(home); err != nil {
					t.Fatal(err)
				}
				other := nativeprofile.Key{Harness: domain.Codex, Purpose: nativeprofile.PurposeConversation, Account: account, Conversation: domain.NewID()}
				if _, err := registry.RegisterLegacy(ctx, other, sharedCodexLegacyPath(account)); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(home); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "registered home removed", code: domain.RecoveryRequired,
			arrange: func(t *testing.T, config Config, machine, account domain.ID, home string) {
				if _, err := resolveSharedCodexHome(ctx, config, machine, account); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(home); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "unregistered home replaced by a link", code: domain.RecoveryRequired,
			arrange: func(t *testing.T, config Config, machine, account domain.ID, home string) {
				target := filepath.Join(config.Root, "elsewhere")
				if err := security.PrivateDir(target); err != nil {
					t.Fatal(err)
				}
				if err := security.PrivateDir(filepath.Dir(home)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, home); err != nil {
					t.Skip("symlinks unavailable")
				}
			}},
	}
	for _, r := range rows {
		r := r
		t.Run(r.name, func(t *testing.T) {
			config, machine := profileTestConfig(t)
			account := domain.NewID()
			home, err := codexAccountHistoryHome(config.Root, account)
			if err != nil {
				t.Fatal(err)
			}
			r.arrange(t, config, machine, account, home)
			registry, err := nativeProfiles(config, machine)
			if err != nil {
				t.Fatal(err)
			}
			before, hadBefore, _ := registry.Lookup(ctx, sharedCodexOwnerKey(account))
			_, beforeErr := os.Lstat(home)
			_, err = resolveSharedCodexHome(ctx, config, machine, account)
			if err == nil {
				t.Fatal("resolution accepted inconsistent state")
			}
			if r.reason != "" {
				if reason, ok := nativeprofile.Reasoned(err); !ok || reason != r.reason {
					t.Fatalf("reason = %v, want %s: %v", reason, r.reason, err)
				}
			}
			if r.code != "" && domain.SafeError(err).Code != r.code {
				t.Fatalf("code = %s, want %s", domain.SafeError(err).Code, r.code)
			}
			if _, afterErr := os.Lstat(home); (beforeErr == nil) != (afterErr == nil) {
				t.Fatal("a failed resolution created or removed the derived account home")
			}
			after, hasAfter, _ := registry.Lookup(ctx, sharedCodexOwnerKey(account))
			if hadBefore != hasAfter || before.ID != after.ID || before.Root != after.Root {
				t.Fatal("a failed resolution changed the registry")
			}
		})
	}
}

// Checkpoint verification reads the registry and never creates the home.
func TestVerifySharedCodexHomeCreatesNothing(t *testing.T) {
	config, machine := profileTestConfig(t)
	account := domain.NewID()
	home, err := codexAccountHistoryHome(config.Root, account)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySharedCodexHome(config.Root, machine, account, home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatal("verification created the account home")
	}
	registry, err := nativeProfiles(config, machine)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := registry.Lookup(context.Background(), sharedCodexOwnerKey(account)); ok {
		t.Fatal("verification registered the account home")
	}
}

// An exclusive continuation (and the founding owner) must free the shared
// account home before the Finish that lets the server admit the next holder.
// The publish callback models the continuation's retried Take being admitted at
// that moment: it must find the lock free and the hold already released.
func TestPublishAfterReleaseFreesSharedHomeBeforeFinish(t *testing.T) {
	config, machine := profileTestConfig(t)
	account := domain.NewID()
	seedSharedCodexHistory(t, config, account)
	ctx := context.Background()
	hold, err := holdSharedCodexHome(ctx, config, machine, account)
	if err != nil {
		t.Fatal(err)
	}
	// While the hold exists another holder is blocked with the busy cause.
	if _, err := holdSharedCodexHome(ctx, config, machine, account); err == nil {
		t.Fatal("two holders locked the shared account home")
	}
	published := false
	releaseErr, publishErr := publishAfterRelease(hold, func() error {
		published = true
		if hold.lease != nil {
			t.Error("the hold still owned the lock when Finish was published")
		}
		next, err := holdSharedCodexHome(ctx, config, machine, account)
		if err != nil {
			t.Errorf("the admitted continuation met a held lock: %v", err)
			return nil
		}
		return next.release()
	})
	if !published || releaseErr != nil || publishErr != nil {
		t.Fatalf("published=%v release=%v publish=%v", published, releaseErr, publishErr)
	}
	// Releasing again, and releasing a missing hold, are no-ops.
	if err := hold.release(); err != nil {
		t.Fatal(err)
	}
	var none *sharedHomeHold
	if err := none.release(); err != nil {
		t.Fatal(err)
	}
	releaseErr, publishErr = publishAfterRelease(none, func() error { return nil })
	if releaseErr != nil || publishErr != nil {
		t.Fatalf("a missing hold blocked the publication: %v %v", releaseErr, publishErr)
	}
}

// A failed release verification never skips the Finish, because the account
// lease must not stay held for a lock that was already closed. The failure is
// reported next to the publish outcome and is verified again by the next call.
func TestPublishAfterReleaseReportsUnverifiedReleaseAndStillPublishes(t *testing.T) {
	config, machine := profileTestConfig(t)
	account := domain.NewID()
	home, _ := seedSharedCodexHistory(t, config, account)
	ctx := context.Background()
	hold, err := holdSharedCodexHome(ctx, config, machine, account)
	if err != nil {
		t.Fatal(err)
	}
	moved := home + "-moved"
	if err := os.Rename(home, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, home); err != nil {
		t.Skip("symlinks unavailable")
	}
	published := false
	releaseErr, publishErr := publishAfterRelease(hold, func() error { published = true; return nil })
	if !published || publishErr != nil {
		t.Fatalf("Finish was skipped: published=%v err=%v", published, publishErr)
	}
	if releaseErr == nil || !hold.unverified {
		t.Fatalf("an unverifiable release was reported as verified: %v", releaseErr)
	}
	if err := os.Remove(home); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, home); err != nil {
		t.Fatal(err)
	}
	if err := hold.release(); err != nil || hold.unverified {
		t.Fatalf("a repaired home was not verified on retry: %v", err)
	}
}
