// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"path/filepath"
	"testing"
)

func TestPersistentModelDeletionIsUnsupported(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.retired-model-delete", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.Delete(domain.ModelKind, domain.NewID(), 1)
	})
	if domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("retired registry deletion was admitted", err)
	}
}
