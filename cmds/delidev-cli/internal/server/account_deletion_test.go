// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// Use a separate account so the fixture's existing Agent/session references
// remain intact and cannot accidentally weaken the deletion reference guard.
func unreferencedSubscription(t *testing.T) *subscriptionFixture {
	t.Helper()
	f := newSubscriptionFixture(t)
	_, account := f.record()
	id := domain.NewID()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.account.deletion", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.AccountKind, id, 0, "", "", account)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.input.AccountID = id
	clear(f.login())
	return f
}

type deletionLogoutNative struct {
	serverLoginFixture
	started chan struct{}
	release chan struct{}
}

func (n *deletionLogoutNative) LogoutManaged(ctx context.Context) error {
	close(n.started)
	select {
	case <-n.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestChatGPTAccountDeletionRequiresConfirmedOriginalLogout(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "cleanup-required"}[cleanupFails], func(t *testing.T) {
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
			if err := remove(request()); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatalf("connected deletion bypassed credential ownership: %v", err)
			}
			op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
			native := &deletionLogoutNative{started: make(chan struct{}), release: make(chan struct{})}
			if cleanupFails {
				native.closeError = domain.Fail(domain.RecoveryRequired, "Fixture cleanup is unconfirmed.", "Retain its original owner.")
			}
			f.service.subscriptionOpen = func(context.Context, string, domain.ID, []byte, *slog.Logger) (serverSubscriptionNative, error) {
				return native, nil
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				f.service.runServerSubscription(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
			}()
			awaitServerFixture(t, native.started)
			if err := remove(request()); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatalf("pending logout permitted deletion: %v", err)
			}
			close(native.release)
			awaitServerFixture(t, done)
			progress := f.progressFor(op.OperationId)
			if cleanupFails {
				if progress.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED || remove(request()) == nil {
					t.Fatal("unconfirmed cleanup allowed deletion")
				}
				return
			}
			if progress.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
				t.Fatal("original logout did not confirm success")
			}
			deletion := request()
			if err := remove(deletion); err != nil {
				t.Fatal("cleaned account could not be deleted", err)
			}
			if err := remove(deletion); err != nil {
				t.Fatal("exact deletion replay did not retain its receipt", err)
			}
			if refs, err := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID); err != nil || len(refs) != 0 {
				t.Fatal("deleted account retained protected references", err)
			}
		})
	}
}
