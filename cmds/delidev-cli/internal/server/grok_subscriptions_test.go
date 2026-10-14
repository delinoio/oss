// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		machine.Installations = []domain.Installation{{Harness: domain.GrokBuild, State: domain.InstallationDetected, Version: domain.GrokSubscriptionVersion, ResolvedPath: "/controlled/native/grok", ProtocolVerified: true, ObservedAt: &observed, Protocol: &domain.ProtocolObservation{Protocol: domain.ProtocolFor(domain.GrokBuild), State: domain.ProtocolVerified}}}
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
	// Execution is a separate contract and has no Grok lease yet.
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

func TestGrokTakeRequiresPinnedSubscriptionVersion(t *testing.T) {
	for _, version := range []string{domain.GrokProtocolVersion, "1.0.45", "1.0.47", "1.0.46-rc.1", "2.0.0"} {
		t.Run(version, func(t *testing.T) {
			f := newGrokFixture(t)
			f.setGrokInstallation(t, func(installations *[]domain.Installation) { (*installations)[0].Version = version })
			if _, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN), pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.Unsupported {
				t.Fatal("Take admitted a Grok installation that is not the pinned release")
			}
			if _, a := f.record(); a.Subscription.Lease != nil || a.Subscription.RecoveryRequired {
				t.Fatal("a refused Take claimed the lease or fenced the account")
			}
		})
	}
	f := newGrokFixture(t)
	f.setGrokInstallation(t, func(installations *[]domain.Installation) {
		(*installations)[0].Version = domain.GrokSubscriptionVersion + "+build.1"
	})
	f.login("synthetic-principal-1")
}

func (f *grokFixture) start2(action pb.SubscriptionAction) (*pb.RequestSubscriptionResponse, error) {
	r, _ := f.record()
	response, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: action}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}
