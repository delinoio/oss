// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestServerSubscriptionColdVaultSharedOwner(t *testing.T) {
	f := newSubscriptionFixture(t)
	f.service.accountSecrets = nil
	t.Cleanup(func() {
		if err := f.service.closeAccountSecrets(); err != nil {
			t.Error(err)
		}
	})
	ids := []domain.ID{f.input.AccountID}
	for i := 1; i < 16; i++ {
		id := domain.NewID()
		_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.subscription.account", nil, func(tx *store.Tx) (any, error) {
			return tx.Put(domain.AccountKind, id, 0, "", "", domain.Account{Alias: "Concurrent subscription", Enabled: true, Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionChatGPT, Health: domain.AccountDisconnected})
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		var r store.Record
		if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error { var err error; r, err = tx.Get(domain.AccountKind, id); return err }); err != nil {
			t.Fatal(err)
		}
		_, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}))
		if err != nil {
			t.Fatal(err)
		}
	}
	var nativeCalls atomic.Int32
	f.service.subscriptionOpen = func(context.Context, string, domain.ID, []byte, *slog.Logger) (serverSubscriptionNative, error) {
		nativeCalls.Add(1)
		return nil, domain.Fail(domain.Unsupported, "Synthetic native refusal.", "")
	}
	start := make(chan struct{})
	var joined sync.WaitGroup
	for _, id := range ids {
		joined.Add(1)
		go func(id domain.ID) {
			defer joined.Done()
			<-start
			f.service.runServerSubscription(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), id)
		}(id)
	}
	// API protected initialization uses the same gate while subscriptions start.
	var apiOwner accountSecrets
	joined.Add(1)
	go func() {
		defer joined.Done()
		<-start
		unlock, err := f.service.lockAccounts(context.Background())
		if err != nil {
			t.Error(err)
			return
		}
		defer unlock()
		apiOwner, err = f.service.secrets()
		if err != nil {
			t.Error(err)
		}
	}()
	close(start)
	joined.Wait()
	if nativeCalls.Load() != 16 {
		t.Fatalf("native calls = %d", nativeCalls.Load())
	}
	unlock, err := f.service.lockAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if f.service.ownedVault == nil || f.service.accountSecrets != f.service.ownedVault || apiOwner != f.service.ownedVault {
		t.Fatal("protected operations did not retain one vault owner")
	}
}

type gateReadSecrets struct {
	*accountTestSecrets
	service  *Service
	expected credentials.Ref
	calls    int
}

func (v *gateReadSecrets) Get(ctx context.Context, ref credentials.Ref) ([]byte, error) {
	if len(v.service.accountGate) != 1 {
		return nil, domain.Fail(domain.Conflict, "Original read lacks the account gate.", "")
	}
	if ref != v.expected {
		return nil, domain.Fail(domain.Conflict, "Wrong original reference.", "")
	}
	v.calls++
	return v.accountTestSecrets.Get(ctx, ref)
}
func TestServerSubscriptionOriginalVaultReadGate(t *testing.T) {
	f := newSubscriptionFixture(t)
	ref := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	f.secrets.values[ref] = []byte("synthetic-original")
	vault := &gateReadSecrets{accountTestSecrets: f.secrets, service: f.service, expected: ref}
	f.service.accountSecrets = vault
	owner, old, err := f.service.serverSubscriptionCredentials(context.Background(), ref.Owner, ref.ID)
	defer clear(old)
	if err != nil || owner != vault || string(old) != "synthetic-original" || vault.calls != 1 {
		t.Fatalf("original read: owner=%t calls=%d error=%v", owner == vault, vault.calls, err)
	}
	if len(f.service.accountGate) != 0 {
		t.Fatal("native work would retain the protected gate")
	}
}
func TestServerSubscriptionVaultCancellationOpenFailureRetry(t *testing.T) {
	f := newSubscriptionFixture(t)
	f.service.accountSecrets = nil
	t.Cleanup(func() {
		if err := f.service.closeAccountSecrets(); err != nil {
			t.Error(err)
		}
	})
	blocked, err := f.service.lockAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = f.service.serverSubscriptionCredentials(ctx, f.input.AccountID, ""); err == nil {
		t.Fatal("canceled access admitted")
	}
	blocked()
	path := filepath.Join(f.service.Store.Root(), "secrets")
	// The fixture owns this temporary metadata scope and its injected vault.
	if err = os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("synthetic obstacle"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.service.serverSubscriptionCredentials(context.Background(), f.input.AccountID, ""); err == nil {
		t.Fatal("vault-open failure ignored")
	}
	if f.service.accountSecrets != nil || f.service.ownedVault != nil {
		t.Fatal("failed initialization published an owner")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	first, _, err := f.service.serverSubscriptionCredentials(context.Background(), f.input.AccountID, "")
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := f.service.serverSubscriptionCredentials(context.Background(), f.input.AccountID, "")
	if err != nil || first != again || first != f.service.ownedVault {
		t.Fatal("retry replaced the original vault")
	}
}
func TestServerSubscriptionNativeWaitReleasesVaultGate(t *testing.T) {
	f := newSubscriptionFixture(t)
	f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	n := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("fixture-account", "first", time.Now().UTC())}
	done := f.serverRun(n)
	awaitServerFixture(t, n.started)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, err := f.service.lockAccounts(ctx)
	if err != nil {
		close(n.finish)
		awaitServerFixture(t, done)
		t.Fatal("native wait held the protected gate", err)
	}
	unlock()
	close(n.finish)
	awaitServerFixture(t, done)
}
