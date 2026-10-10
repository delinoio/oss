// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"sync"
	"testing"
)

func nameWrite(s *Store, request domain.ID, kind domain.Kind, id domain.ID, revision uint64, name string) (Result, error) {
	input := struct {
		Kind     domain.Kind
		ID       domain.ID
		Revision uint64
		Name     string
	}{kind, id, revision, name}
	return s.Mutate(context.Background(), request, "test.name.save", input, func(tx *Tx) (any, error) {
		return tx.Put(kind, id, revision, "", "", map[string]any{"name": name, "archived": true})
	})
}
func TestConfigurationNamesRefuseEquivalentCreateAndRenameWithoutPublication(t *testing.T) {
	for _, kind := range []domain.Kind{domain.ProjectKind, domain.RepositoryKind} {
		t.Run(string(kind), func(t *testing.T) {
			s, _ := openTest(t)
			first, other := domain.NewID(), domain.NewID()
			if _, err := nameWrite(s, domain.NewID(), kind, first, 0, "Alpha"); err != nil {
				t.Fatal(err)
			}
			if _, err := nameWrite(s, domain.NewID(), kind, other, 0, "Other"); err != nil {
				t.Fatal(err)
			}
			prior, err := s.Get(context.Background(), kind, other)
			if err != nil {
				t.Fatal(err)
			}
			counts := func() [3]int {
				t.Helper()
				var n [3]int
				if err := s.Read(context.Background(), func(tx *Tx) error {
					for i, table := range []string{"entities", "events", "receipts"} {
						if err := tx.tx.QueryRowContext(tx.ctx, "SELECT COUNT(*) FROM "+table).Scan(&n[i]); err != nil {
							return err
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				return n
			}
			before := counts()
			for _, name := range []string{"Alpha", " alpha ", "ALPHA"} {
				for _, update := range []bool{false, true} {
					id, rev := domain.NewID(), uint64(0)
					if update {
						id, rev = other, prior.Revision
					}
					_, err := nameWrite(s, domain.NewID(), kind, id, rev, name)
					p := domain.SafeError(err)
					if err == nil || p.Code != domain.Conflict || p.Cause != domain.ConfigurationNameConflictCause || strings.Contains(p.Error(), string(first)) {
						t.Fatalf("wrong collision: %v", err)
					}
					if counts() != before {
						t.Fatal("collision published data, events or receipt")
					}
				}
			}
			after, err := s.Get(context.Background(), kind, other)
			if err != nil || after.Revision != prior.Revision || string(after.Data) != string(prior.Data) {
				t.Fatal("rejected rename changed original")
			}
			_, err = nameWrite(s, domain.NewID(), kind, other, prior.Revision+1, "Alpha")
			if err == nil || domain.SafeError(err).Cause != "" {
				t.Fatal("revision conflict became name collision", err)
			}
			if _, err := nameWrite(s, domain.NewID(), kind, first, 1, "Alpha"); err != nil {
				t.Fatal("same original ID blocked", err)
			}
		})
	}
}
func TestConfigurationNameNamespacesUnicodeDeletionAndOriginalReplay(t *testing.T) {
	s, _ := openTest(t)
	project, repository := domain.NewID(), domain.NewID()
	request := domain.NewID()
	if _, err := nameWrite(s, request, domain.ProjectKind, project, 0, "Straße"); err != nil {
		t.Fatal(err)
	}
	if _, err := nameWrite(s, domain.NewID(), domain.RepositoryKind, repository, 0, "STRASSE"); err != nil {
		t.Fatal("cross-kind collision", err)
	}
	if _, err := nameWrite(s, domain.NewID(), domain.ProjectKind, domain.NewID(), 0, "STRASSE"); err == nil {
		t.Fatal("default fold collision accepted")
	}
	korean := domain.NewID()
	if _, err := nameWrite(s, domain.NewID(), domain.ProjectKind, korean, 0, "한글"); err != nil {
		t.Fatal(err)
	}
	if _, err := nameWrite(s, domain.NewID(), domain.ProjectKind, domain.NewID(), 0, "한글"); err == nil {
		t.Fatal("NFC collision accepted")
	}
	for _, name := range []string{"a b", "a  b"} {
		if _, err := nameWrite(s, domain.NewID(), domain.ProjectKind, domain.NewID(), 0, name); err != nil {
			t.Fatal("internal spaces collapsed", err)
		}
	}
	if _, err := nameWrite(s, domain.NewID(), domain.ProjectKind, project, 1, "Renamed"); err != nil {
		t.Fatal(err)
	}
	replacement := domain.NewID()
	if _, err := nameWrite(s, domain.NewID(), domain.ProjectKind, replacement, 0, "STRASSE"); err != nil {
		t.Fatal(err)
	}
	replay, err := nameWrite(s, request, domain.ProjectKind, project, 0, "Straße")
	if err != nil || !replay.Replayed {
		t.Fatal("original receipt revalidated name", err)
	}
	row, err := s.Get(context.Background(), domain.ProjectKind, project)
	if err != nil || !strings.Contains(string(row.Data), "Renamed") {
		t.Fatal("replay rewrote history")
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "test.name.delete", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.ProjectKind, replacement, 1) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nameWrite(s, domain.NewID(), domain.ProjectKind, domain.NewID(), 0, "Straße"); err != nil {
		t.Fatal("tombstone reserved display name", err)
	}
	if _, err := nameWrite(s, domain.NewID(), domain.ProjectKind, replacement, 0, "Fresh"); err == nil || domain.SafeError(err).Cause != "" {
		t.Fatal("tombstoned identity reused/mislabeled")
	}
}
func TestConcurrentConfigurationNameCreatesPublishOnce(t *testing.T) {
	s, _ := openTest(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"Alpha", " ALPHA "} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			<-start
			_, err := nameWrite(s, domain.NewID(), domain.ProjectKind, domain.NewID(), 0, name)
			results <- err
		}(name)
	}
	close(start)
	wg.Wait()
	close(results)
	success, collision := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if domain.SafeError(err).Cause == domain.ConfigurationNameConflictCause {
			collision++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || collision != 1 {
		t.Fatalf("success=%d collision=%d", success, collision)
	}
	rows, err := s.List(context.Background(), Filter{Kind: domain.ProjectKind, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal("duplicate publication")
	}
	var value map[string]any
	if json.Unmarshal(rows[0].Data, &value) != nil || (value["name"] != "Alpha" && value["name"] != " ALPHA ") {
		t.Fatal("stored display spelling normalized")
	}
}

func TestConfigurationNameRefusalDoesNotReplaceAuthorizationOrReadOnlyErrors(t *testing.T) {
	s, _ := openTest(t)
	if _, err := nameWrite(s, domain.NewID(), domain.ProjectKind, domain.NewID(), 0, "Alpha"); err != nil {
		t.Fatal(err)
	}
	actor := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: domain.NewID()})
	_, err := s.Mutate(actor, domain.NewID(), "test.denied.name", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.ProjectKind, domain.NewID(), 0, "", "", map[string]string{"name": "ALPHA"})
	})
	if err == nil || domain.SafeError(err).Code != domain.Unauthenticated || domain.SafeError(err).Cause != "" {
		t.Fatal("naming exposed before authorization", err)
	}
	err = s.Read(context.Background(), func(tx *Tx) error {
		_, e := tx.Put(domain.ProjectKind, domain.NewID(), 0, "", "", map[string]string{"name": "ALPHA"})
		return e
	})
	if err == nil || domain.SafeError(err).Code != domain.PermissionDenied || domain.SafeError(err).Cause != "" {
		t.Fatal("naming replaced read-only protection", err)
	}
}
