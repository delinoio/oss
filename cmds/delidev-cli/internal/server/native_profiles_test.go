package server

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
)

// testNativeScope binds a fresh account to the persistent profile registry of
// root, the way production openers receive their scope.
func testNativeScope(t *testing.T, root string) nativeScope {
	t.Helper()
	registry, err := serverNativeProfiles(root, domain.NewID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return nativeScope{Root: root, Account: domain.NewID(), Profiles: registry}
}

// profileRootFor returns the retained profile root of a scope's account.
func profileRootFor(t *testing.T, scope nativeScope, harness domain.Harness, purpose nativeprofile.Purpose) string {
	t.Helper()
	info, ok, err := scope.Profiles.Lookup(context.Background(), nativeprofile.Key{Harness: harness, Purpose: purpose, Account: scope.Account})
	if err != nil || !ok {
		t.Fatalf("profile is not registered: %v", err)
	}
	return info.Root
}

// nativeProfilesMust returns the server's registry or fails the test.
func (s *Service) nativeProfilesMust(t *testing.T) *nativeprofile.Registry {
	t.Helper()
	registry, err := s.nativeProfiles()
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestAccountProfilesAreSeparateAndSurviveEveryOperation(t *testing.T) {
	scope := testNativeScope(t, t.TempDir())
	other := scope
	other.Account = domain.NewID()
	ctx := context.Background()
	roots := map[string]bool{}
	for _, s := range []nativeScope{scope, other} {
		for _, purpose := range []nativeprofile.Purpose{nativeprofile.PurposeAuth, nativeprofile.PurposeQuota, nativeprofile.PurposeCredit} {
			lease, err := acquireAccountProfile(ctx, s, domain.Codex, purpose)
			if err != nil {
				t.Fatal(err)
			}
			if roots[lease.Root] {
				t.Fatal("profile shared across accounts or purposes")
			}
			roots[lease.Root] = true
			_ = lease.Close()
		}
	}
	// A joined quota read leaves native helper links and caches behind; neither
	// blocks the next eligible read, which resolves the same permanent profile.
	first, err := acquireAccountProfile(ctx, scope, domain.Codex, nativeprofile.PurposeQuota)
	if err != nil {
		t.Fatal(err)
	}
	helpers := filepath.Join(first.Root, "codex", "tmp", "arg0", "native-helpers")
	if err := os.MkdirAll(helpers, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(first.Root, "verified-native"), filepath.Join(helpers, "apply_patch")); err != nil {
		t.Skip("symlinks unavailable")
	}
	_ = first.Close()
	next, err := acquireAccountProfile(ctx, scope, domain.Codex, nativeprofile.PurposeQuota)
	if err != nil {
		t.Fatalf("retained helper link blocked the next operation: %v", err)
	}
	defer next.Close()
	if next.Root != first.Root || next.ID != first.ID {
		t.Fatal("a later operation allocated a new profile")
	}
	if _, err := os.Lstat(filepath.Join(helpers, "apply_patch")); err != nil {
		t.Fatal("helper link was removed")
	}
}

func TestAuthProfileBlocksOnUnremovedCredentialUntilReconciled(t *testing.T) {
	scope := testNativeScope(t, t.TempDir())
	ctx := context.Background()
	lease, err := acquireAccountProfile(ctx, scope, domain.Codex, nativeprofile.PurposeAuth)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(lease.Root, "codex"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lease.Root, "codex", "auth.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = lease.Close()
	owner := domain.NewID()
	var logs bytes.Buffer
	_, err = openServerSubscription(ctx, scope, owner, nil, slog.New(slog.NewJSONHandler(&logs, nil)))
	if domain.SafeError(err).Code != domain.RecoveryRequired || !strings.Contains(logs.String(), `"reason":"credential_file_present"`) || strings.Contains(logs.String(), scope.Root) {
		t.Fatalf("an unremoved credential file did not block with its specific safe cause: %v %s", err, logs.String())
	}
	// The failed open released its lock; reconciliation retires only the file.
	if err := retireServerLoginCredentials(ctx, scope, filepath.Join(scope.Root, "subscription-runtime", "auth", string(owner)), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(lease.Root, "codex", "auth.json")); !os.IsNotExist(err) {
		t.Fatal("credential file was not retired")
	}
	if _, err := os.Stat(lease.Root); err != nil {
		t.Fatal("reconciliation removed the persistent profile")
	}
}

func TestAccessOnlyProfileRefusesManagedAuthentication(t *testing.T) {
	scope := testNativeScope(t, t.TempDir())
	ctx := context.Background()
	lease, err := acquireAccountProfile(ctx, scope, domain.Codex, nativeprofile.PurposeQuota)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(lease.Root, "codex"), 0700)
	_ = os.WriteFile(filepath.Join(lease.Root, "codex", "auth.json"), []byte("{}"), 0600)
	_ = lease.Close()
	var logs bytes.Buffer
	_, err = openServerQuota(ctx, scope, domain.NewID(), codex.QuotaAuthentication{}, slog.New(slog.NewJSONHandler(&logs, nil)))
	if domain.SafeError(err).Code != domain.RecoveryRequired || !strings.Contains(logs.String(), `"reason":"credential_file_present"`) {
		t.Fatalf("an access-only profile accepted managed authentication: %v", err)
	}
	if _, err := openServerSubscription(ctx, nativeScope{Root: scope.Root, Account: scope.Account}, domain.NewID(), nil, nil); err == nil {
		t.Fatal("a scope without a registry fell back to a temporary home")
	}
}
