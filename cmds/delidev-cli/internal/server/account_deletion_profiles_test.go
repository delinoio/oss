// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// Explicit account deletion owns the account's permanent server profiles: it
// removes every auth, quota and credit profile with its native caches before
// acknowledging, and a busy profile keeps the deletion pending.
func TestAccountDeletionRemovesEveryServerProfileOfTheAccount(t *testing.T) {
	f := unreferencedSubscription(t)
	configuration := delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, f.http.URL)
	remove := func(request *pb.DeleteConfigurationRequest) error {
		_, err := configuration.DeleteConfiguration(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
		return err
	}
	request := func() *pb.DeleteConfigurationRequest {
		r, _ := f.record()
		return &pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: &pb.Mutation{Id: string(r.ID), ExpectedRevision: r.Revision, RequestId: string(domain.NewID())}}
	}
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	native := &deletionLogoutNative{started: make(chan struct{}), release: make(chan struct{})}
	f.service.subscriptionOpen = func(context.Context, nativeScope, domain.ID, []byte, *slog.Logger) (serverSubscriptionNative, error) {
		return native, nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.service.runServerSubscription(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
	}()
	awaitServerFixture(t, native.started)
	close(native.release)
	awaitServerFixture(t, done)
	if f.progressFor(op.OperationId).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
		t.Fatal("original logout did not confirm success")
	}

	registry, err := f.service.nativeProfiles()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var roots []string
	var busy *nativeprofile.Lease
	for _, key := range []nativeprofile.Key{
		{Harness: domain.Codex, Purpose: nativeprofile.PurposeAuth, Account: f.input.AccountID},
		{Harness: domain.Codex, Purpose: nativeprofile.PurposeQuota, Account: f.input.AccountID},
		{Harness: domain.ClaudeCode, Purpose: nativeprofile.PurposeCredit, Account: f.input.AccountID},
	} {
		lease, err := registry.Acquire(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(lease.Root, "cache.db"), []byte("native cache"), 0600); err != nil {
			t.Fatal(err)
		}
		roots = append(roots, filepath.Dir(lease.Root))
		if busy == nil {
			busy = lease
			continue
		}
		lease.Close()
	}
	// Another account's profile is never touched.
	other := domain.NewID()
	otherLease, err := registry.Acquire(ctx, nativeprofile.Key{Harness: domain.Codex, Purpose: nativeprofile.PurposeAuth, Account: other})
	if err != nil {
		t.Fatal(err)
	}
	otherLease.Close()

	if err := remove(request()); err == nil {
		t.Fatal("deletion was acknowledged while an account profile was in use")
	}
	if found, _ := registry.Exists(ctx, nativeprofile.DeleteScope{Account: f.input.AccountID}); !found {
		t.Fatal("a busy account kept no profile")
	}
	busy.Close()
	if err := remove(request()); err != nil {
		t.Fatalf("cleaned account could not be deleted: %v", err)
	}
	if found, err := registry.Exists(ctx, nativeprofile.DeleteScope{Account: f.input.AccountID}); err != nil || found {
		t.Fatalf("account profiles survived deletion: %v %v", found, err)
	}
	for _, root := range roots {
		if _, err := os.Lstat(root); !os.IsNotExist(err) {
			t.Fatalf("profile directory %s survived account deletion", filepath.Base(root))
		}
	}
	if found, err := registry.Exists(ctx, nativeprofile.DeleteScope{Account: other}); err != nil || !found {
		t.Fatalf("another account's profile was removed: %v %v", found, err)
	}
}
