// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"crypto/sha256"
	"encoding/json"
	"filippo.io/age"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workernetwork"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestEncryptedWorkerNetworkGenerationControlsDispatchAndOriginalScope(t *testing.T) {
	f := newAuthorityFixture(t, "http://127.0.0.1:46311")
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	f.service.accountSecrets = vault
	pairing := domain.NewID()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.network.original.pairing", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.PairingKind, pairing, 0, "", "", domain.Pairing{Name: "fixture", Type: domain.WorkerDevice, UsedBy: f.device, ExpiresAt: time.Now().Add(-time.Hour)})
	})
	if err != nil {
		t.Fatal(err)
	}
	authority := workernetwork.Authority{ServerID: f.service.Identity.ServerID, Endpoint: f.http.URL, MachineID: f.input.MachineID, DeviceID: f.device, PairingID: pairing}
	root := filepath.Join(t.TempDir(), "private-worker")
	recipient, err := workernetwork.Prepare(context.Background(), root, vault, authority)
	if err != nil {
		t.Fatal(err)
	}
	owner := f.service.Identity.Token
	network := delidevv1connect.NewNetworkServiceClient(http.DefaultClient, f.http.URL)
	request := &pb.ExportWorkerNetworkBundleRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, MachineId: string(authority.MachineID), DeviceId: string(authority.DeviceID), PairingId: string(pairing), Endpoint: authority.Endpoint, Recipient: recipient.PublicKey, KeyId: string(recipient.KeyID)}
	export, err := network.ExportWorkerNetworkBundle(context.Background(), subscriptionRequest(owner, request))
	if err != nil {
		t.Fatal(err)
	}
	if export.Msg.Route == nil || export.Msg.Route.Revision != 1 || export.Msg.CiphertextDigest != workernetwork.Digest(export.Msg.Ciphertext) {
		t.Fatal("export lost exact scope/generation")
	}
	cache, err := workernetwork.Import(context.Background(), root, vault, authority, export.Msg.Ciphertext, export.Msg.CiphertextDigest)
	if err != nil {
		t.Fatal(err)
	}
	worker := delidevv1connect.NewWorkerServiceClient(http.DefaultClient, f.http.URL)
	_, err = worker.AttachWorker(context.Background(), subscriptionRequest(f.workerToken, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: string(authority.MachineID), InstanceId: string(f.instance), Version: rpc.Version, Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_NETWORK_BOOTSTRAP_V1}, NetworkKeyId: string(recipient.KeyID), NetworkRecipient: recipient.PublicKey, NetworkGeneration: cache.Generation, NetworkRouteId: string(cache.RouteID)}))
	if err != nil {
		t.Fatal(err)
	}
	status, err := network.GetWorkerNetworkStatus(context.Background(), subscriptionRequest(owner, &pb.GetWorkerNetworkStatusRequest{MachineId: string(authority.MachineID)}))
	if err != nil {
		t.Fatal(err)
	}
	var observed domain.WorkerNetworkStatus
	if domain.Decode(status.Msg.StatusJson, &observed) != nil || observed.ControlState != domain.WorkerRouteStale {
		t.Fatal("attachment alone granted effective generation")
	}
	sync := &pb.SyncWorkerNetworkRequest{RequestId: string(domain.NewID()), MachineId: string(authority.MachineID), InstanceId: string(f.instance), Recipient: recipient.PublicKey, KeyId: string(recipient.KeyID), EffectiveGeneration: cache.Generation, RouteId: string(cache.RouteID), CiphertextDigest: cache.CiphertextDigest}
	ack, err := worker.SyncWorkerNetwork(context.Background(), subscriptionRequest(f.workerToken, sync))
	if err != nil || !ack.Msg.InSync {
		t.Fatal("original cache could not reconcile", err)
	}
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error { return workerNetworkReady(tx, authority.MachineID) }); err != nil {
		t.Fatal(err)
	}
	// A fresh desired generation never changes original active work. It closes
	// admission for another job until the exact protected derivative is applied.
	selectResponse, err := network.SelectNetworkProfile(context.Background(), subscriptionRequest(owner, &pb.SelectNetworkProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(cache.RouteID), ExpectedRevision: cache.Generation}, MachineId: string(authority.MachineID)}))
	if err != nil {
		t.Fatal(err)
	}
	if selectResponse.Msg.Resource.Revision != 2 {
		t.Fatal("selection lost monotonic generation")
	}
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error { return workerNetworkReady(tx, authority.MachineID) }); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("stale cache granted dispatch", err)
	}
	if _, err := network.ExportWorkerNetworkBundle(context.Background(), subscriptionRequest(owner, request)); err == nil {
		t.Fatal("accepted export replay adopted a later route")
	}
	sync.RequestId = string(domain.NewID())
	updated, err := worker.SyncWorkerNetwork(context.Background(), subscriptionRequest(f.workerToken, sync))
	if err != nil || updated.Msg.InSync || updated.Msg.Route.Revision != 2 {
		t.Fatal("stale generation did not retain control-only synchronization", err)
	}
	cache, err = workernetwork.Import(context.Background(), root, vault, authority, updated.Msg.Ciphertext, updated.Msg.CiphertextDigest)
	if err != nil {
		t.Fatal(err)
	}
	sync.RequestId = string(domain.NewID())
	sync.EffectiveGeneration = cache.Generation
	sync.CiphertextDigest = cache.CiphertextDigest
	ack, err = worker.SyncWorkerNetwork(context.Background(), subscriptionRequest(f.workerToken, sync))
	if err != nil || !ack.Msg.InSync {
		t.Fatal(err)
	}
	if _, err := network.GetWorkerNetworkStatus(context.Background(), subscriptionRequest(f.workerToken, &pb.GetWorkerNetworkStatusRequest{MachineId: string(authority.MachineID)})); err == nil {
		t.Fatal("Worker borrowed owner status authority")
	}
	for _, body := range [][]byte{export.Msg.Route.DocumentJson, selectResponse.Msg.Resource.DocumentJson} {
		var route domain.NetworkRoute
		if json.Unmarshal(body, &route) != nil || route.Binding == nil || route.Binding.KeyID != recipient.KeyID {
			t.Fatal("selection lost original recipient pin")
		}
	}
}

func TestWorkerNativeRouteKeepsOriginalGenerationAndDoesNotGrantAnotherLaunch(t *testing.T) {
	f := newAuthorityFixture(t, "http://127.0.0.1:46311")
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	key, routeID, profileID := domain.NewID(), domain.NewID(), domain.NewID()
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.native-route", nil, func(tx *store.Tx) (any, error) {
		profile := domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Fixture", Mode: domain.ProxyHTTP, Host: "127.0.0.1", Port: 46319}}
		if _, err := tx.Put(domain.NetworkProfileKind, profileID, 0, "", "", profile); err != nil {
			return nil, err
		}
		_, err := tx.Put(domain.NetworkRouteKind, routeID, 0, "", "", domain.NetworkRoute{MachineID: f.input.MachineID, ProfileID: profileID, ProfileRevision: 1, Profile: profile, Binding: &domain.WorkerNetworkBinding{DeviceID: f.device, PairingID: domain.NewID(), KeyID: key, Recipient: identity.Recipient().String(), Endpoint: f.http.URL}})
		if err != nil {
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
		machine.WorkerCapabilities = []domain.WorkerCapability{domain.NetworkBootstrapV1, domain.CodexAPIProxyV1}
		machine.Network = &domain.WorkerNetworkState{InstanceID: f.instance, KeyID: key, Recipient: identity.Recipient().String(), RouteID: routeID, EffectiveGeneration: 1, NativeState: domain.WorkerRouteNotApplied, ObservedAt: time.Now().UTC()}
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.registerGrant(t)
	request := &pb.ReportWorkerNativeRouteRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.job), ExpectedRevision: 1}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), ExecutionId: string(f.input.ExecutionID), RouteId: string(routeID), Generation: 1, State: pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_OBSERVED}
	if _, err := f.client.ReportWorkerNativeRoute(context.Background(), subscriptionRequest(f.workerToken, request)); err == nil {
		t.Fatal("positive report without original pre-launch pin was accepted")
	}
	request.Mutation.RequestId = string(domain.NewID())
	request.State = pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_UNVERIFIED
	accepted, err := f.client.ReportWorkerNativeRoute(context.Background(), subscriptionRequest(f.workerToken, request))
	if err != nil || accepted.Msg.Replayed {
		t.Fatal("original launch binding failed", err)
	}
	accepted, err = f.client.ReportWorkerNativeRoute(context.Background(), subscriptionRequest(f.workerToken, request))
	if err != nil || !accepted.Msg.Replayed {
		t.Fatal("exact binding receipt was not observation-only", err)
	}
	network := delidevv1connect.NewNetworkServiceClient(http.DefaultClient, f.http.URL)
	_, err = network.SelectNetworkProfile(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.SelectNetworkProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(routeID), ExpectedRevision: 1}, MachineId: string(f.input.MachineID), ProfileId: string(profileID), ProfileRevision: 1}))
	if err != nil {
		t.Fatal(err)
	}
	request.Mutation.RequestId = string(domain.NewID())
	request.State = pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_OBSERVED
	if _, err := f.client.ReportWorkerNativeRoute(context.Background(), subscriptionRequest(f.workerToken, request)); err != nil {
		t.Fatal("active native owner lost its original generation", err)
	}
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		status, err := workerNetworkStatus(tx, f.input.MachineID)
		if err != nil {
			return err
		}
		if status.ControlState != domain.WorkerRouteStale || status.DesiredGeneration != 2 || status.EffectiveGeneration != 1 || status.NativeGeneration != 1 || status.NativeExecutionID != f.input.ExecutionID {
			t.Fatal("desired/effective/native generations were collapsed")
		}
		if domain.SafeError(workerNetworkAdmission(tx, f.input.MachineID)).Code != domain.ResourceExhausted {
			t.Fatal("stale transaction granted fresh claim")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	request.Mutation.RequestId = string(domain.NewID())
	request.State = pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_UNVERIFIED
	if _, err := f.client.ReportWorkerNativeRoute(context.Background(), subscriptionRequest(f.workerToken, request)); err == nil {
		t.Fatal("new launch adopted an existing original route pin")
	}
	f.register.Mutation.RequestId = string(domain.NewID())
	digest := sha256.Sum256([]byte("another-scoped-token"))
	f.register.CredentialDigest = digest[:]
	if _, err := f.client.RegisterExecution(context.Background(), subscriptionRequest(f.workerToken, f.register)); err == nil {
		t.Fatal("stale generation granted fresh native credential")
	}
	request.Mutation.RequestId = string(domain.NewID())
	request.State = pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_OBSERVED
	if _, err := f.client.ReportWorkerNativeRoute(context.Background(), connect.NewRequest(request)); err == nil {
		t.Fatal("unpaired actor reported native routing")
	}
}
