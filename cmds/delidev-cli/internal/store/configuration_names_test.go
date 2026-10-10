// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestConfigurationNamesAtomicAndReplay(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	id := domain.NewID()
	original := create(t, s, id, " Café ")
	write := func(request, target domain.ID, expected uint64, name string) error {
		_, err := s.Mutate(ctx, request, "name.save", name, func(tx *Tx) (any, error) {
			return tx.Put(domain.ProjectKind, target, expected, "", "", map[string]string{"name": name})
		})
		return err
	}
	request := domain.NewID()
	err := write(request, domain.NewID(), 0, "CAFE\u0301")
	if safe := domain.SafeError(err); safe.Cause != string(domain.ConfigurationNameConflict) || safe.Code != domain.Conflict {
		t.Fatal(err)
	}
	// A rejected rename changes neither original bytes nor revision or events.
	second := create(t, s, domain.NewID(), "other")
	before, _ := s.Events(ctx, 0, "", 100)
	request = domain.NewID()
	if err := write(request, second.ID, 1, "cafe\u0301"); domain.SafeError(err).Cause != string(domain.ConfigurationNameConflict) {
		t.Fatal(err)
	}
	after, _ := s.Events(ctx, 0, "", 100)
	row, _ := s.Get(ctx, domain.ProjectKind, second.ID)
	if row.Revision != 1 || string(row.Data) != string(second.Data) || len(before) != len(after) {
		t.Fatal("rejected rename changed publication")
	}
	if err := write(domain.NewID(), second.ID, 99, "café"); domain.SafeError(err).Cause != "" {
		t.Fatal("name masked revision", err)
	}
	if err := write(domain.NewID(), id, original.Revision, "CAFÉ"); err != nil {
		t.Fatal("own identity collided", err)
	}
	request = domain.NewID()
	fresh := domain.NewID()
	if err := write(request, fresh, 0, "replayed"); err != nil {
		t.Fatal(err)
	}
	if err := write(request, fresh, 0, "replayed"); err != nil {
		t.Fatal("original request replay collided", err)
	}
	replayed, _ := s.Get(ctx, domain.ProjectKind, fresh)
	if replayed.Revision != 1 {
		t.Fatal("replay republished resource", replayed.Revision)
	}
	// Separate kinds share the spelling, and deletion releases the name.
	_, err = s.Mutate(ctx, domain.NewID(), "name.repository", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.RepositoryKind, domain.NewID(), 0, "", "", map[string]string{"name": "café"})
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "name.delete", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.ProjectKind, id, 2) })
	if err != nil {
		t.Fatal(err)
	}
	if err := write(domain.NewID(), domain.NewID(), 0, "café"); err != nil {
		t.Fatal("deleted name reserved", err)
	}
}
func TestConfigurationNamesConcurrentPublish(t *testing.T) {
	s, _ := openTest(t)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for _, name := range []string{"Straße", " STRASSE "} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_, err := s.Mutate(context.Background(), domain.NewID(), "name.concurrent", name, func(tx *Tx) (any, error) {
				return tx.Put(domain.RepositoryKind, domain.NewID(), 0, "", "", map[string]string{"name": name})
			})
			if err == nil {
				successes.Add(1)
			} else if domain.SafeError(err).Cause != string(domain.ConfigurationNameConflict) {
				t.Error(err)
			}
		}(name)
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("concurrent duplicate publication", successes.Load())
	}
}
