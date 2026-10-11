package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestConfigurationImportNamesRejectOverlayAndLateTargetCollisions(t *testing.T) {
	s, _ := newDoctorFixture(t)
	entry := transferEntry(domain.RepositoryKind, domain.Repository{Name: "Straße", RemoteURL: "https://example.test/repository.git", Checkouts: []domain.Checkout{}, AutoFetch: true})
	selection := domain.ConfigurationImportSelection{Bundle: domain.ConfigurationBundle{Version: domain.ConfigurationBundleVersion, Entries: []domain.ConfigurationEntry{entry}, Machines: []domain.ConfigurationMachine{}}, Bindings: []domain.ConfigurationBinding{}, Machines: []domain.ConfigurationMachineBinding{}, Checkouts: []domain.ConfigurationCheckoutBinding{}}
	second := transferEntry(domain.RepositoryKind, domain.Repository{Name: " STRASSE ", RemoteURL: "https://example.test/second.git", Checkouts: []domain.Checkout{}, AutoFetch: true})
	selection.Bundle.Entries = append(selection.Bundle.Entries, second)
	raw, _ := json.Marshal(selection)
	_, err := s.PreviewConfigurationImport(transferOwner(), connect.NewRequest(&pb.PreviewConfigurationImportRequest{SelectionJson: raw}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("intra-bundle collision preview admitted", err)
	}
	selection.Bundle.Entries = selection.Bundle.Entries[:1]
	preview := transferPreview(t, s, selection)
	existing := domain.NewID()
	doctorPut(t, s, domain.RepositoryKind, existing, 0, domain.Repository{Name: "STRASSE", RemoteURL: "https://example.test/current.git", Checkouts: []domain.Checkout{}})
	_, err = s.ApplyConfigurationImport(transferOwner(), connect.NewRequest(&pb.ApplyConfigurationImportRequest{RequestId: string(domain.NewID()), PreviewJson: preview}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("late target collision admitted", err)
	}
	rows, err := s.Store.List(context.Background(), store.Filter{Kind: domain.RepositoryKind, Limit: store.MaxPage})
	if err != nil || len(rows) != 1 || rows[0].ID != existing {
		t.Fatal("import partially published", err)
	}
	raw, _ = json.Marshal(selection)
	_, err = s.PreviewConfigurationImport(transferOwner(), connect.NewRequest(&pb.PreviewConfigurationImportRequest{SelectionJson: raw}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("target collision preview admitted", err)
	}
}

func TestRepositoryNamesRecheckAfterIndependentInspections(t *testing.T) {
	s, _ := newDoctorFixture(t)
	ctx := transferOwner()
	machine := domain.NewID()
	doctorPut(t, s, domain.MachineKind, machine, 0, domain.Machine{Name: "fixture", OS: "linux", Architecture: "amd64", Version: "0.1.0"})
	parents := []store.Record{}
	for _, name := range []string{"Alpha", " alpha "} {
		repository := domain.Repository{Name: name, Checkouts: []domain.Checkout{{MachineID: machine, Path: "/fixture/repo"}}, PreferredRemote: "origin", AutoFetch: true}
		raw, _ := json.Marshal(repository)
		result, err := saveRepository(ctx, s.Store, ConfigurationMutation{RequestID: domain.NewID(), Kind: domain.RepositoryKind, Document: raw}, repository)
		if err != nil {
			t.Fatal(err)
		}
		var parent store.Record
		if err := domain.Decode(result.Data, &parent); err != nil {
			t.Fatal(err)
		}
		parents = append(parents, parent)
	}
	for _, parent := range parents {
		_, err := s.Store.Mutate(ctx, domain.NewID(), "fixture.name-inspection", nil, func(tx *store.Tx) (any, error) {
			children, err := tx.Jobs("", parent.ID, "", "", store.MaxPage)
			if err != nil || len(children) != 1 {
				t.Fatalf("children: %v %v", children, err)
			}
			child, err := store.Decode[domain.Job](children[0])
			if err != nil {
				return nil, err
			}
			now := time.Now().UTC()
			child.State, child.FinishedAt = domain.JobSucceeded, &now
			child.Output, _ = json.Marshal(workspace.Inspection{Root: "/fixture/repo", Name: "repo", Remotes: []string{"origin"}, DefaultRefs: map[string]string{}})
			if _, err = tx.PutJob(children[0].ID, children[0].Revision, "", "", child); err != nil {
				return nil, err
			}
			return nil, finishRepositorySave(tx, parent.ID)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	first, _ := s.Store.Get(ctx, domain.JobKind, parents[0].ID)
	success, _ := store.Decode[domain.Job](first)
	last, _ := s.Store.Get(ctx, domain.JobKind, parents[1].ID)
	collision, _ := store.Decode[domain.Job](last)
	if success.State != domain.JobSucceeded || collision.State != domain.JobFailed || collision.Problem == nil || collision.Problem.Cause != domain.ConfigurationNameConflictCause {
		t.Fatal("late publication did not settle collision")
	}
	rows, err := s.Store.List(ctx, store.Filter{Kind: domain.RepositoryKind, Limit: store.MaxPage})
	if err != nil || len(rows) != 1 {
		t.Fatal("multiple repository publication", err)
	}
}
