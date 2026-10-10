// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"sync"
	"sync/atomic"
	"testing"
)

func namePut(s *Store, kind domain.Kind, id domain.ID, revision uint64, name string) error {
	_, err := s.Mutate(context.Background(), domain.NewID(), "name.fixture", nil, func(tx *Tx) (any, error) {
		return tx.Put(kind, id, revision, "", "", map[string]string{"name": name})
	})
	return err
}
func TestConfigurationNamesNamespacesRevisionDeletion(t *testing.T) {
	s, _ := openTest(t)
	id := domain.NewID()
	if err := namePut(s, domain.ProjectKind, id, 0, "Straße"); err != nil {
		t.Fatal(err)
	}
	if err := namePut(s, domain.RepositoryKind, domain.NewID(), 0, "STRASSE"); err != nil {
		t.Fatal("cross-kind refused", err)
	}
	if err := namePut(s, domain.ProjectKind, id, 1, "Straße"); err != nil {
		t.Fatal("same ID refused", err)
	}
	rejected := domain.NewID()
	request := domain.NewID()
	reject := func() error {
		_, err := s.Mutate(context.Background(), request, "name.rejected", nil, func(tx *Tx) (any, error) {
			return tx.Put(domain.ProjectKind, rejected, 0, "", "", map[string]string{"name": " STRASSE "})
		})
		return err
	}
	for i := 0; i < 2; i++ {
		err := reject()
		if domain.SafeError(err).Cause != domain.ConfigurationNameConflictCause {
			t.Fatal(err)
		}
	}
	if _, err := s.Get(context.Background(), domain.ProjectKind, rejected); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("rejection published", err)
	}
	if err := namePut(s, domain.ProjectKind, id, 1, "different"); domain.SafeError(err).Cause != "" {
		t.Fatal("revision mislabeled", err)
	}
	_, err := s.Mutate(context.Background(), domain.NewID(), "name.delete", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.ProjectKind, id, 2) })
	if err != nil {
		t.Fatal(err)
	}
	if err := namePut(s, domain.ProjectKind, domain.NewID(), 0, "STRASSE"); err != nil {
		t.Fatal("deleted name reserved", err)
	}
	if err := namePut(s, domain.ProjectKind, id, 0, "fresh"); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("tombstone revived", err)
	}
}
func TestConfigurationNamesSerializeCompetingPublication(t *testing.T) {
	s, _ := openTest(t)
	var success atomic.Int32
	var refused atomic.Int32
	var wait sync.WaitGroup
	for _, name := range []string{"Alpha", " alpha "} {
		wait.Add(1)
		go func(name string) {
			defer wait.Done()
			err := namePut(s, domain.RepositoryKind, domain.NewID(), 0, name)
			if err == nil {
				success.Add(1)
			} else if domain.SafeError(err).Cause == domain.ConfigurationNameConflictCause {
				refused.Add(1)
			}
		}(name)
	}
	wait.Wait()
	if success.Load() != 1 || refused.Load() != 1 {
		t.Fatal("competing publications", success.Load(), refused.Load())
	}
}
