// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestNetworkSaveReceiptSurvivesLaterProfileDeletion(t *testing.T) {
	f := newIntegrationFixture(t)
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	f.service.accountSecrets = vault
	client := delidevv1connect.NewNetworkServiceClient(f.httpServer.Client(), f.url)
	ctx := context.Background()
	create := &pb.Mutation{RequestId: string(domain.NewID())}
	definition := `{"name":"Receipt fixture","mode":"http","host":"127.0.0.1","port":3128}`
	credential := `{"username":"receipt-fixture-user","password":"receipt-fixture-password"}`
	save := func(mutation *pb.Mutation, document, secret string) (*connect.Response[pb.SaveNetworkProfileResponse], error) {
		return client.SaveNetworkProfile(ctx, ownerRequest(f.service.Identity, &pb.SaveNetworkProfileRequest{
			Mutation: mutation, SchemaVersion: 1, DocumentJson: []byte(document), CredentialJson: []byte(secret),
		}))
	}
	created, err := save(create, definition, credential)
	if err != nil {
		t.Fatal(err)
	}
	edit := &pb.Mutation{RequestId: string(domain.NewID()), Id: created.Msg.Resource.Id, ExpectedRevision: created.Msg.Resource.Revision}
	edited, err := save(edit, definition, credential)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.DeleteNetworkProfile(ctx, ownerRequest(f.service.Identity, &pb.DeleteNetworkProfileRequest{
		Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: edited.Msg.Resource.Id, ExpectedRevision: edited.Msg.Resource.Revision},
	}))
	if err != nil {
		t.Fatal(err)
	}
	puts, deletes, _ := vault.counts()
	for _, original := range []*pb.Mutation{create, edit} {
		replayed, err := save(original, definition, credential)
		if err != nil {
			t.Fatal("accepted save became an error after deletion", err)
		}
		if !replayed.Msg.Replayed || !replayed.Msg.Deleted || replayed.Msg.Resource != nil || replayed.Msg.RequestId != original.RequestId {
			t.Fatal("lost original receipt or resurrected deleted profile", replayed.Msg)
		}
	}
	if _, err := save(create, definition, `{"username":"altered-user","password":"altered-password"}`); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("changed credential reused the deleted save receipt", err)
	}
	if currentPuts, currentDeletes, remaining := vault.counts(); currentPuts != puts || currentDeletes != deletes || remaining != 0 {
		t.Fatal("receipt replay performed protected work", currentPuts, currentDeletes, remaining)
	}
	if _, err := f.service.Store.Get(ctx, domain.NetworkProfileKind, domain.ID(created.Msg.Resource.Id)); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("receipt replay recreated the profile", err)
	}
}
