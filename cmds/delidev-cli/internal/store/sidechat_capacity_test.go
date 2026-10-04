// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSidechatCascadeCountsOnlyMissingDependentJournals(t *testing.T) {
	s, _ := openTest(t)
	actor := domain.Principal{Type: domain.OwnerDevice}
	ctx := domain.WithPrincipal(context.Background(), actor)
	server := domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	parent, child, _, _, _ := sidechatDeletionFixture(t, s, ctx)
	existing, _, err := s.DeleteSession(ctx, domain.NewID(), child.ID, server, child.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxSessionDeletions-2; i++ {
		v := SessionDeletion{Version: 1, ID: domain.NewID(), SessionID: domain.NewID(), ServerID: server, RequestID: domain.NewID(), Actor: actor, ExpectedRevision: 1, Revision: 1, AcceptedAt: time.Now().UTC(), Workers: []SessionDeletionWorker{}}
		if err := s.writeSessionDeletion(v); err != nil {
			t.Fatal(err)
		}
	}
	plan, _, err := s.DeleteSession(ctx, domain.NewID(), parent.ID, server, parent.Revision)
	if err != nil || len(plan.Dependents) != 1 || plan.Dependents[0].ID != existing.ID || plan.Dependents[0].RequestID != existing.RequestID {
		t.Fatal("existing child journal was charged twice", err)
	}
	all, err := s.sessionDeletionInventory(ctx)
	if err != nil || len(all) != maxSessionDeletions {
		t.Fatal("cascade changed bounded journal inventory", err)
	}
}
