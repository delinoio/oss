// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// quotaHelperAliasNames are the exact Codex temporary aliases that the original
// startup proof can retire. They are repeated here to keep the server fixture
// independent of the Codex adapter's unexported proof.
var quotaHelperAliasNames = []string{"apply_patch", "applypatch", "codex-execve-wrapper"}

// writeQuotaHelperRuntime creates a retained quota runtime shaped like one whose
// native tree exited and left its three Codex helper aliases behind.
func writeQuotaHelperRuntime(t *testing.T, home string, aliases bool) string {
	t.Helper()
	directory := filepath.Join(home, "codex", "tmp", "arg0", "native-helpers")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".lock"), []byte("synthetic lock"), 0600); err != nil {
		t.Fatal(err)
	}
	if aliases {
		for _, name := range quotaHelperAliasNames {
			if err := os.Symlink(filepath.Join(home, "verified-native"), filepath.Join(directory, name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return directory
}

// A retired runtime (original aliases removed by the joined client) is exactly
// what strict runtime cleanup accepts; an unretired one keeps the generic
// symlink refusal, so the quota owner stays uncertain.
func TestServerQuotaRuntimeCleanupAcceptsOnlyRetiredHelpers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix helper aliases are not created on Windows")
	}
	for _, retired := range []bool{false, true} {
		name := map[bool]string{false: "aliases-retained", true: "aliases-retired"}[retired]
		t.Run(name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "quota")
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
			original, err := security.StableStat(home)
			if err != nil {
				t.Fatal(err)
			}
			directory := writeQuotaHelperRuntime(t, home, !retired)
			err = subscription.CleanupRuntime(home, original)
			if retired {
				if err != nil {
					t.Fatal("retired runtime was refused", err)
				}
				if _, statErr := os.Lstat(home); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatal("retired runtime remained")
				}
				return
			}
			var failure *subscription.RuntimeCleanupError
			if !errors.As(err, &failure) || failure.Reason != subscription.CleanupSymlink {
				t.Fatal("surviving aliases lost the generic symlink refusal", err)
			}
			for _, alias := range quotaHelperAliasNames {
				if _, statErr := os.Lstat(filepath.Join(directory, alias)); statErr != nil {
					t.Fatal("refused cleanup removed an alias", alias, statErr)
				}
			}
		})
	}
}

// A historical retained quota runtime has the three aliases and an empty
// original process index but no in-memory startup proof. Restart reconciliation
// must keep the obligation fenced and unlink nothing.
func TestServerQuotaRestartKeepsHistoricalHelperAliasesFenced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix helper aliases are not created on Windows")
	}
	f, n := newServerQuotaFixture(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f))); err != nil {
		t.Fatal(err)
	}
	var op domain.ID
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.quota.historical-helpers", nil, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		o := a.Subscription.ServerQuota
		o.Epoch = domain.NewID()
		o.Phase = domain.SubscriptionObservationUncertain
		op = o.ID
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(f.service.Store.Root(), "subscription-runtime")
	home := filepath.Join(runtimeRoot, "quota", string(op))
	for _, path := range []string{runtimeRoot, filepath.Join(runtimeRoot, "processes"), filepath.Join(runtimeRoot, "processes", string(op)), filepath.Join(runtimeRoot, "quota"), home} {
		if err := security.PrivateDir(path); err != nil {
			t.Fatal(err)
		}
	}
	directory := writeQuotaHelperRuntime(t, home, true)
	if err := f.service.initializeServerQuotas(ctx); err != nil {
		t.Fatal(err)
	}
	f.service.runServerQuota(ctx, f.input.AccountID)
	_, a := f.record()
	if n.reads.Load() != 0 || a.Subscription.ServerQuota.CleanupConfirmed || a.Subscription.ServerQuota.Phase != domain.SubscriptionObservationUncertain {
		t.Fatal("historical helper aliases released the cleanup obligation")
	}
	for _, alias := range quotaHelperAliasNames {
		if info, statErr := os.Lstat(filepath.Join(directory, alias)); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatal("restart reconciliation inferred removal authority for", alias, statErr)
		}
	}
}
