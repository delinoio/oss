// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// quotaHelperAliasNames are the exact Codex temporary aliases that the original
// startup proof can retire. They are repeated here to keep the server fixture
// independent of the Codex adapter's unexported proof. The pinned startup also
// creates codex-linux-sandbox on Linux only.
var quotaHelperAliasNames = quotaHelperAliasNamesFor(runtime.GOOS)

func quotaHelperAliasNamesFor(goos string) []string {
	names := []string{"apply_patch", "applypatch", "codex-execve-wrapper"}
	if goos == "linux" {
		names = append(names, "codex-linux-sandbox")
	}
	return names
}

// writeQuotaHelperRuntime creates a retained quota runtime shaped like one whose
// native tree exited and left its Codex helper aliases behind.
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

// historicalQuotaFixture prepares the reported historical access-only quota
// shape: a prior-epoch uncertain operation, an empty original process index, no
// auth or probe files and the native helper aliases left in its retained runtime.
func historicalQuotaFixture(t *testing.T, aliases bool) (*subscriptionFixture, *serverQuotaFixture, domain.ID, string, string) {
	t.Helper()
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
	return f, n, op, home, writeQuotaHelperRuntime(t, home, aliases)
}

func restartServerQuotas(t *testing.T, f *subscriptionFixture) {
	t.Helper()
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.initializeServerQuotas(ctx); err != nil {
		t.Fatal(err)
	}
	f.service.runServerQuota(ctx, f.input.AccountID)
}

// A historical retained quota runtime has the original aliases and an empty
// original process index. Restart reconciliation settles the uncertain attempt
// exactly once as failed without unlinking or following the helper aliases,
// without replaying the old request, and without changing identity.
func TestServerQuotaRestartSettlesHistoricalHelperAliasesOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix helper aliases are not created on Windows")
	}
	f, n, op, home, directory := historicalQuotaFixture(t, true)
	_, before := f.record()
	var logs bytes.Buffer
	f.service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	restartServerQuotas(t, f)
	_, a := f.record()
	o := a.Subscription.ServerQuota
	if n.reads.Load() != 0 {
		t.Fatal("the historical request was replayed")
	}
	if !o.CleanupConfirmed || o.Phase != domain.SubscriptionObservationFailed || o.ErrorCode != domain.Canceled {
		t.Fatalf("historical shape was not settled once as failed: %+v", o)
	}
	b := before.Subscription.ServerQuota
	if o.ID != op || o.ConnectionID != b.ConnectionID || o.Generation != b.Generation || a.Subscription.Generation != before.Subscription.Generation || !o.RequestedAt.Equal(b.RequestedAt) {
		t.Fatal("settlement changed the operation, connection or generation identity")
	}
	if !reflect.DeepEqual(a.Subscription.QuotaObservedAt, before.Subscription.QuotaObservedAt) {
		t.Fatal("settlement invented or changed a quota observation")
	}
	for _, alias := range quotaHelperAliasNames {
		if info, statErr := os.Lstat(filepath.Join(directory, alias)); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatal("migration unlinked or replaced alias", alias, statErr)
		}
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatal("migration removed the retained directory")
	}
	receipt, ok, err := f.service.nativeProfilesMust(t).ReadReceipt(context.Background(), nativeprofile.ReceiptLegacyQuota, op)
	if err != nil || !ok || receipt.Step != nativeprofile.ReceiptSettled || receipt.Account != f.input.AccountID || receipt.Connection != o.ConnectionID || receipt.Generation != o.Generation {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	// A second restart changes nothing and logs only closed fields.
	f.service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	restartServerQuotas(t, f)
	_, again := f.record()
	if !reflect.DeepEqual(again.Subscription.ServerQuota, o) || n.reads.Load() != 0 {
		t.Fatal("a second restart changed the settled operation")
	}
	for _, forbidden := range []string{home, f.service.Store.Root(), "verified-native"} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatal("migration logs exposed a personal path", forbidden)
		}
	}
}

// Missing or contradicting proof keeps the original fence and unlinks nothing.
func TestServerQuotaHistoricalShapeKeepsFenceWithoutProof(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix helper aliases are not created on Windows")
	}
	cases := []struct {
		name   string
		mutate func(t *testing.T, f *subscriptionFixture, op domain.ID, home string)
	}{
		{"missing-process-index", func(t *testing.T, f *subscriptionFixture, op domain.ID, _ string) {
			if err := os.Remove(filepath.Join(f.service.Store.Root(), "subscription-runtime", "processes", string(op))); err != nil {
				t.Fatal(err)
			}
		}},
		{"auth-file-remnant", func(t *testing.T, f *subscriptionFixture, _ domain.ID, home string) {
			if err := security.PrivateDir(filepath.Join(home, "codex")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, "codex", "auth.json"), []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"token-remnant", func(t *testing.T, f *subscriptionFixture, _ domain.ID, home string) {
			if err := os.WriteFile(filepath.Join(home, "codex", "log.txt"), []byte(`{"refresh_token":"synthetic"}`), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"changed-operation-identity", func(t *testing.T, f *subscriptionFixture, op domain.ID, _ string) {
			err := f.service.nativeProfilesMust(t).WriteReceipt(context.Background(), nativeprofile.Receipt{Kind: nativeprofile.ReceiptLegacyQuota, Operation: op, Account: f.input.AccountID, Connection: domain.NewID(), Generation: domain.NewID(), Step: nativeprofile.ReceiptVerified, RetainedPath: "subscription-runtime/quota/" + string(op)})
			if err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, n, op, home, directory := historicalQuotaFixture(t, true)
			c.mutate(t, f, op, home)
			restartServerQuotas(t, f)
			_, a := f.record()
			if n.reads.Load() != 0 || a.Subscription.ServerQuota.CleanupConfirmed || a.Subscription.ServerQuota.Phase != domain.SubscriptionObservationUncertain {
				t.Fatal("missing or contradicting proof released the cleanup obligation")
			}
			for _, alias := range quotaHelperAliasNames {
				if info, statErr := os.Lstat(filepath.Join(directory, alias)); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatal("reconciliation inferred removal authority for", alias, statErr)
				}
			}
		})
	}
}
