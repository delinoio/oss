// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func grokWorkerFile(t *testing.T, principal, refresh, expires string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{subscription.GrokScopeKey: map[string]any{
		"key": "synthetic-key-" + refresh, "auth_mode": "oidc", "create_time": "2026-10-14T00:00:00Z", "expires_at": expires,
		"user_id": "synthetic-user", "principal_type": "user", "principal_id": principal, "refresh_token": "synthetic-refresh-" + refresh,
		"oidc_issuer": subscription.GrokIssuer, "oidc_client_id": subscription.GrokClientID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func grokWorkerLease(t *testing.T, server, account, generation domain.ID, bundle []byte) *managedSubscriptionLease {
	t.Helper()
	return &managedSubscriptionLease{credential: Credential{ServerID: server}, account: account, finishID: domain.NewID(), response: &pb.TakeSubscriptionResponse{LeaseId: string(domain.NewID()), GenerationId: string(generation), Bundle: bundle}}
}

func grokHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "grok")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestGrokMaterializationWritesPrivateAuthFileFromBoundEnvelope(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	native, err := subscription.ParseGrokAuthFile(grokWorkerFile(t, "synthetic-principal-1", "one", "2026-10-14T06:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := subscription.SealGrokBundle(server, account, generation, native)
	if err != nil {
		t.Fatal(err)
	}
	home := grokHome(t)
	lease := grokWorkerLease(t, server, account, generation, sealed)
	original, err := lease.materializeGrok(home)
	if err != nil || original.IdentityKey() != native.IdentityKey() {
		t.Fatal("bound envelope was not materialized")
	}
	if len(lease.response.Bundle) != 0 {
		t.Fatal("the delivered envelope stayed in memory")
	}
	path := filepath.Join(home, subscription.GrokAuthFileName)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("auth.json was not written as a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("auth.json is not owner-only")
	}
	written, err := subscription.ReadGrokAuthFile(home)
	if err != nil || !written.Equal(native) {
		t.Fatal("auth.json does not carry the delivered credential")
	}
	if entries, _ := os.ReadDir(home); len(entries) != 1 {
		t.Fatal("materialization left a temporary beside auth.json")
	}
}

func TestGrokMaterializationRejectsReboundEnvelopeBeforeWriting(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	native, _ := subscription.ParseGrokAuthFile(grokWorkerFile(t, "synthetic-principal-1", "one", "2026-10-14T06:00:00Z"))
	for name, lease := range map[string]*managedSubscriptionLease{
		"other-server":     grokWorkerLease(t, domain.NewID(), account, generation, mustSeal(t, server, account, generation, native)),
		"other-account":    grokWorkerLease(t, server, domain.NewID(), generation, mustSeal(t, server, account, generation, native)),
		"other-generation": grokWorkerLease(t, server, account, domain.NewID(), mustSeal(t, server, account, generation, native)),
		"garbage":          grokWorkerLease(t, server, account, generation, []byte(`{"version":1}`)),
		"missing-bundle":   grokWorkerLease(t, server, account, generation, nil),
	} {
		t.Run(name, func(t *testing.T) {
			home := grokHome(t)
			if _, err := lease.materializeGrok(home); err == nil {
				t.Fatal("rebound or missing envelope was accepted")
			} else if strings.Contains(err.Error(), "synthetic") {
				t.Fatal("rejection echoed a bundle value")
			}
			if entries, _ := os.ReadDir(home); len(entries) != 0 {
				t.Fatal("a rejected envelope left files behind")
			}
		})
	}
	// A fresh login legitimately has no bundle and no generation.
	fresh := grokWorkerLease(t, server, account, "", nil)
	if bundle, err := fresh.materializeGrok(grokHome(t)); err != nil || bundle.IdentityKey() != "" {
		t.Fatal("fresh login materialization failed")
	}
}

func mustSeal(t *testing.T, server, account, generation domain.ID, bundle subscription.GrokBundle) []byte {
	t.Helper()
	sealed, err := subscription.SealGrokBundle(server, account, generation, bundle)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func TestGrokMaterializationRefusesNonPrivateOrLinkedHome(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	native, _ := subscription.ParseGrokAuthFile(grokWorkerFile(t, "synthetic-principal-1", "one", "2026-10-14T06:00:00Z"))
	sealed := mustSeal(t, server, account, generation, native)
	if runtime.GOOS != "windows" {
		open := filepath.Join(t.TempDir(), "open")
		if err := os.Mkdir(open, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := grokWorkerLease(t, server, account, generation, bytes.Clone(sealed)).materializeGrok(open); err == nil {
			t.Fatal("a group-readable home received a credential")
		}
		real := grokHome(t)
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		if _, err := grokWorkerLease(t, server, account, generation, bytes.Clone(sealed)).materializeGrok(link); err == nil {
			t.Fatal("a symlinked home received a credential")
		}
		if entries, _ := os.ReadDir(real); len(entries) != 0 {
			t.Fatal("a credential was written through a symlink")
		}
	}
}

func TestPrepareGrokFinishSealsRotatedCredentialAndRetiresFile(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	original, _ := subscription.ParseGrokAuthFile(grokWorkerFile(t, "synthetic-principal-1", "one", "2026-10-14T06:00:00Z"))
	home := grokHome(t)
	path := filepath.Join(home, subscription.GrokAuthFileName)
	write := func(raw []byte) {
		t.Helper()
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z"))
	out := prepareGrokFinish(home, server, account, generation, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH, original, true)
	if !out.Success || !out.Cleanup || len(out.Bundle) == 0 {
		t.Fatal("a rotated credential was not sealed")
	}
	opened, err := subscription.OpenGrokBundle(out.Bundle, server, account, generation)
	if err != nil || subscription.GrokRefreshed(original, opened) != nil {
		t.Fatal("the sealed Finish bundle is not the rotated credential bound to the next generation")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("auth.json survived Finish preparation")
	}

	// A different identity in the native file is never sealed.
	write(grokWorkerFile(t, "synthetic-principal-2", "two", "2026-10-14T12:00:00Z"))
	out = prepareGrokFinish(home, server, account, generation, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH, original, true)
	if out.Success || len(out.Bundle) != 0 || !out.Cleanup {
		t.Fatal("a foreign identity was sealed or the file was kept")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("auth.json survived a rejected identity")
	}

	// Logout and a failed operation return no bundle but still retire the file.
	for name, action := range map[string]pb.SubscriptionAction{"logout": pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT, "failed-login": pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN} {
		write(grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z"))
		out = prepareGrokFinish(home, server, account, generation, action, subscription.GrokBundle{}, name == "logout")
		if len(out.Bundle) != 0 || !out.Cleanup {
			t.Fatalf("%s returned a bundle or kept the file", name)
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("%s left auth.json", name)
		}
	}

	// An unparsable native file downgrades to an unsuccessful Finish.
	write([]byte(`{"x":1}`))
	out = prepareGrokFinish(home, server, account, generation, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, subscription.GrokBundle{}, true)
	if out.Success || len(out.Bundle) != 0 || !out.Cleanup {
		t.Fatal("an invalid native file was reported as success")
	}
}

func TestPrepareGrokFinishWithholdsBundleWhenCleanupIsUnconfirmed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory replacement semantics differ")
	}
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	home := grokHome(t)
	path := filepath.Join(home, subscription.GrokAuthFileName)
	raw := grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// An interrupted earlier write left a plaintext temporary that is a directory
	// and therefore cannot be proven removable.
	stuck := filepath.Join(home, security.OwnedTemporaryPrefix(path)+"stuck")
	if err := os.Mkdir(stuck, 0o700); err != nil {
		t.Fatal(err)
	}
	out := prepareGrokFinish(home, server, account, generation, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, subscription.GrokBundle{}, true)
	if out.Cleanup || len(out.Bundle) != 0 {
		t.Fatal("cleanup was confirmed or a bundle was returned despite an unretired remnant")
	}
}

// A native process can leave another directory entry for auth.json. Unlinking
// the name in the private home would not remove the credential, so Finish
// neither seals it nor confirms cleanup.
func TestPrepareGrokFinishWithholdsBundleForHardLinkedCredential(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	home := grokHome(t)
	path := filepath.Join(home, subscription.GrokAuthFileName)
	original, err := subscription.ParseGrokAuthFile(grokWorkerFile(t, "synthetic-principal-1", "one", "2026-10-14T06:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, filepath.Join(t.TempDir(), "alias.json")); err != nil {
		t.Skip("hard links are unsupported on this platform")
	}
	out := prepareGrokFinish(home, server, account, generation, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH, original, true)
	if out.Success || out.Cleanup || len(out.Bundle) != 0 {
		t.Fatal("a hard-linked credential was sealed or its cleanup was confirmed")
	}
}
