// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type networkCancelEnumeration struct {
	*accountTestSecrets
	cancel context.CancelFunc
}

func (v *networkCancelEnumeration) UnremovedReferences(ctx context.Context, owner domain.ID) ([]credentials.Ref, error) {
	refs, err := v.accountTestSecrets.UnremovedReferences(ctx, owner)
	v.cancel()
	return refs, err
}

func TestNetworkDeleteIntentSurvivesFailureRevocationAndRestart(t *testing.T) {
	for _, failure := range []string{"enumeration", "deletion", "cancellation"} {
		t.Run(failure, func(t *testing.T) {
			f := newIntegrationFixture(t)
			vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
			f.service.accountSecrets = vault
			id := domain.NewID()
			created, err := f.service.SaveNetworkProfile(networkIntentContext(), networkIntentRequest(id))
			if err != nil {
				t.Fatal(err)
			}
			clientIdentity := localOriginClient(t, &accountFixture{identity: f.service.Identity, endpoint: Endpoint{URL: f.url}})
			digest := sha256.Sum256([]byte(clientIdentity.Token))
			actor, err := f.service.Store.Authenticate(context.Background(), digest[:])
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(domain.WithPrincipal(context.Background(), actor))
			defer cancel()
			unavailable := domain.Fail(domain.Unavailable, "Fixture cleanup unavailable.", "")
			switch failure {
			case "enumeration":
				vault.referenceError = unavailable
			case "deletion":
				vault.deleteError = unavailable
			case "cancellation":
				f.service.accountSecrets = &networkCancelEnumeration{accountTestSecrets: vault, cancel: cancel}
			}
			original := &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: created.Msg.Resource.Revision}
			if _, err := f.service.DeleteNetworkProfile(ctx, connect.NewRequest(&pb.DeleteNetworkProfileRequest{Mutation: original})); err == nil {
				t.Fatal("failed cleanup reported success")
			}
			if _, err := f.service.Store.Get(networkIntentContext(), domain.NetworkProfileKind, id); domain.SafeError(err).Code != domain.NotFound {
				t.Fatal("public deletion was not committed", err)
			}
			raw, err := os.ReadFile(f.service.networkDeleteIntentPath())
			if err != nil || bytes.Contains(raw, []byte(outboundtest.Username)) || bytes.Contains(raw, []byte(outboundtest.Password)) {
				t.Fatal("missing or secret-bearing cleanup obligation", err)
			}
			puts, deletes, remaining := vault.counts()
			if remaining != 1 {
				t.Fatal("fault did not retain native credential")
			}
			devices := delidevv1connect.NewDeviceServiceClient(f.httpServer.Client(), f.url)
			if _, err := devices.RevokeDevice(context.Background(), ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(actor.DeviceID), ExpectedRevision: 1}})); err != nil {
				t.Fatal(err)
			}
			f.restart()
			vault.deleteError, vault.referenceError = nil, nil
			f.service.accountSecrets = vault
			client := delidevv1connect.NewNetworkServiceClient(f.httpServer.Client(), f.url)
			if _, err := client.DeleteNetworkProfile(context.Background(), ownerRequest(clientIdentity, &pb.DeleteNetworkProfileRequest{Mutation: original})); connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatal("revoked client recovered protected work", err)
			}
			if _, err := client.DeleteNetworkProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteNetworkProfileRequest{Mutation: original})); err != nil {
				t.Fatal("owner reused another actor's receipt", err)
			}
			if p, d, _ := vault.counts(); p != puts || d != deletes {
				t.Fatal("unauthorized or changed retry performed native work")
			}
			fresh := &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: original.ExpectedRevision}
			recovered, err := client.DeleteNetworkProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteNetworkProfileRequest{Mutation: fresh}))
			if err != nil || !recovered.Msg.Deleted || recovered.Msg.Replayed || recovered.Msg.Resource != nil || recovered.Msg.RequestId != fresh.RequestId {
				t.Fatal("fresh owner could not recover the original deletion", err)
			}
			if _, _, n := vault.counts(); n != 0 {
				t.Fatal("cleanup left a native credential")
			}
			if _, err := os.Stat(f.service.networkDeleteIntentPath()); !os.IsNotExist(err) {
				t.Fatal("confirmed cleanup retained its obligation", err)
			}
			replayed, err := client.DeleteNetworkProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteNetworkProfileRequest{Mutation: fresh}))
			if err != nil || !replayed.Msg.Replayed || !replayed.Msg.Deleted {
				t.Fatal("recovered deletion lost its new actor-bound receipt", err)
			}
		})
	}
}

func TestNetworkDeleteIntentUnacceptedSQLPreservesCredentials(t *testing.T) {
	f := newIntegrationFixture(t)
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	f.service.accountSecrets = vault
	id := domain.NewID()
	if _, err := f.service.SaveNetworkProfile(networkIntentContext(), networkIntentRequest(id)); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(f.root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER fixture_network_delete_fail BEFORE DELETE ON entities WHEN OLD.kind='network_profile' BEGIN SELECT RAISE(ABORT,'fixture deletion failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.DeleteNetworkProfile(networkIntentContext(), connect.NewRequest(&pb.DeleteNetworkProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: 1}})); err == nil {
		t.Fatal("SQL failure reported deletion")
	}
	if _, exists, err := f.service.readNetworkDeleteIntent(); err != nil || !exists {
		t.Fatal("SQL failure lost prior durable intent", err)
	}
	if _, err := db.Exec(`DROP TRIGGER fixture_network_delete_fail`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.SaveNetworkProfile(networkIntentContext(), networkIntentRequest(domain.NewID())); err != nil {
		t.Fatal("proved absent receipt could not retire metadata", err)
	}
	if _, deletes, remaining := vault.counts(); deletes != 0 || remaining != 2 {
		t.Fatal("unaccepted deletion removed a live credential", deletes, remaining)
	}
	if _, err := f.service.Store.Get(networkIntentContext(), domain.NetworkProfileKind, id); err != nil {
		t.Fatal("unaccepted deletion removed public profile", err)
	}
}

func TestNetworkDeleteIntentFailedCleanupBlocksReplacementAndTampering(t *testing.T) {
	f := newIntegrationFixture(t)
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	f.service.accountSecrets = vault
	id := domain.NewID()
	if _, err := f.service.SaveNetworkProfile(networkIntentContext(), networkIntentRequest(id)); err != nil {
		t.Fatal(err)
	}
	vault.referenceError = domain.Fail(domain.Unavailable, "Fixture enumeration unavailable.", "")
	if _, err := f.service.DeleteNetworkProfile(networkIntentContext(), connect.NewRequest(&pb.DeleteNetworkProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: 1}})); err == nil {
		t.Fatal("failed cleanup reported success")
	}
	for i := 0; i < 3; i++ {
		if _, err := f.service.SaveNetworkProfile(networkIntentContext(), networkIntentRequest(domain.NewID())); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatal("cleanup failure admitted replacement", err)
		}
	}
	if puts, _, remaining := vault.counts(); puts != 1 || remaining != 1 {
		t.Fatal("cleanup failures accumulated native credentials", puts, remaining)
	}
	raw, err := os.ReadFile(f.service.networkDeleteIntentPath())
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(raw, []byte(id), []byte(domain.NewID()), 1)
	if err := os.WriteFile(f.service.networkDeleteIntentPath(), tampered, 0600); err != nil {
		t.Fatal(err)
	}
	vault.referenceError = nil
	if _, err := f.service.SaveNetworkProfile(networkIntentContext(), networkIntentRequest(domain.NewID())); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("tampered obligation authorized native work", err)
	}
	if puts, deletes, remaining := vault.counts(); puts != 1 || deletes != 0 || remaining != 1 {
		t.Fatal("tampered obligation changed vault", puts, deletes, remaining)
	}
}

func TestNetworkDeleteIntentMustPersistBeforePublicRemoval(t *testing.T) {
	f := newIntegrationFixture(t)
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	f.service.accountSecrets = vault
	id := domain.NewID()
	if _, err := f.service.SaveNetworkProfile(networkIntentContext(), networkIntentRequest(id)); err != nil {
		t.Fatal(err)
	}
	// A directory at the journal's exact path prevents atomic publication.
	if err := os.Mkdir(f.service.networkDeleteIntentPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.DeleteNetworkProfile(networkIntentContext(), connect.NewRequest(&pb.DeleteNetworkProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: 1}})); err == nil {
		t.Fatal("unpersisted obligation permitted deletion")
	}
	if _, err := f.service.Store.Get(networkIntentContext(), domain.NetworkProfileKind, id); err != nil {
		t.Fatal("failed obligation removed public profile", err)
	}
	if _, deletes, remaining := vault.counts(); deletes != 0 || remaining != 1 {
		t.Fatal("failed obligation changed native credentials", deletes, remaining)
	}
}
