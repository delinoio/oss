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
	written, err := bindGrokHome(t, home).ReadAuthFile()
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

func bindGrokHome(t *testing.T, home string) subscription.GrokHome {
	t.Helper()
	bound, err := subscription.BindGrokHome(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bound.Close() })
	return bound
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
		// A bundle-less login lets the native child create auth.json itself, so it
		// must be refused in the same homes.
		for name, unsafe := range map[string]string{"group-readable": open, "symlinked": link} {
			if _, err := grokWorkerLease(t, server, account, "", nil).materializeGrok(unsafe); err == nil {
				t.Fatalf("a %s home was accepted for a fresh login", name)
			}
		}
	}
}

func TestGrokFreshLoginRefusesLeftoverCredentialFiles(t *testing.T) {
	server, account := domain.NewID(), domain.NewID()
	for name, leftover := range map[string]string{
		"credential": subscription.GrokAuthFileName,
		"temporary":  security.OwnedTemporaryPrefix(subscription.GrokAuthFileName) + "synthetic",
	} {
		t.Run(name, func(t *testing.T) {
			home := grokHome(t)
			if err := os.WriteFile(filepath.Join(home, leftover), []byte("synthetic"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := grokWorkerLease(t, server, account, "", nil).materializeGrok(home); err == nil {
				t.Fatal("a fresh login accepted a home that already holds a credential")
			}
		})
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
	out := prepareGrokFinish(bindGrokHome(t, home), server, account, generation, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH, original, true)
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
	out = prepareGrokFinish(bindGrokHome(t, home), server, account, generation, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH, original, true)
	if out.Success || len(out.Bundle) != 0 || !out.Cleanup {
		t.Fatal("a foreign identity was sealed or the file was kept")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("auth.json survived a rejected identity")
	}

	// Logout and a failed operation return no bundle but still retire the file.
	for name, action := range map[string]pb.SubscriptionAction{"logout": pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT, "failed-login": pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN} {
		write(grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z"))
		out = prepareGrokFinish(bindGrokHome(t, home), server, account, generation, action, subscription.GrokBundle{}, name == "logout")
		if len(out.Bundle) != 0 || !out.Cleanup {
			t.Fatalf("%s returned a bundle or kept the file", name)
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("%s left auth.json", name)
		}
	}

	// An unparsable native file downgrades to an unsuccessful Finish.
	write([]byte(`{"x":1}`))
	out = prepareGrokFinish(bindGrokHome(t, home), server, account, generation, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, subscription.GrokBundle{}, true)
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
	out := prepareGrokFinish(bindGrokHome(t, home), server, account, generation, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, subscription.GrokBundle{}, true)
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
	out := prepareGrokFinish(bindGrokHome(t, home), server, account, generation, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH, original, true)
	if out.Success || out.Cleanup || len(out.Bundle) != 0 {
		t.Fatal("a hard-linked credential was sealed or its cleanup was confirmed")
	}
}

// The credential was materialized in one directory. If that directory is renamed
// away, a path lookup finds either nothing (an absent parent reads as clean) or a
// replacement, so cleanup is never confirmed and no bundle is returned, for a
// logout (which never seals) as well as for a refresh. Nothing is removed from
// the substitute path, and the original directory keeps its file for recovery.
func TestPrepareGrokFinishRefusesARenamedOrReplacedHome(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	original, _ := subscription.ParseGrokAuthFile(grokWorkerFile(t, "synthetic-principal-1", "one", "2026-10-14T06:00:00Z"))
	for name, replace := range map[string]func(t *testing.T, home string){
		"renamed-away": func(t *testing.T, home string) {},
		"replaced-by-empty-home": func(t *testing.T, home string) {
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
		},
		"replaced-by-planted-credential": func(t *testing.T, home string) {
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, subscription.GrokAuthFileName), grokWorkerFile(t, "synthetic-principal-1", "planted", "2026-10-14T18:00:00Z"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		for action, success := range map[pb.SubscriptionAction]bool{
			pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT:  true,
			pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH: true,
			pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN:   false,
		} {
			t.Run(name+"/"+action.String(), func(t *testing.T) {
				home := grokHome(t)
				path := filepath.Join(home, subscription.GrokAuthFileName)
				if err := os.WriteFile(path, grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z"), 0o600); err != nil {
					t.Fatal(err)
				}
				bound := bindGrokHome(t, home)
				moved := home + ".moved"
				if err := os.Rename(home, moved); err != nil {
					t.Fatal(err)
				}
				replace(t, home)
				planted, plantedErr := os.ReadFile(filepath.Join(home, subscription.GrokAuthFileName))
				out := prepareGrokFinish(bound, server, account, generation, action, original, success)
				if out.Cleanup || out.Success || len(out.Bundle) != 0 {
					t.Fatal("cleanup was confirmed or a bundle was returned for a home that is no longer the original")
				}
				if _, err := os.Lstat(filepath.Join(moved, subscription.GrokAuthFileName)); err != nil {
					t.Fatal("the original directory's credential was touched through the substitute path")
				}
				if after, err := os.ReadFile(filepath.Join(home, subscription.GrokAuthFileName)); (err == nil) != (plantedErr == nil) || !bytes.Equal(after, planted) {
					t.Fatal("the substitute directory was modified")
				}
			})
		}
	}
}

// A lease that never bound a home has no directory to retire in, so it can
// never confirm cleanup.
func TestPrepareGrokFinishRefusesAnUnboundHome(t *testing.T) {
	out := prepareGrokFinish(subscription.GrokHome{}, domain.NewID(), domain.NewID(), domain.NewID(), pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT, subscription.GrokBundle{}, true)
	if out.Cleanup || out.Success || len(out.Bundle) != 0 {
		t.Fatal("an unbound home confirmed cleanup")
	}
}

// Materialization binds the home that Finish later verifies.
func TestGrokMaterializationBindsTheHomeForFinish(t *testing.T) {
	server, account := domain.NewID(), domain.NewID()
	lease := grokWorkerLease(t, server, account, "", nil)
	home := grokHome(t)
	if _, err := lease.materializeGrok(home); err != nil {
		t.Fatal(err)
	}
	if lease.grokHome.Path() != home || lease.grokHome.Verify() != nil {
		t.Fatal("materialization did not bind the home")
	}
}

// A failed materialization releases the directory handle it bound and leaves no
// bound home on the lease, so Finish can never act on a half-bound lease.
func TestGrokMaterializationReleasesTheHomeWhenItFails(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	lease := grokWorkerLease(t, server, account, generation, []byte(`{"version":1}`))
	if _, err := lease.materializeGrok(grokHome(t)); err == nil {
		t.Fatal("a garbage envelope was materialized")
	}
	if lease.grokHome.Path() != "" || lease.grokHome.Verify() == nil {
		t.Fatal("a failed materialization kept a bound home")
	}
}

const grokExecuteAction = pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE

// grokExitFixture is an execution whose native process has exited: the home holds
// the credential the process left behind and the native lock file.
type grokExitFixture struct {
	server, account, generation domain.ID
	original                    subscription.GrokBundle
	home, path, lock            string
}

func newGrokExitFixture(t *testing.T) *grokExitFixture {
	t.Helper()
	f := &grokExitFixture{server: domain.NewID(), account: domain.NewID(), generation: domain.NewID(), home: grokHome(t)}
	f.original, _ = subscription.ParseGrokAuthFile(grokWorkerFile(t, "synthetic-principal-1", "one", "2026-10-14T06:00:00Z"))
	f.path = filepath.Join(f.home, subscription.GrokAuthFileName)
	f.lock = filepath.Join(f.home, subscription.GrokLockFileName)
	return f
}

// leave writes the file the native process left behind, with its lock file.
func (f *grokExitFixture) leave(t *testing.T, raw []byte) {
	t.Helper()
	if err := os.WriteFile(f.path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *grokExitFixture) exit(t *testing.T, processOK bool) grokFinishOutcome {
	t.Helper()
	return prepareGrokFinish(bindGrokHome(t, f.home), f.server, f.account, f.generation, grokExecuteAction, f.original, processOK)
}

func (f *grokExitFixture) assertRetired(t *testing.T) {
	t.Helper()
	for _, path := range []string{f.path, f.lock} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("%s survived the execution exit", filepath.Base(path))
		}
	}
}

func TestGrokExecutionExitReturnsASelfRefreshedCredentialAndRetiresTheProfile(t *testing.T) {
	for name, processOK := range map[string]bool{"clean exit": true, "failed process": false} {
		t.Run(name, func(t *testing.T) {
			f := newGrokExitFixture(t)
			f.leave(t, grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z"))
			out := f.exit(t, processOK)
			if !out.Success || !out.Cleanup || !out.Refreshed || len(out.Bundle) == 0 {
				t.Fatal("a self-refreshed credential was not returned")
			}
			next, err := subscription.OpenGrokBundle(out.Bundle, f.server, f.account, f.generation)
			if err != nil || subscription.GrokRefreshed(f.original, next) != nil {
				t.Fatal("the returned bundle is not the verified successor bound to the next generation")
			}
			f.assertRetired(t)
		})
	}
}

func TestGrokExecutionExitWithAnUnchangedCredentialReturnsNothingButRetiresTheProfile(t *testing.T) {
	f := newGrokExitFixture(t)
	f.leave(t, grokWorkerFile(t, "synthetic-principal-1", "one", "2026-10-14T06:00:00Z"))
	out := f.exit(t, true)
	if !out.Success || !out.Cleanup || out.Refreshed || len(out.Bundle) != 0 {
		t.Fatal("an unchanged credential must be released without a replacement bundle")
	}
	f.assertRetired(t)
}

func TestGrokExecutionExitKeepsTheLeaseForAnythingItCannotVerify(t *testing.T) {
	cases := map[string][]byte{
		"another identity":       grokWorkerFile(t, "synthetic-principal-2", "two", "2026-10-14T12:00:00Z"),
		"unreadable file":        []byte(`{"x":1}`),
		"empty file":             []byte(""),
		"earlier expiry":         grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T03:00:00Z"),
		"same tokens new expiry": grokWorkerFile(t, "synthetic-principal-1", "one", "2026-10-14T12:00:00Z"),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			f := newGrokExitFixture(t)
			f.leave(t, raw)
			out := f.exit(t, true)
			// Success is withheld, so Finish fences the account instead of releasing
			// the lease. No bundle ever accompanies it, and the profile is still
			// cleaned so no credential stays on disk.
			if out.Success || out.Refreshed || len(out.Bundle) != 0 {
				t.Fatal("an unverifiable exit was reported as success or returned a bundle")
			}
			f.assertRetired(t)
		})
	}
}

func TestGrokExecutionExitWithAMissingFileKeepsTheLease(t *testing.T) {
	f := newGrokExitFixture(t)
	if out := f.exit(t, true); out.Success || len(out.Bundle) != 0 {
		t.Fatal("an absent auth.json was reported as a verified exit")
	}
}

func TestGrokExecutionExitNeverConfirmsCleanupWhileTheLockFileRemains(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory replacement semantics differ")
	}
	f := newGrokExitFixture(t)
	if err := os.WriteFile(f.path, grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The lock path is a directory, which cannot be proven removable.
	if err := os.Mkdir(f.lock, 0o700); err != nil {
		t.Fatal(err)
	}
	out := f.exit(t, true)
	if out.Cleanup || out.Refreshed || len(out.Bundle) != 0 {
		t.Fatal("cleanup was confirmed or a bundle was returned with an unretired lock file")
	}
}

func TestGrokExecutionExitRefusesARenamedOrReplacedHome(t *testing.T) {
	f := newGrokExitFixture(t)
	f.leave(t, grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z"))
	bound := bindGrokHome(t, f.home)
	if err := os.Rename(f.home, f.home+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(f.home); err != nil {
		t.Fatal(err)
	}
	out := prepareGrokFinish(bound, f.server, f.account, f.generation, grokExecuteAction, f.original, true)
	if out.Success || out.Cleanup || out.Refreshed || len(out.Bundle) != 0 {
		t.Fatal("a replaced home produced a success, a confirmed cleanup or a bundle")
	}
}
