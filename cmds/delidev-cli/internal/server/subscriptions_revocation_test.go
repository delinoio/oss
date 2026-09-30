// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestSubscriptionRevokedQueuedLogoutSurvivesExecutionWriteBack(t *testing.T) {
	f := newSubscriptionFixture(t)
	raw := f.login()
	defer clear(raw)
	_, account := f.record()
	f.input.ConnectionID = account.Connection.ID
	f.input.Configuration.Subscription = true
	var err error
	f.input.ConfigurationDigest, err = f.input.Configuration.Digest()
	if err != nil {
		t.Fatal(err)
	}
	var jobRevision uint64
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.subscription-execution", nil, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		session.InitialExecution.ConnectionID = f.input.ConnectionID
		session.InitialExecution.Configuration = f.input.Configuration
		session.InitialExecution.ConfigurationDigest = f.input.ConfigurationDigest
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.SessionID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		jr, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](jr)
		if err != nil {
			return nil, err
		}
		job.Input, _ = json.Marshal(f.input)
		updated, err := tx.PutJob(jr.ID, jr.Revision, jr.SessionID, jr.ProjectID, job)
		jobRevision = updated.Revision
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	take := &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: jobRevision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: string(f.job), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}
	response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, take))
	if err != nil {
		t.Fatal(err)
	}
	clear(response.Msg.Bundle)
	devices := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, f.http.URL)
	ctx := context.Background()
	code, token := randomCode(), randomCode()
	codeHash, tokenHash := sha256.Sum256([]byte(code)), sha256.Sum256([]byte(token))
	pairing, err := devices.CreatePairing(ctx, subscriptionRequest(f.service.Identity.Token, &pb.CreatePairingRequest{RequestId: string(domain.NewID()), Name: "logout client", Type: pb.DeviceType_DEVICE_TYPE_CLIENT, CodeDigest: codeHash[:]}))
	if err != nil {
		t.Fatal(err)
	}
	paired, err := devices.PairDevice(ctx, connect.NewRequest(&pb.PairDeviceRequest{RequestId: string(domain.NewID()), PairingId: pairing.Msg.Pairing.Id, Code: code, DeviceId: string(domain.NewID()), CredentialDigest: tokenHash[:]}))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := f.record()
	_, err = f.client.RequestSubscription(ctx, subscriptionRequest(token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = devices.RevokeDevice(ctx, subscriptionRequest(f.service.Identity.Token, &pb.RevokeDeviceRequest{Mutation: acctMutation(paired.Msg.Device, domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	_, revoked := f.record()
	if revoked.Health != domain.AccountRevoked || revoked.Subscription.Pending != nil || revoked.Subscription.Lease == nil || string(revoked.Subscription.Lease.ID) != response.Msg.LeaseId {
		t.Fatal("queued logout cancellation changed its revocation or independent execution lease")
	}
	// Native completion can win before its targeted cancellation is observed.
	// Latest-bundle write-back still cannot undo the accepted logout revocation.
	if _, err := f.finish(response.Msg, raw, true, false, true); err != nil {
		t.Fatal(err)
	}
	_, written := f.record()
	if written.Health != domain.AccountRevoked || written.Subscription.Lease != nil || written.Subscription.Pending != nil || written.Subscription.Generation == "" || written.Subscription.RecoveryRequired {
		t.Fatal("execution write-back resurrected logout-revoked authentication")
	}
	logout := f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	lease, err := f.take(logout, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	if err != nil {
		t.Fatal(err)
	}
	clear(lease.Bundle)
	if _, err := f.finish(lease, nil, true, false, true); err != nil {
		t.Fatal(err)
	}
	_, cleaned := f.record()
	if cleaned.Connection != nil || cleaned.Health != domain.AccountDisconnected || cleaned.Subscription.Generation != "" {
		t.Fatal("fresh authorized logout could not clean the preserved generation")
	}
}
