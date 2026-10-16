// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func grokServerBundle(t *testing.T, principal string) subscription.GrokBundle {
	t.Helper()
	raw, err := json.Marshal(map[string]any{subscription.GrokScopeKey: map[string]any{
		"key": "synthetic-key", "auth_mode": "oidc", "create_time": "2026-10-14T00:00:00Z", "expires_at": "2026-10-14T06:00:00Z",
		"user_id": "synthetic-user", "principal_type": "user", "principal_id": principal, "refresh_token": "synthetic-refresh",
		"oidc_issuer": subscription.GrokIssuer, "oidc_client_id": subscription.GrokClientID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := subscription.ParseGrokAuthFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestGrokIdentityCommitmentRejectsDuplicateOwnershipPerPrincipal(t *testing.T) {
	f := newSubscriptionFixture(t)
	first, same, other := grokServerBundle(t, "synthetic-principal-1"), grokServerBundle(t, "synthetic-principal-1"), grokServerBundle(t, "synthetic-principal-2")
	commitment := f.service.grokIdentityCommitment(first)
	if !domain.NativeIdentityCommitmentValid(commitment) || commitment == first.IdentityKey() {
		t.Fatal("commitment must be a keyed 32-byte hex value distinct from the bundle key")
	}
	if f.service.grokIdentityCommitment(same) != commitment || f.service.grokIdentityCommitment(other) == commitment || f.service.grokIdentityCommitment(subscription.GrokBundle{}) != "" {
		t.Fatal("commitment must follow the identity key")
	}
	owner := domain.NewID()
	_, account := f.record()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.owner", nil, func(tx *store.Tx) (any, error) {
		account.SubscriptionService = domain.SubscriptionGrok
		account.Subscription = &domain.SubscriptionState{IdentityCommitment: commitment}
		return tx.Put(domain.AccountKind, owner, 0, "", "", account)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		candidate := domain.NewID()
		if err := uniqueSubscriptionIdentity(tx, candidate, f.service.grokIdentityCommitment(same)); err == nil || domain.SafeError(err).Code != domain.Conflict {
			t.Error("second account with the same Grok identity was not rejected")
		} else if strings.Contains(err.Error(), "synthetic") {
			t.Error("duplicate rejection echoed a bundle value")
		}
		if err := uniqueSubscriptionIdentity(tx, candidate, f.service.grokIdentityCommitment(other)); err != nil {
			t.Error("a different principal for the same user was rejected")
		}
		if err := uniqueSubscriptionIdentity(tx, owner, commitment); err != nil {
			t.Error("the owner was rejected by its own identity")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCodexSubscriptionPathNeverServesGrokAccount(t *testing.T) {
	f := newSubscriptionFixture(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.service", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.SubscriptionService = domain.SubscriptionGrok
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		if _, _, err := subscriptionAccount(tx, f.input.AccountID, 0); err == nil || domain.SafeError(err).Code != domain.Unsupported {
			t.Error("the Codex lifecycle accepted a Grok account")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// grokFixture drives the Grok lifecycle over the real Connect handlers. Grok
// login cannot be requested yet, so queued operations are seeded directly.
type grokFixture struct{ *subscriptionFixture }

func newGrokFixture(t *testing.T) *grokFixture {
	t.Helper()
	f := &grokFixture{newSubscriptionFixture(t)}
	f.service.grokLoginPerform = func(ctx context.Context, id domain.ID, op domain.ServerSubscriptionOperation, canceled *atomic.Bool) grokLoginOutcome {
		var device bool
		_ = f.service.Store.Read(ctx, func(tx *store.Tx) error {
			_, a, err := grokSubscriptionAccount(tx, id, 0)
			if err == nil {
				device = a.Subscription.Pending.DeviceCode
			}
			return err
		})
		if device {
			return f.service.performGrokDeviceLogin(ctx, id, op, canceled)
		}
		return f.service.performGrokLogin(ctx, id, op, canceled)
	}
	f.service.grokLogoutPerform = f.service.performGrokLogout
	observed := time.Now().UTC()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.account", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.SubscriptionService = domain.SubscriptionGrok
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		mr, err := tx.Get(domain.MachineKind, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine, err := store.Decode[domain.Machine](mr)
		if err != nil {
			return nil, err
		}
		machine.Installations = []domain.Installation{{Harness: domain.GrokBuild, State: domain.InstallationDetected, Version: domain.GrokSubscriptionBaselineVersion, ResolvedPath: "/controlled/native/grok", ProtocolVerified: true, ObservedAt: &observed, Protocol: &domain.ProtocolObservation{Protocol: domain.ProtocolFor(domain.GrokBuild), State: domain.ProtocolVerified}}}
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *grokFixture) queue(action pb.SubscriptionAction) *pb.RequestSubscriptionResponse {
	f.t.Helper()
	op := domain.NewID()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.queue", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		if a.Subscription == nil {
			a.Subscription = &domain.SubscriptionState{}
		}
		a.Subscription.Pending = &domain.SubscriptionOperation{ID: op, Action: subscriptionAction(action), MachineID: f.input.MachineID, Actor: domain.Principal{Type: domain.OwnerDevice}, Phase: domain.SubscriptionQueued}
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return &pb.RequestSubscriptionResponse{OperationId: string(op)}
}

// sealed builds the Finish bundle bound to the generation the Finish creates.
func (f *grokFixture) sealed(t *testing.T, account, generation domain.ID, principal string) []byte {
	t.Helper()
	raw, err := subscription.SealGrokBundle(f.service.Identity.ServerID, account, generation, grokServerBundle(t, principal))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f *grokFixture) finishWith(lease *pb.TakeSubscriptionResponse, request domain.ID, bundle []byte, success, refresh, cleanup bool) (*pb.FinishSubscriptionResponse, error) {
	response, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(request), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Bundle: bundle, Succeeded: success, RefreshConfirmed: refresh, CleanupConfirmed: cleanup}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

// login completes one Grok login for principal and returns its generation.
func (f *grokFixture) login(principal string) domain.ID {
	f.t.Helper()
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN), pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		f.t.Fatal(err)
	}
	if len(lease.Bundle) != 0 || lease.GenerationId != "" {
		f.t.Fatal("fresh Grok login borrowed existing credentials")
	}
	generation := domain.NewID()
	if _, err := f.finishWith(lease, generation, f.sealed(f.t, f.input.AccountID, generation, principal), true, false, true); err != nil {
		f.t.Fatal(err)
	}
	return generation
}

func TestGrokLifecycleMovesBundleOnlyThroughProtectedTakeAndFinish(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	_, a := f.record()
	st := a.Subscription
	want := f.service.grokIdentityCommitment(grokServerBundle(t, "synthetic-principal-1"))
	if st.Lease != nil || st.Pending != nil || st.Generation != generation || st.IdentityCommitment != want || a.Connection == nil || a.Health != domain.AccountReady {
		t.Fatal("login did not store the exact generation and identity commitment")
	}
	stored, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin})
	if err != nil || !subscription.GrokBundleEnvelope(stored) {
		t.Fatal("the vault does not hold the sealed Grok envelope")
	}
	if refs, err := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID); err != nil || len(refs) != 1 {
		t.Fatal("login left unexpected protected generations")
	}
	// No secret or unkeyed identity key may reach durable metadata or responses.
	for _, name := range []string{"state.sqlite", "state.sqlite-wal", "state.sqlite-shm"} {
		data, err := os.ReadFile(filepath.Join(f.service.Store.Root(), name))
		if err != nil {
			continue
		}
		for _, secret := range []string{"synthetic-refresh", "synthetic-key", grokServerBundle(t, "synthetic-principal-1").IdentityKey()} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatal("secret in durable metadata")
			}
		}
	}

	// Refresh receives exactly the stored envelope and must write back a rotated
	// credential for the same identity under a new generation.
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(lease.Bundle, stored) || lease.GenerationId != string(generation) {
		t.Fatal("wrong generation delivered")
	}
	rotated := domain.NewID()
	raw, err := json.Marshal(map[string]any{subscription.GrokScopeKey: map[string]any{
		"key": "synthetic-key-rotated", "auth_mode": "oidc", "create_time": "2026-10-14T00:00:00Z", "expires_at": "2026-10-14T12:00:00Z",
		"user_id": "synthetic-user", "principal_type": "user", "principal_id": "synthetic-principal-1", "refresh_token": "synthetic-refresh-rotated",
		"oidc_issuer": subscription.GrokIssuer, "oidc_client_id": subscription.GrokClientID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	next, err := subscription.ParseGrokAuthFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := subscription.SealGrokBundle(f.service.Identity.ServerID, f.input.AccountID, rotated, next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.finishWith(lease, rotated, sealed, true, true, true); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	if a.Subscription.Generation != rotated || a.Subscription.IdentityCommitment != want || a.Subscription.Lease != nil {
		t.Fatal("refresh did not publish the new generation under the same identity")
	}
	if refs, _ := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID); len(refs) != 1 || refs[0].ID != rotated {
		t.Fatal("refresh retained the superseded generation")
	}

	// Logout returns the bundle for native cleanup and removes every reference.
	lease, err = f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT), pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	if err != nil || len(lease.Bundle) == 0 {
		t.Fatal("logout did not receive the current envelope")
	}
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	if a.Connection != nil || a.Health != domain.AccountDisconnected || a.Subscription.Generation != "" || a.Subscription.IdentityCommitment != "" {
		t.Fatal("logout retained usable authentication")
	}
	if refs, err := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID); err != nil || len(refs) != 0 {
		t.Fatal("logout left protected generations")
	}
}

func TestGrokFinishRejectsReboundAndForeignBundles(t *testing.T) {
	for name, build := range map[string]func(f *grokFixture, generation domain.ID) []byte{
		"other-account": func(f *grokFixture, g domain.ID) []byte {
			return f.sealed(t, domain.NewID(), g, "synthetic-principal-1")
		},
		"other-generation": func(f *grokFixture, g domain.ID) []byte {
			return f.sealed(t, f.input.AccountID, domain.NewID(), "synthetic-principal-1")
		},
		"codex-bundle": func(f *grokFixture, g domain.ID) []byte {
			return subscriptionTestBundle("fixture-account", "first", time.Now().UTC().Add(-time.Minute))
		},
		"native-file": func(f *grokFixture, g domain.ID) []byte {
			raw, _ := grokServerBundle(t, "synthetic-principal-1").AuthFile()
			return raw
		},
		"empty": func(f *grokFixture, g domain.ID) []byte { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			f := newGrokFixture(t)
			lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN), pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			if err != nil {
				t.Fatal(err)
			}
			generation := domain.NewID()
			_, err = f.finishWith(lease, generation, build(f, generation), true, false, true)
			if err == nil {
				t.Fatal("rebound bundle was accepted")
			}
			if strings.Contains(err.Error(), "synthetic") {
				t.Fatal("rejection echoed a bundle value")
			}
			_, a := f.record()
			if a.Connection != nil || a.Subscription.Generation != "" || a.Subscription.IdentityCommitment != "" || !a.Subscription.RecoveryRequired {
				t.Fatal("rejected bundle changed account authority or skipped the recovery fence")
			}
			if refs, _ := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID); len(refs) != 0 {
				t.Fatal("rejected bundle was staged in the vault")
			}
		})
	}
}

func TestGrokLoginRejectsDuplicateIdentityAndAdmitsDifferentPrincipal(t *testing.T) {
	f := newGrokFixture(t)
	other := domain.NewID()
	_, account := f.record()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.other", nil, func(tx *store.Tx) (any, error) {
		account.Subscription = &domain.SubscriptionState{IdentityCommitment: f.service.grokIdentityCommitment(grokServerBundle(t, "synthetic-principal-1"))}
		return tx.Put(domain.AccountKind, other, 0, "", "", account)
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN), pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	generation := domain.NewID()
	_, err = f.finishWith(lease, generation, f.sealed(t, f.input.AccountID, generation, "synthetic-principal-1"), true, false, true)
	if err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict {
		t.Fatal("a second account with the same Grok identity was not rejected")
	}
	if refs, _ := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID); len(refs) != 0 {
		t.Fatal("duplicate identity staged a vault generation")
	}

	// A different principal of the same user is a distinct identity.
	g := newGrokFixture(t)
	_, existing := g.record()
	_, err = g.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.other", nil, func(tx *store.Tx) (any, error) {
		existing.Subscription = &domain.SubscriptionState{IdentityCommitment: g.service.grokIdentityCommitment(grokServerBundle(t, "synthetic-principal-1"))}
		return tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", existing)
	})
	if err != nil {
		t.Fatal(err)
	}
	g.login("synthetic-principal-2")
	_, a := g.record()
	if a.Subscription.IdentityCommitment != g.service.grokIdentityCommitment(grokServerBundle(t, "synthetic-principal-2")) {
		t.Fatal("a different principal was not admitted")
	}
}

func TestGrokRefreshRequiresSameIdentityAndRotation(t *testing.T) {
	for name, principal := range map[string]string{"other-principal": "synthetic-principal-2", "unrotated": "synthetic-principal-1"} {
		t.Run(name, func(t *testing.T) {
			f := newGrokFixture(t)
			generation := f.login("synthetic-principal-1")
			lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
			if err != nil {
				t.Fatal(err)
			}
			next := domain.NewID()
			if _, err := f.finishWith(lease, next, f.sealed(t, f.input.AccountID, next, principal), true, true, true); err == nil {
				t.Fatal("invalid refresh was accepted")
			}
			_, a := f.record()
			if a.Subscription.Generation != generation || !a.Subscription.RecoveryRequired {
				t.Fatal("rejected refresh changed the generation or skipped the recovery fence")
			}
		})
	}
}

func TestGrokTakeRefusesUnsupportedAndUnverifiedWork(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	// A Worker without the managed Grok capability never receives an execute lease.
	if _, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT), pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE); err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.Unsupported {
		t.Fatal("Grok execute Take was not refused")
	}
	if _, err := f.start2(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH); err == nil {
		t.Fatal("the Codex lifecycle accepted a Grok request")
	}

	g := newGrokFixture(t)
	op := g.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	_, err := g.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.uninstall", nil, func(tx *store.Tx) (any, error) {
		mr, err := tx.Get(domain.MachineKind, g.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine, err := store.Decode[domain.Machine](mr)
		if err != nil {
			return nil, err
		}
		machine.Installations[0].ProtocolVerified = false
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.Unsupported {
		t.Fatal("Take admitted an unverified Grok installation")
	}
	_, a := g.record()
	if a.Subscription.Lease != nil {
		t.Fatal("a refused Take created a lease")
	}
}

// setGrokInstallation rewrites the Runner Device's discovered installations the
// way a completed harness discovery replaces them.
func (f *grokFixture) setGrokInstallation(t *testing.T, edit func(*[]domain.Installation)) {
	t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.installations", nil, func(tx *store.Tx) (any, error) {
		mr, err := tx.Get(domain.MachineKind, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine, err := store.Decode[domain.Machine](mr)
		if err != nil {
			return nil, err
		}
		edit(&machine.Installations)
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGrokTakeRequiresSubscriptionBaselineVersion(t *testing.T) {
	for _, version := range []string{domain.GrokProtocolVersion, "1.0.45", "1.0.46-rc.1", "not-a-version"} {
		t.Run(version, func(t *testing.T) {
			f := newGrokFixture(t)
			f.setGrokInstallation(t, func(installations *[]domain.Installation) { (*installations)[0].Version = version })
			if _, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN), pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.Unsupported {
				t.Fatal("Take admitted a Grok installation below the subscription baseline")
			}
			if _, a := f.record(); a.Subscription.Lease != nil || a.Subscription.RecoveryRequired {
				t.Fatal("a refused Take claimed the lease or fenced the account")
			}
		})
	}
	// The baseline and newer releases are admitted by SemVer precedence.
	for _, version := range []string{domain.GrokSubscriptionBaselineVersion + "+build.1", "1.0.47", "1.0.52"} {
		f := newGrokFixture(t)
		f.setGrokInstallation(t, func(installations *[]domain.Installation) { (*installations)[0].Version = version })
		f.login("synthetic-principal-1")
	}
}

// Harness discovery replaces the installation list at any time. A lease that
// Take already claimed must stay finishable by its exact original owner, so the
// Worker can report cleanup and release the account.
func TestGrokFinishSurvivesInstallationRediscoveryAfterTake(t *testing.T) {
	rediscovery := map[string]func(*[]domain.Installation){
		"missing": func(i *[]domain.Installation) { *i = nil },
		"failed": func(i *[]domain.Installation) {
			(*i)[0].State = domain.InstallationFailed
			(*i)[0].ProtocolVerified = false
		},
		"duplicated":   func(i *[]domain.Installation) { *i = append(*i, (*i)[0]) },
		"unsupported":  func(i *[]domain.Installation) { (*i)[0].Version = "1.0.45" },
		"unverified":   func(i *[]domain.Installation) { (*i)[0].ProtocolVerified = false },
		"stale-legacy": func(i *[]domain.Installation) { (*i)[0].Version = domain.GrokProtocolVersion },
	}
	for name, edit := range rediscovery {
		t.Run(name+"/success", func(t *testing.T) {
			f := newGrokFixture(t)
			lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN), pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			if err != nil {
				t.Fatal(err)
			}
			f.setGrokInstallation(t, edit)
			generation := domain.NewID()
			if _, err := f.finishWith(lease, generation, f.sealed(t, f.input.AccountID, generation, "synthetic-principal-1"), true, false, true); err != nil {
				t.Fatalf("Finish failed after installation rediscovery: %v", err)
			}
			_, a := f.record()
			if a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Subscription.Generation != generation || a.Connection == nil {
				t.Fatal("the original owner could not complete its lease after rediscovery")
			}
		})
		t.Run(name+"/cleanup-only", func(t *testing.T) {
			f := newGrokFixture(t)
			lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN), pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			if err != nil {
				t.Fatal(err)
			}
			f.setGrokInstallation(t, edit)
			if _, err := f.finishWith(lease, domain.NewID(), nil, false, false, true); err != nil {
				t.Fatalf("confirmed cleanup could not release the lease after rediscovery: %v", err)
			}
			if _, a := f.record(); a.Subscription.Lease != nil || a.Subscription.RecoveryRequired {
				t.Fatal("the lease stayed fenced after confirmed cleanup")
			}
		})
	}
}

// takeWith sends a Grok Take under an explicit request identifier so a lost
// response can be retried with the exact original request.
func (f *grokFixture) takeWith(request domain.ID, revision uint64, op *pb.RequestSubscriptionResponse, action pb.SubscriptionAction) (*pb.TakeSubscriptionResponse, error) {
	response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(request), Id: string(f.input.AccountID), ExpectedRevision: revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: op.OperationId, Action: action}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

// When the first Take response is lost, the Worker retries the same request. The
// receipt never redistributes the credential, and the unfinished lease the
// Worker can no longer finish is fenced for recovery.
func TestGrokReplayedTakeFencesTheUnfinishedLease(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	op := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	request := domain.NewID()
	observed, _ := f.record()
	first, err := f.takeWith(request, observed.Revision, op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil || len(first.Bundle) == 0 {
		t.Fatal("the first Take did not deliver the envelope")
	}
	if _, a := f.record(); a.Subscription.RecoveryRequired || a.Subscription.Lease == nil {
		t.Fatal("a delivered lease was fenced before any replay")
	}
	replay, err := f.takeWith(request, observed.Revision, op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err == nil || replay != nil {
		t.Fatal("a replayed receipt redistributed the credential")
	}
	_, a := f.record()
	if !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil || a.Health != domain.AccountFailed || a.Subscription.Generation != generation {
		t.Fatal("the replay did not fence the unfinished lease for recovery")
	}
	rotated := domain.NewID()
	if _, err := f.finishWith(first, rotated, f.sealed(t, f.input.AccountID, rotated, "synthetic-principal-1"), true, true, true); err == nil {
		t.Fatal("a recovery-fenced lease accepted a new generation")
	}
}

// A replay after the lease was finished or replaced must not fence a healthy
// account.
func TestGrokLateReplayedTakeDoesNotFenceAReleasedAccount(t *testing.T) {
	f := newGrokFixture(t)
	op := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	request := domain.NewID()
	observed, _ := f.record()
	lease, err := f.takeWith(request, observed.Revision, op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	generation := domain.NewID()
	if _, err := f.finishWith(lease, generation, f.sealed(t, f.input.AccountID, generation, "synthetic-principal-1"), true, false, true); err != nil {
		t.Fatal(err)
	}
	if replay, err := f.takeWith(request, observed.Revision, op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); err == nil || replay != nil {
		t.Fatal("a late replay redistributed a credential")
	}
	if _, a := f.record(); a.Subscription.RecoveryRequired || a.Subscription.Lease != nil || a.Health != domain.AccountReady || a.Subscription.Generation != generation {
		t.Fatal("a late replay fenced an already released account")
	}
}

// orderedGrokSecrets records every vault deletion that happens while the
// account document still references the deleted generation.
type orderedGrokSecrets struct {
	*accountTestSecrets
	f          *grokFixture
	deletes    int
	prematures int
}

func (v *orderedGrokSecrets) Delete(ctx context.Context, ref credentials.Ref) error {
	v.deletes++
	if _, a := v.f.record(); a.Subscription != nil && a.Subscription.Generation == ref.ID {
		v.prematures++
	}
	return v.accountTestSecrets.Delete(ctx, ref)
}

// A superseded generation is removed only after the account document durably
// stops referencing it, for both a refresh and a logout.
func TestGrokFinishDeletesTheReferencedGenerationOnlyAfterPublication(t *testing.T) {
	f := newGrokFixture(t)
	vault := &orderedGrokSecrets{accountTestSecrets: f.secrets, f: f}
	f.service.accountSecrets = vault
	f.login("synthetic-principal-1")
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal(err)
	}
	rotated := domain.NewID()
	raw, err := json.Marshal(map[string]any{subscription.GrokScopeKey: map[string]any{
		"key": "synthetic-key-rotated", "auth_mode": "oidc", "create_time": "2026-10-14T00:00:00Z", "expires_at": "2026-10-14T12:00:00Z",
		"user_id": "synthetic-user", "principal_type": "user", "principal_id": "synthetic-principal-1", "refresh_token": "synthetic-refresh-rotated",
		"oidc_issuer": subscription.GrokIssuer, "oidc_client_id": subscription.GrokClientID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	next, err := subscription.ParseGrokAuthFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := subscription.SealGrokBundle(f.service.Identity.ServerID, f.input.AccountID, rotated, next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.finishWith(lease, rotated, sealed, true, true, true); err != nil {
		t.Fatal(err)
	}
	if vault.deletes != 1 || vault.prematures != 0 {
		t.Fatalf("refresh removed the superseded generation while it was still referenced (%d deletes, %d premature)", vault.deletes, vault.prematures)
	}
	lease, err = f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT), pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal(err)
	}
	if vault.deletes != 2 || vault.prematures != 0 {
		t.Fatalf("logout removed the generation while it was still referenced (%d deletes, %d premature)", vault.deletes, vault.prematures)
	}
}

// A publication that fails after the new generation is staged must leave the
// account pointing at a generation that still exists.
func TestGrokFinishFailedPublicationKeepsTheCurrentGeneration(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal(err)
	}
	f.secrets.afterPut = func() {
		_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.revoke-grok-worker", nil, func(tx *store.Tx) (any, error) {
			r, err := tx.Get(domain.DeviceKind, f.device)
			if err != nil {
				return nil, err
			}
			d, err := store.Decode[domain.Device](r)
			if err != nil {
				return nil, err
			}
			d.Revoked = true
			_, err = tx.Put(domain.DeviceKind, r.ID, r.Revision, "", "", d)
			return nil, err
		})
		if err != nil {
			t.Error(err)
		}
	}
	rotated := domain.NewID()
	raw, err := json.Marshal(map[string]any{subscription.GrokScopeKey: map[string]any{
		"key": "synthetic-key-rotated", "auth_mode": "oidc", "create_time": "2026-10-14T00:00:00Z", "expires_at": "2026-10-14T12:00:00Z",
		"user_id": "synthetic-user", "principal_type": "user", "principal_id": "synthetic-principal-1", "refresh_token": "synthetic-refresh-rotated",
		"oidc_issuer": subscription.GrokIssuer, "oidc_client_id": subscription.GrokClientID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	next, err := subscription.ParseGrokAuthFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := subscription.SealGrokBundle(f.service.Identity.ServerID, f.input.AccountID, rotated, next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.finishWith(lease, rotated, sealed, true, true, true); err == nil {
		t.Fatal("a revoked Worker published a generation")
	}
	_, a := f.record()
	if a.Subscription.Generation != generation || a.Subscription.Lease == nil || !a.Subscription.RecoveryRequired {
		t.Fatal("a failed publication changed the generation or released uncertain ownership")
	}
	if stored, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}); err != nil || len(stored) == 0 {
		t.Fatal("a failed publication deleted the generation the account still references")
	}
	if refs, err := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID); err != nil || len(refs) != 1 || refs[0].ID != generation {
		t.Fatal("an aborted publication left the staged generation beside the current one")
	}
}

// grokRotatedEnvelope seals a rotated credential for the same principal under
// generation.
func (f *grokFixture) grokRotatedEnvelope(generation domain.ID) []byte {
	f.t.Helper()
	raw, err := json.Marshal(map[string]any{subscription.GrokScopeKey: map[string]any{
		"key": "synthetic-key-rotated", "auth_mode": "oidc", "create_time": "2026-10-14T00:00:00Z", "expires_at": "2026-10-14T12:00:00Z",
		"user_id": "synthetic-user", "principal_type": "user", "principal_id": "synthetic-principal-1", "refresh_token": "synthetic-refresh-rotated",
		"oidc_issuer": subscription.GrokIssuer, "oidc_client_id": subscription.GrokClientID,
	}})
	if err != nil {
		f.t.Fatal(err)
	}
	next, err := subscription.ParseGrokAuthFile(raw)
	if err != nil {
		f.t.Fatal(err)
	}
	sealed, err := subscription.SealGrokBundle(f.service.Identity.ServerID, f.input.AccountID, generation, next)
	if err != nil {
		f.t.Fatal(err)
	}
	return sealed
}

// An abort whose staged generation cannot be removed still fences the account and
// keeps the current generation; the unreferenced generation stays vault-owned
// (never granted) until the next cleanup removes it.
func TestGrokFinishAbortWithFailedRetirementStaysFencedAndNeverGrantsTheStagedGeneration(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal(err)
	}
	f.secrets.afterPut = func() {
		f.secrets.mu.Lock()
		f.secrets.deleteError = domain.Fail(domain.Unavailable, "The vault removal failed.", "")
		f.secrets.mu.Unlock()
		_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.revoke-grok-worker", nil, func(tx *store.Tx) (any, error) {
			r, err := tx.Get(domain.DeviceKind, f.device)
			if err != nil {
				return nil, err
			}
			d, err := store.Decode[domain.Device](r)
			if err != nil {
				return nil, err
			}
			d.Revoked = true
			_, err = tx.Put(domain.DeviceKind, r.ID, r.Revision, "", "", d)
			return nil, err
		})
		if err != nil {
			t.Error(err)
		}
	}
	rotated := domain.NewID()
	if _, err := f.finishWith(lease, rotated, f.grokRotatedEnvelope(rotated), true, true, true); err == nil {
		t.Fatal("a revoked Worker published a generation")
	}
	_, a := f.record()
	if a.Subscription.Generation != generation || !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil {
		t.Fatal("an aborted publication released the account or changed its generation")
	}
	if refs, err := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID); err != nil || len(refs) != 2 {
		t.Fatal("the failed removal did not leave the staged generation vault-owned")
	}
	if stored, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}); err != nil || len(stored) == 0 {
		t.Fatal("the failed removal damaged the current generation")
	}
}

// Retirement is bound to proof that the account document does not reference the
// generation: a commit whose caller saw an error keeps the generation it
// published, an unreadable account keeps everything, and only an unreferenced
// generation is removed.
func TestGrokRetireUnpublishedGenerationRequiresProofOfNonPublication(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	staged := domain.NewID()
	ref := func(id domain.ID) credentials.Ref {
		return credentials.Ref{Owner: f.input.AccountID, ID: id, Purpose: credentials.AccountLogin}
	}
	if _, err := f.secrets.Put(context.Background(), ref(staged), []byte("synthetic-staged")); err != nil {
		t.Fatal(err)
	}
	f.service.retireUnpublishedGrokGeneration(context.Background(), f.secrets, f.input.AccountID, domain.NewID(), generation)
	if _, err := f.secrets.Get(context.Background(), ref(generation)); err != nil {
		t.Fatal("the generation the account references was removed")
	}
	f.service.retireUnpublishedGrokGeneration(context.Background(), f.secrets, domain.NewID(), domain.NewID(), staged)
	if _, err := f.secrets.Get(context.Background(), ref(staged)); err != nil {
		t.Fatal("an unreadable account did not keep the generation")
	}
	f.service.retireUnpublishedGrokGeneration(context.Background(), f.secrets, f.input.AccountID, domain.NewID(), staged)
	if _, err := f.secrets.Get(context.Background(), ref(staged)); err == nil {
		t.Fatal("an unreferenced generation was not retired")
	}
	if _, err := f.secrets.Get(context.Background(), ref(generation)); err != nil {
		t.Fatal("retirement removed the current generation")
	}
}

// A logout the Worker could not complete is fenced for recovery and the account
// still references its generation, so the vault must keep that generation.
func TestGrokFinishFencedLogoutKeepsTheReferencedGeneration(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT), pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.finishWith(lease, domain.NewID(), nil, false, false, true); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if !a.Subscription.RecoveryRequired || a.Subscription.Generation != generation || a.Connection == nil {
		t.Fatal("a failed logout did not stay fenced with its connection")
	}
	if stored, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}); err != nil || len(stored) == 0 {
		t.Fatal("a fenced logout deleted the generation the account still references")
	}
}

func (f *grokFixture) start2(action pb.SubscriptionAction) (*pb.RequestSubscriptionResponse, error) {
	r, _ := f.record()
	response, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: action}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

// grokLargestBundle returns a bundle whose canonical native file is exactly the
// accepted maximum. U+2028 always expands to a six-byte escape, so it makes the
// sealed envelope as large as an accepted bundle can be.
func grokLargestBundle(t *testing.T) subscription.GrokBundle {
	t.Helper()
	build := func(pad int) []byte {
		raw, err := json.Marshal(map[string]any{subscription.GrokScopeKey: map[string]any{
			"key": strings.Repeat(" ", 8192), "auth_mode": "oidc", "create_time": "2026-10-14T00:00:00Z", "expires_at": "2026-10-14T06:00:00Z",
			"user_id": "synthetic-user", "principal_type": "user", "principal_id": "synthetic-principal-1", "refresh_token": strings.Repeat("a", pad),
			"oidc_issuer": subscription.GrokIssuer, "oidc_client_id": subscription.GrokClientID,
		}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	pad := 1 + subscription.MaxGrokAuthFile - len(build(1))
	raw := build(pad)
	if len(raw) != subscription.MaxGrokAuthFile {
		t.Fatalf("fixture is %d bytes, want %d", len(raw), subscription.MaxGrokAuthFile)
	}
	bundle, err := subscription.ParseGrokAuthFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

// The server stores the exact sealed envelope in the protected vault, whose
// per-secret limit is independent of the native file bound. The largest
// accepted bundle must stay storable, so an accepted login is never fenced for
// recovery only because its envelope metadata crossed the vault limit.
func TestGrokFinishStoresLargestAcceptedBundleWithinVaultLimit(t *testing.T) {
	if subscription.MaxGrokBundleEnvelope > credentials.MaxSecretBytes {
		t.Fatal("the Grok envelope bound exceeds the vault secret limit")
	}
	f := newGrokFixture(t)
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN), pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	generation := domain.NewID()
	sealed, err := subscription.SealGrokBundle(f.service.Identity.ServerID, f.input.AccountID, generation, grokLargestBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) > credentials.MaxSecretBytes {
		t.Fatalf("sealed envelope is %d bytes, above the vault limit %d", len(sealed), credentials.MaxSecretBytes)
	}
	if _, err := f.finishWith(lease, generation, bytes.Clone(sealed), true, false, true); err != nil {
		t.Fatal("the largest accepted bundle could not be finished")
	}
	_, a := f.record()
	if a.Subscription.RecoveryRequired || a.Subscription.Generation != generation || a.Subscription.Lease != nil {
		t.Fatal("the largest accepted bundle fenced the account or kept the lease")
	}
	stored, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin})
	if err != nil || !bytes.Equal(stored, sealed) {
		t.Fatal("the vault does not hold the exact sealed envelope")
	}
}

// workerContext is a request context carrying the fixture Worker's principal.
func (f *grokFixture) workerContext(parent context.Context) context.Context {
	return domain.WithPrincipal(parent, domain.Principal{Type: domain.WorkerDevice, DeviceID: f.device, MachineID: f.input.MachineID})
}

// The replay fence is mandatory bookkeeping for an already claimed lease, so it
// must complete when the replaying request is canceled or past its deadline, and
// when the very Worker whose request is being settled was revoked meanwhile.
func TestGrokReplayFenceIsDetachedFromTheReplayingRequest(t *testing.T) {
	for name, request := range map[string]func(*grokFixture) (context.Context, context.CancelFunc){
		"canceled": func(f *grokFixture) (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(f.workerContext(context.Background()))
			cancel()
			return ctx, cancel
		},
		"deadline-exceeded": func(f *grokFixture) (context.Context, context.CancelFunc) {
			return context.WithDeadline(f.workerContext(context.Background()), time.Now().Add(-time.Second))
		},
		"revoked-worker": func(f *grokFixture) (context.Context, context.CancelFunc) {
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.revoke-grok-worker", nil, func(tx *store.Tx) (any, error) {
				r, err := tx.Get(domain.DeviceKind, f.device)
				if err != nil {
					return nil, err
				}
				d, err := store.Decode[domain.Device](r)
				if err != nil {
					return nil, err
				}
				d.Revoked = true
				_, err = tx.Put(domain.DeviceKind, r.ID, r.Revision, "", "", d)
				return nil, err
			})
			if err != nil {
				f.t.Fatal(err)
			}
			return context.WithCancel(f.workerContext(context.Background()))
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newGrokFixture(t)
			f.login("synthetic-principal-1")
			op := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
			observed, _ := f.record()
			if _, err := f.takeWith(domain.NewID(), observed.Revision, op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH); err != nil {
				t.Fatal(err)
			}
			_, a := f.record()
			lease := a.Subscription.Lease
			if lease == nil || a.Subscription.RecoveryRequired {
				t.Fatal("the fixture did not leave an unfinished lease")
			}
			ctx, cancel := request(f)
			defer cancel()
			if err := f.service.fenceReplayedGrokTake(ctx, f.input.AccountID, lease.ID, lease.OperationID, lease.MachineID, lease.InstanceID, lease.Action, domain.Principal{Type: domain.WorkerDevice, DeviceID: lease.DeviceID, MachineID: lease.MachineID}); err != nil {
				t.Fatalf("the fence depended on the request context: %v", err)
			}
			if _, a := f.record(); !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil || a.Health != domain.AccountFailed {
				t.Fatal("the unfinished lease was left stranded without RecoveryRequired")
			}
		})
	}
}

// ctxRecordingSecrets records the state of the context each vault deletion saw.
type ctxRecordingSecrets struct {
	*accountTestSecrets
	cancelRequest context.CancelFunc
	canceled      []bool
	bounded       []bool
}

func (v *ctxRecordingSecrets) Delete(ctx context.Context, ref credentials.Ref) error {
	// The request ends while the post-commit removal is in flight.
	if v.cancelRequest != nil {
		v.cancelRequest()
	}
	_, hasDeadline := ctx.Deadline()
	v.canceled = append(v.canceled, ctx.Err() != nil)
	v.bounded = append(v.bounded, hasDeadline)
	return v.accountTestSecrets.Delete(ctx, ref)
}

// The removal of the superseded generation after a committed Finish is mandatory
// bookkeeping: a request that ends mid-removal must neither cancel it nor turn a
// committed publication into a recovery fence, and the removal stays bounded.
func TestGrokFinishCleanupIsDetachedFromTheRequestAndBounded(t *testing.T) {
	f := newGrokFixture(t)
	vault := &ctxRecordingSecrets{accountTestSecrets: f.secrets}
	f.service.accountSecrets = vault
	generation := f.login("synthetic-principal-1")
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(f.workerContext(context.Background()))
	defer cancel()
	vault.cancelRequest = cancel
	rotated := domain.NewID()
	_, _ = f.service.FinishSubscription(ctx, subscriptionRequest(f.workerToken, &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(rotated), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Bundle: f.grokRotatedEnvelope(rotated), Succeeded: true, RefreshConfirmed: true, CleanupConfirmed: true}))
	if len(vault.canceled) != 1 || vault.canceled[0] || !vault.bounded[0] {
		t.Fatalf("the superseded-generation removal ran on the request context (canceled=%v bounded=%v)", vault.canceled, vault.bounded)
	}
	_, a := f.record()
	if a.Subscription.RecoveryRequired || a.Subscription.Lease != nil || a.Subscription.Generation != rotated {
		t.Fatal("a request ending during cleanup undid or fenced a committed publication")
	}
	if _, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}); err == nil {
		t.Fatal("the superseded generation was not removed")
	}
}

// Retiring an unpublished staged generation after an abort runs even when the
// aborted request's context is already done.
func TestGrokRetireUnpublishedGenerationSurvivesAnEndedRequest(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	staged := domain.NewID()
	ref := credentials.Ref{Owner: f.input.AccountID, ID: staged, Purpose: credentials.AccountLogin}
	if _, err := f.secrets.Put(context.Background(), ref, []byte("synthetic-staged")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(f.workerContext(context.Background()))
	cancel()
	f.service.retireUnpublishedGrokGeneration(ctx, f.secrets, f.input.AccountID, domain.NewID(), staged)
	if _, err := f.secrets.Get(context.Background(), ref); err == nil {
		t.Fatal("an ended request kept an unreferenced staged generation")
	}
}
