package server

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const (
	identityUsedMessage    = "This identity is already used."
	identityUsedGuidance   = "Create the entity with a new ID."
	identityDeletedMessage = "This entity was permanently deleted."
	identityDeletedAdvice  = "Create a new entity with a new ID."
)

type identitySnapshot struct {
	Rows   map[domain.Kind][]store.Record
	Events int
}

func snapshotIdentityState(t *testing.T, s *Service) identitySnapshot {
	t.Helper()
	snapshot := identitySnapshot{Rows: map[domain.Kind][]store.Record{}}
	for _, kind := range []domain.Kind{domain.ProjectKind, domain.RepositoryKind, domain.MachineKind} {
		rows, err := s.Store.List(context.Background(), store.Filter{Kind: kind, Limit: store.MaxPage})
		if err != nil {
			t.Fatal(err)
		}
		snapshot.Rows[kind] = rows
	}
	events, err := s.Store.Events(context.Background(), 0, "", store.MaxPage)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Events = len(events)
	return snapshot
}

func identityProject(repository domain.ID, name string) domain.Project {
	return domain.Project{Name: name, Repositories: []domain.ID{repository}, PrimaryRepository: repository}
}

func identityRepositoryFixture(t *testing.T, s *Service) domain.ID {
	t.Helper()
	id := domain.NewID()
	doctorPut(t, s, domain.RepositoryKind, id, 0, domain.Repository{Name: "Fixture repository", RemoteURL: "https://example.test/fixture.git", Checkouts: []domain.Checkout{}, AutoFetch: true})
	return id
}

func identityProjectMutation(t *testing.T, repository, id domain.ID, revision uint64, name string) ConfigurationMutation {
	t.Helper()
	raw, err := json.Marshal(identityProject(repository, name))
	if err != nil {
		t.Fatal(err)
	}
	return ConfigurationMutation{RequestID: domain.NewID(), ID: id, ExpectedRevision: revision, Kind: domain.ProjectKind, Document: raw}
}

func tombstoneIdentity(t *testing.T, s *Service, kind domain.Kind, id domain.ID) {
	t.Helper()
	_, err := s.Store.Mutate(context.Background(), domain.NewID(), "identity.fixture.delete", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.Delete(kind, id, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSaveConfigurationProjectCreateIdentityOutranksNameConflict(t *testing.T) {
	s, _ := newDoctorFixture(t)
	ctx := context.Background()
	repository := identityRepositoryFixture(t, s)
	existingName := "Taken"
	sameKind := domain.NewID()
	doctorPut(t, s, domain.ProjectKind, sameKind, 0, identityProject(repository, existingName))
	otherKind := domain.NewID()
	doctorPut(t, s, domain.MachineKind, otherKind, 0, domain.Machine{Name: "fixture", OS: "linux", Architecture: "amd64", Version: "0.1.0"})
	tombstoned := domain.NewID()
	doctorPut(t, s, domain.ProjectKind, tombstoned, 0, identityProject(repository, "Soon deleted"))
	tombstoneIdentity(t, s, domain.ProjectKind, tombstoned)

	cases := []struct {
		name     string
		id       domain.ID
		message  string
		guidance string
	}{
		{"same kind live ID", sameKind, identityUsedMessage, identityUsedGuidance},
		{"other kind live ID", otherKind, identityUsedMessage, identityUsedGuidance},
		{"tombstoned ID", tombstoned, identityDeletedMessage, identityDeletedAdvice},
	}
	for _, test := range cases {
		for _, submitted := range []string{existingName, " TAKEN ", "Available"} {
			t.Run(test.name+"/"+submitted, func(t *testing.T) {
				before := snapshotIdentityState(t, s)
				_, err := SaveConfiguration(ctx, s.Store, identityProjectMutation(t, repository, test.id, 0, submitted))
				failure := domain.SafeError(err)
				if failure.Code != domain.Conflict || failure.Cause != "" || failure.Message != test.message || failure.Guidance != test.guidance {
					t.Fatalf("unexpected identity failure: %+v", failure)
				}
				if after := snapshotIdentityState(t, s); !reflect.DeepEqual(before, after) {
					t.Fatal("rejected create changed persisted state")
				}
			})
		}
	}
}

func TestSaveConfigurationProjectCreateKeepsNameAndRevisionPrecedence(t *testing.T) {
	s, _ := newDoctorFixture(t)
	ctx := context.Background()
	repository := identityRepositoryFixture(t, s)
	existing := domain.NewID()
	doctorPut(t, s, domain.ProjectKind, existing, 0, identityProject(repository, "Taken"))

	t.Run("unused ID with equivalent name", func(t *testing.T) {
		before := snapshotIdentityState(t, s)
		_, err := SaveConfiguration(ctx, s.Store, identityProjectMutation(t, repository, domain.NewID(), 0, " taken "))
		if failure := domain.SafeError(err); failure.Code != domain.Conflict || failure.Cause != domain.ConfigurationNameConflictCause {
			t.Fatalf("expected name conflict, got %+v", failure)
		}
		if after := snapshotIdentityState(t, s); !reflect.DeepEqual(before, after) {
			t.Fatal("rejected create changed persisted state")
		}
	})
	t.Run("generated ID with available name", func(t *testing.T) {
		if _, err := SaveConfiguration(ctx, s.Store, identityProjectMutation(t, repository, "", 0, "Fresh")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("stale update revision", func(t *testing.T) {
		before := snapshotIdentityState(t, s)
		_, err := SaveConfiguration(ctx, s.Store, identityProjectMutation(t, repository, existing, 7, "Taken"))
		failure := domain.SafeError(err)
		if failure.Code != domain.Conflict || failure.Cause != "" || failure.Message != "The entity revision changed." {
			t.Fatalf("expected revision conflict, got %+v", failure)
		}
		if after := snapshotIdentityState(t, s); !reflect.DeepEqual(before, after) {
			t.Fatal("rejected update changed persisted state")
		}
	})
}

func TestRepositoryCreateIdentityUsesOrdinaryGuidanceAndRPCAborts(t *testing.T) {
	s, _ := newDoctorFixture(t)
	ctx := transferOwner()
	deleted := domain.NewID()
	doctorPut(t, s, domain.RepositoryKind, deleted, 0, domain.Repository{Name: "Deleted repository", RemoteURL: "https://example.test/deleted.git", Checkouts: []domain.Checkout{}, AutoFetch: true})
	tombstoneIdentity(t, s, domain.RepositoryKind, deleted)
	raw, err := json.Marshal(domain.Repository{Name: "Repo", RemoteURL: "https://example.test/repo.git", Checkouts: []domain.Checkout{}, AutoFetch: true})
	if err != nil {
		t.Fatal(err)
	}
	live := domain.NewID()
	doctorPut(t, s, domain.RepositoryKind, live, 0, domain.Repository{Name: "Live repository", RemoteURL: "https://example.test/live.git", Checkouts: []domain.Checkout{}, AutoFetch: true})
	otherKind := domain.NewID()
	doctorPut(t, s, domain.MachineKind, otherKind, 0, domain.Machine{Name: "fixture", OS: "linux", Architecture: "amd64", Version: "0.1.0"})
	cases := []struct {
		name     string
		id       domain.ID
		message  string
		guidance string
	}{
		{"same kind live ID", live, identityUsedMessage, identityUsedGuidance},
		{"other kind live ID", otherKind, identityUsedMessage, identityUsedGuidance},
		{"tombstoned ID", deleted, identityDeletedMessage, identityDeletedAdvice},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			before := snapshotIdentityState(t, s)
			err := s.Store.Read(context.Background(), func(tx *store.Tx) error { return repositoryRevision(tx, test.id, 0) })
			failure := domain.SafeError(err)
			if failure.Code != domain.Conflict || failure.Cause != "" || failure.Message != test.message || failure.Guidance != test.guidance {
				t.Fatalf("unexpected identity failure: %+v", failure)
			}
			_, err = s.SaveConfiguration(ctx, connect.NewRequest(&pb.SaveConfigurationRequest{
				Mutation:      &pb.Mutation{Id: string(test.id), RequestId: string(domain.NewID())},
				Kind:          pb.EntityKind_ENTITY_KIND_REPOSITORY,
				SchemaVersion: rpc.ResourceSchemaVersion(domain.RepositoryKind, raw),
				DocumentJson:  raw,
			}))
			if connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("expected aborted identity conflict", err)
			}
			if after := snapshotIdentityState(t, s); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected create changed persisted state")
			}
		})
	}
}

func TestImportAdmissionKeepsPreviewIdentityWording(t *testing.T) {
	s, _ := newDoctorFixture(t)
	repository := identityRepositoryFixture(t, s)
	live := domain.NewID()
	doctorPut(t, s, domain.ProjectKind, live, 0, identityProject(repository, "Occupied"))
	tombstoned := domain.NewID()
	doctorPut(t, s, domain.ProjectKind, tombstoned, 0, identityProject(repository, "Deleted"))
	tombstoneIdentity(t, s, domain.ProjectKind, tombstoned)
	for _, id := range []domain.ID{live, tombstoned} {
		err := s.Store.Read(context.Background(), func(tx *store.Tx) error { return tx.RequireUnusedID(id) })
		failure := domain.SafeError(err)
		if failure.Code != domain.Conflict || failure.Message != "An imported target identity was already used or permanently deleted." || failure.Guidance != "Generate a new preview with fresh target identities; never recreate a deleted entity." {
			t.Fatalf("import wording changed: %+v", failure)
		}
	}
}
