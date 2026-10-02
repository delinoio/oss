// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestSubscriptionConfirmedPreNativeFailurePreservesGeneration(t *testing.T) {
	for _, pending := range []pb.SubscriptionAction{pb.SubscriptionAction_SUBSCRIPTION_ACTION_UNSPECIFIED, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT} {
		t.Run(pending.String(), func(t *testing.T) {
			f := newSubscriptionFixture(t)
			clear(f.login())
			lease := takeSubscriptionExecutionFixture(t, f)
			defer clear(lease.Bundle)
			var waiting *pb.RequestSubscriptionResponse
			if pending != pb.SubscriptionAction_SUBSCRIPTION_ACTION_UNSPECIFIED {
				waiting = f.start(pending)
			}
			_, before := f.record()
			if _, err := f.finish(lease, lease.Bundle, false, false, true); err != nil {
				t.Fatal("confirmed unchanged pre-native cleanup was refused", err)
			}
			stored, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: domain.ID(lease.GenerationId), Purpose: credentials.AccountLogin})
			defer clear(stored)
			if err != nil || !bytes.Equal(stored, lease.Bundle) {
				t.Fatal("confirmed pre-native failure removed its original vault generation", err)
			}
			_, after := f.record()
			if after.Subscription.Generation != before.Subscription.Generation || after.Subscription.Lease != nil || after.Subscription.RecoveryRequired || after.Connection == nil || after.Connection.ID != before.Connection.ID || after.Health != before.Health {
				t.Fatal("confirmed pre-native failure changed account authority or retained its lease")
			}
			if waiting == nil {
				waiting = f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
				pending = pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH
			} else if after.Subscription.Pending == nil || string(after.Subscription.Pending.ID) != waiting.OperationId {
				t.Fatal("pre-native release erased the independently queued operation")
			}
			next, err := f.take(waiting, pending)
			if err != nil {
				t.Fatal("next authorized operation could not acquire the retained generation", err)
			}
			defer clear(next.Bundle)
			if next.GenerationId != lease.GenerationId || !bytes.Equal(next.Bundle, lease.Bundle) {
				t.Fatal("pre-native release replaced the original generation")
			}
		})
	}
}

func TestSubscriptionPreNativeReleaseRequiresUnchangedBundleAndCleanup(t *testing.T) {
	for _, mode := range []string{"missing-bundle", "changed-bundle", "missing-cleanup"} {
		t.Run(mode, func(t *testing.T) {
			f := newSubscriptionFixture(t)
			clear(f.login())
			lease := takeSubscriptionExecutionFixture(t, f)
			defer clear(lease.Bundle)
			bundle, cleanup := lease.Bundle, true
			if mode == "missing-bundle" {
				bundle = nil
			} else if mode == "changed-bundle" {
				bundle = subscriptionTestBundle("fixture-account", "changed", time.Now().UTC())
				defer clear(bundle)
			} else {
				cleanup = false
			}
			_, finishErr := f.finish(lease, bundle, false, false, cleanup)
			if mode == "changed-bundle" && finishErr == nil {
				t.Fatal("changed authentication was accepted as unused original material")
			}
			_, account := f.record()
			if !account.Subscription.RecoveryRequired || account.Subscription.Lease == nil || account.Subscription.Lease.ID != domain.ID(lease.LeaseId) || string(account.Subscription.Generation) != lease.GenerationId {
				t.Fatal("missing pre-native evidence released recovery ownership")
			}
			stored, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: domain.ID(lease.GenerationId), Purpose: credentials.AccountLogin})
			defer clear(stored)
			if err != nil || !bytes.Equal(stored, lease.Bundle) {
				t.Fatal("uncertain failed execution destroyed its blocked recovery generation", err)
			}
		})
	}
}
