package store

import (
	"context"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestConfigurationNamesSerializeConcurrentCreatesAndPreserveNamespaces(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	results := make(chan error, 2)
	var joined sync.WaitGroup
	for _, name := range []string{"Straße", " STRASSE "} {
		joined.Add(1)
		go func(name string) {
			defer joined.Done()
			_, err := s.Mutate(ctx, domain.NewID(), "name.create", name, func(tx *Tx) (any, error) {
				return tx.Put(domain.ProjectKind, domain.NewID(), 0, "", "", map[string]string{"name": name})
			})
			results <- err
		}(name)
	}
	joined.Wait()
	close(results)
	successes, collisions := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if failure := domain.SafeError(err); failure.Code == domain.Conflict && failure.Cause == domain.ConfigurationNameConflictCause {
			collisions++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || collisions != 1 {
		t.Fatalf("publications=%d collisions=%d", successes, collisions)
	}
	_, err := s.Mutate(ctx, domain.NewID(), "other.namespace", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.RepositoryKind, domain.NewID(), 0, "", "", map[string]string{"name": "STRASSE"})
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(ctx, 0, "", 20)
	if err != nil || len(events) != 2 {
		t.Fatalf("collision emitted event: %d %v", len(events), err)
	}
}

func TestConfigurationNameRejectionRollsBackAndPreservesRevisionAndDeletion(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	original := create(t, s, domain.NewID(), " Alpha ")
	second := create(t, s, domain.NewID(), "Beta")
	requestID := domain.NewID()
	_, err := s.Mutate(ctx, requestID, "name.rename", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.TemplateKind, domain.NewID(), 0, "", "", map[string]string{"name": "temporary"}); err != nil {
			return nil, err
		}
		return tx.Put(domain.ProjectKind, second.ID, second.Revision, "", "", map[string]string{"name": "ALPHA"})
	})
	if domain.SafeError(err).Cause != domain.ConfigurationNameConflictCause {
		t.Fatal(err)
	}
	current, err := s.Get(ctx, domain.ProjectKind, second.ID)
	if err != nil || current.Revision != second.Revision || string(current.Data) != string(second.Data) {
		t.Fatal("rejected rename changed original", err)
	}
	templates, err := s.List(ctx, Filter{Kind: domain.TemplateKind, Limit: MaxPage})
	if err != nil || len(templates) != 0 {
		t.Fatal("rejected transaction retained partial writes", err)
	}
	_, found, err := s.Replay(ctx, requestID, "name.rename", nil)
	if err != nil || found {
		t.Fatal("rejection created success receipt", err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "name.self", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.ProjectKind, original.ID, original.Revision, "", "", map[string]string{"name": " Alpha "})
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "name.stale", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.ProjectKind, original.ID, original.Revision, "", "", map[string]string{"name": "Beta"})
	})
	if problem := domain.SafeError(err); problem.Code != domain.Conflict || problem.Cause == domain.ConfigurationNameConflictCause {
		t.Fatal("stale revision mislabeled", err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "name.delete", nil, func(tx *Tx) (any, error) {
		return nil, tx.Delete(domain.ProjectKind, original.ID, original.Revision+1)
	})
	if err != nil {
		t.Fatal(err)
	}
	create(t, s, domain.NewID(), "ALPHA")
	_, err = s.Mutate(ctx, domain.NewID(), "name.tombstone", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.ProjectKind, original.ID, 0, "", "", map[string]string{"name": "unused"})
	})
	if problem := domain.SafeError(err); problem.Code != domain.Conflict || problem.Cause == domain.ConfigurationNameConflictCause {
		t.Fatal("tombstone identity reused", err)
	}
}
