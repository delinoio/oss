package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (f *integrationFixture) repository(profile domain.ID) *pb.Resource {
	f.t.Helper()
	id := domain.NewID()
	var record store.Record
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	// This fixture supplies stored configuration only; it makes no Worker or
	// native checkout preparation claim and never touches the placeholder path.
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "test.repository", id, func(tx *store.Tx) (any, error) {
		var e error
		record, e = tx.Put(domain.RepositoryKind, id, 0, "", "", domain.Repository{RemoteURL: "https://github.com/fixture/repo.git", Name: "Fixture repository", Checkouts: []domain.Checkout{{MachineID: domain.NewID(), Path: "/fixture/unused"}}, GitHubOwner: "fixture-owner", GitHubName: "repo", IntegrationID: profile})
		return record, e
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return rpc.Resource(record)
}
