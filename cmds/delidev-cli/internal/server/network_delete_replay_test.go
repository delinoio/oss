// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"os"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestNetworkCompletedDeleteReplayDoesNotRecreateCleanup(t *testing.T) {
	for _, unavailable := range []string{"vault", "intent-publication"} {
		t.Run(unavailable, func(t *testing.T) {
			f := newIntegrationFixture(t)
			vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
			f.service.accountSecrets = vault
			id := domain.NewID()
			created, err := f.service.SaveNetworkProfile(networkIntentContext(), networkIntentRequest(id))
			if err != nil {
				t.Fatal(err)
			}
			mutation := &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: created.Msg.Resource.Revision}
			client := delidevv1connect.NewNetworkServiceClient(f.httpServer.Client(), f.url)
			ctx := context.Background()
			if _, err := client.DeleteNetworkProfile(ctx, ownerRequest(f.service.Identity, &pb.DeleteNetworkProfileRequest{Mutation: mutation})); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(f.service.networkDeleteIntentPath()); !os.IsNotExist(err) {
				t.Fatal("completed deletion retained cleanup intent", err)
			}
			puts, deletes, remaining := vault.counts()
			if remaining != 0 {
				t.Fatal("completed deletion retained a credential")
			}
			f.restart()
			f.service.accountSecrets = vault
			client = delidevv1connect.NewNetworkServiceClient(f.httpServer.Client(), f.url)
			if unavailable == "vault" {
				vault.referenceError = domain.Fail(domain.Unavailable, "Fixture vault unavailable.", "")
			} else {
				root := f.service.Store.Root()
				t.Cleanup(func() { _ = os.Chmod(root, 0700) })
				if err := os.Chmod(root, 0500); err != nil {
					t.Fatal(err)
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				replayed, err := client.DeleteNetworkProfile(ctx, ownerRequest(f.service.Identity, &pb.DeleteNetworkProfileRequest{Mutation: mutation}))
				if err != nil {
					t.Fatal("completed deletion repeated protected work", err)
				}
				if !replayed.Msg.Replayed || !replayed.Msg.Deleted || replayed.Msg.Resource != nil || replayed.Msg.RequestId != mutation.RequestId {
					t.Fatal("completed deletion lost its original receipt", replayed.Msg)
				}
				if _, err := os.Stat(f.service.networkDeleteIntentPath()); !os.IsNotExist(err) {
					t.Fatal("receipt replay recreated cleanup intent", err)
				}
			}
			if p, d, n := vault.counts(); p != puts || d != deletes || n != 0 {
				t.Fatal("receipt replay changed native credentials", p, d, n)
			}
			changed := &pb.Mutation{RequestId: mutation.RequestId, Id: mutation.Id, ExpectedRevision: mutation.ExpectedRevision + 1}
			if _, err := client.DeleteNetworkProfile(ctx, ownerRequest(f.service.Identity, &pb.DeleteNetworkProfileRequest{Mutation: changed})); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("changed input reused completed deletion", err)
			}
		})
	}
}
