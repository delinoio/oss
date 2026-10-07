// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestModelDeletionRetainsForkSourceReferences(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	target, snapshotModel, sessionID := domain.NewID(), domain.NewID(), domain.NewID()
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.model-fork-reference", nil, func(tx *store.Tx) (any, error) {
		for _, id := range []domain.ID{target, snapshotModel} {
			if _, err := tx.Put(domain.ModelKind, id, 0, "", "", domain.Model{Name: "Retained model", NativeID: string(id), Harnesses: []domain.Harness{domain.Codex}}); err != nil {
				return nil, err
			}
		}
		session := domain.Session{
			Name: "Fork retaining a fallback model",
			Fork: &domain.ForkOrigin{Snapshot: domain.InitialExecution{
				Configuration: domain.ExecutionConfiguration{ModelID: snapshotModel},
				Route:         domain.Route{Sources: []domain.SourceSelection{{ModelID: target}}},
			}},
		}
		return tx.Put(domain.SessionKind, sessionID, 0, "", "", session)
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Mutate(ctx, domain.NewID(), "fixture.delete-fork-model", nil, func(tx *store.Tx) (any, error) {
		if err := validateDeletion(tx, domain.ModelKind, target); err != nil {
			return nil, err
		}
		return nil, tx.Delete(domain.ModelKind, target, 1)
	})
	if err == nil || domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("fork source model reference was not retained: %v", err)
	}
}
