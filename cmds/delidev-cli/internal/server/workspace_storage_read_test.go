// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"net/http"
	"testing"
)

func TestWorkspaceReadsRejectUnavailableStorageBeforePublication(t *testing.T) {
	for _, state := range []domain.WorkspaceStorageState{domain.WorkspaceStoragePending, domain.WorkspaceStorageUncertain, domain.WorkspaceStored} {
		t.Run(string(state), func(t *testing.T) {
			f := newStorageFixture(t)
			_, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "fixture.storage-read-state", nil, func(tx *store.Tx) (any, error) {
				r, session, err := sessionRecord(tx, f.session)
				if err != nil {
					return nil, err
				}
				session.Storage = &domain.WorkspaceStorage{State: state, JobID: domain.NewID()}
				return tx.Put(domain.SessionKind, r.ID, r.Revision, r.SessionID, r.ProjectID, session)
			})
			if err != nil {
				t.Fatal(err)
			}
			client := delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.server.URL)
			for _, operation := range []domain.WorkspaceReadOperation{domain.WorkspaceRoots, domain.WorkspaceFile, domain.WorkspaceDirectory} {
				query, _ := json.Marshal(domain.WorkspaceReadQuery{Operation: operation, Path: "note"})
				if operation == domain.WorkspaceRoots {
					query, _ = json.Marshal(domain.WorkspaceReadQuery{Operation: operation})
				}
				_, err := client.ReadSessionWorkspace(context.Background(), ownerRequest(f.service.Identity, &pb.ReadSessionWorkspaceRequest{SessionId: string(f.session), QueryJson: query}))
				if connect.CodeOf(err) != connect.CodeUnavailable {
					t.Fatal(operation, "read published without present storage", err)
				}
			}
		})
	}
}
