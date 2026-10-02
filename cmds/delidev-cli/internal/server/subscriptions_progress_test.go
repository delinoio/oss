// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestSubscriptionLostOwnershipRejectsAndPurgesProgress(t *testing.T) {
	for _, mode := range []string{"worker-loss", "server-restart", "failed-completion"} {
		t.Run(mode, func(t *testing.T) {
			f := newSubscriptionFixture(t)
			op := f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			if err != nil {
				t.Fatal(err)
			}
			publish := &pb.PublishSubscriptionProgressRequest{AccountId: string(f.input.AccountID), LeaseId: lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Url: "https://auth.openai.com/codex/device", UserCode: "TEST-1234"}
			if _, err := f.client.PublishSubscriptionProgress(context.Background(), subscriptionRequest(f.workerToken, publish)); err != nil {
				t.Fatal(err)
			}
			read := &pb.GetSubscriptionProgressRequest{AccountId: string(f.input.AccountID), OperationId: op.OperationId}
			before, err := f.client.GetSubscriptionProgress(context.Background(), subscriptionRequest(f.service.Identity.Token, read))
			if err != nil || before.Msg.Url != publish.Url || before.Msg.UserCode != publish.UserCode {
				t.Fatal("live login presentation was unavailable", err)
			}
			switch mode {
			case "worker-loss":
				err = f.service.retainLostSubscriptionLeases(f.input.MachineID, f.instance, false)
			case "server-restart":
				unlock, e := f.service.lockAccounts(context.Background())
				if e != nil {
					t.Fatal(e)
				}
				f.service.subscriptionEpoch = domain.NewID()
				unlock()
				err = f.service.retainLostSubscriptionLeases("", "", false)
			case "failed-completion":
				err = f.service.markSubscriptionRecovery(f.input.AccountID)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, account := f.record()
			if !account.Subscription.RecoveryRequired || account.Subscription.Lease == nil || account.Subscription.Lease.ID != domain.ID(lease.LeaseId) || account.Subscription.Pending == nil {
				t.Fatal("lost ownership released the original lease or pending operation")
			}
			if _, err := f.client.GetSubscriptionProgress(context.Background(), subscriptionRequest(f.service.Identity.Token, read)); domain.SafeError(rpc.ClientError(err)).Code != domain.RecoveryRequired {
				t.Fatal("lost ownership still exposed login progress", err)
			}
			if _, err := f.client.PublishSubscriptionProgress(context.Background(), subscriptionRequest(f.workerToken, publish)); err == nil {
				t.Fatal("lost Worker republished stale login progress")
			}
			unlock, err := f.service.lockAccounts(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_, retained := f.service.subscriptionProgress[domain.ID(op.OperationId)]
			unlock()
			if retained {
				t.Fatal("lost login presentation remained cached")
			}
		})
	}
}
