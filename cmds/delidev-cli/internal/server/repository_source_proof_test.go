// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func repositorySourceProofFixture(t *testing.T, source, mode string) string {
	t.Helper()
	if mode == "missing" {
		return ""
	}
	if mode == "historical" {
		return "aee038bac35f715fe0d4bcf220ec4cddcbd816f2ccf93e17c5e1c58794daeb1b"
	}
	if mode == "changed" {
		source += ".git"
	}
	proof, err := domain.RepositoryCloneSourceIdentity(source)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func TestRepositoryInspectionSourceProofGitHubCompatibility(t *testing.T) {
	const historical = "f2b6c1782a95b061ac5a3e7f279129e50df6a4705aa19a90d860f9af55cac61a"
	raw, _ := json.Marshal(domain.RepositoryInspectionInput{Path: "/repo", ExpectedRemoteIdentity: historical})
	child := domain.Job{Type: domain.InspectRepositoryJob, Input: raw}
	for _, source := range []string{"https://github.com/Owner/Repo", "git@github.com:owner/repo.git", "ssh://git@github.com/owner/repo.git"} {
		if err := validateRepositoryInspectionSource(source, child); err != nil {
			t.Fatal("established GitHub source proof was refused", err)
		}
	}
	child.Input = []byte(`{"unexpected":true}`)
	if domain.SafeError(validateRepositoryInspectionSource("https://github.com/owner/repo.git", child)).Code != domain.RecoveryRequired {
		t.Fatal("malformed retained source proof was accepted")
	}
}

func TestRepositorySaveRequiresOriginalSourceProof(t *testing.T) {
	for _, mode := range []string{"current", "missing", "historical", "changed"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := newDoctorFixture(t)
			ctx := context.Background()
			machine := domain.NewID()
			doctorPut(t, s, domain.MachineKind, machine, 0, domain.Machine{Name: "Runner", OS: "linux", Architecture: "amd64", WorkerCapabilities: []domain.WorkerCapability{domain.RepositoryInspectionMetadataV1}})
			repository := domain.Repository{Name: "Source", RemoteURL: "https://generic.invalid/team/repo", Checkouts: []domain.Checkout{{MachineID: machine, Path: "/repo"}}}
			raw, _ := json.Marshal(repository)
			request := ConfigurationMutation{RequestID: domain.NewID(), Kind: domain.RepositoryKind, Document: raw}
			accepted, err := SaveConfiguration(ctx, s.Store, request)
			if err != nil {
				t.Fatal(err)
			}
			var parent store.Record
			if err := domain.Decode(accepted.Data, &parent); err != nil {
				t.Fatal(err)
			}
			original, err := store.Decode[domain.Job](parent)
			if err != nil {
				t.Fatal(err)
			}
			var children []store.Record
			if err := s.Store.Read(ctx, func(tx *store.Tx) error {
				var err error
				children, err = tx.Jobs("", parent.ID, "", "", 10)
				return err
			}); err != nil || len(children) != 1 {
				t.Fatal("inspection child unavailable", err)
			}
			var retainedProof []byte
			_, err = s.Store.Mutate(ctx, domain.NewID(), "fixture.source-proof", nil, func(tx *store.Tx) (any, error) {
				child, err := store.Decode[domain.Job](children[0])
				if err != nil {
					return nil, err
				}
				var input domain.RepositoryInspectionInput
				if err := domain.Decode(child.Input, &input); err != nil {
					return nil, err
				}
				input.ExpectedRemoteIdentity = repositorySourceProofFixture(t, repository.RemoteURL, mode)
				child.Input, _ = json.Marshal(input)
				retainedProof = append([]byte(nil), child.Input...)
				now := time.Now().UTC()
				child.State, child.FinishedAt = domain.JobSucceeded, &now
				child.Output, _ = json.Marshal(workspace.Inspection{Root: "/repo", Name: "Source"})
				if _, err := tx.PutJob(children[0].ID, children[0].Revision, "", "", child); err != nil {
					return nil, err
				}
				return nil, finishRepositorySave(tx, parent.ID)
			})
			if err != nil {
				t.Fatal(err)
			}
			row, err := s.Store.Get(ctx, domain.JobKind, parent.ID)
			if err != nil {
				t.Fatal(err)
			}
			finished, err := store.Decode[domain.Job](row)
			if err != nil {
				t.Fatal(err)
			}
			repositories, err := s.Store.List(ctx, store.Filter{Kind: domain.RepositoryKind, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "current" {
				if finished.State != domain.JobSucceeded || len(repositories) != 1 {
					t.Fatal("current source proof did not publish atomically")
				}
			} else if finished.State != domain.JobUncertain || finished.Problem == nil || finished.Problem.Code != domain.RecoveryRequired || len(repositories) != 0 {
				t.Fatal("missing or changed original proof published configuration")
			}
			if !bytes.Equal(finished.Input, original.Input) {
				t.Fatal("accepted source was rewritten")
			}
			childRow, err := s.Store.Get(ctx, domain.JobKind, children[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			child, err := store.Decode[domain.Job](childRow)
			if err != nil || child.State != domain.JobSucceeded || !bytes.Equal(child.Input, retainedProof) {
				t.Fatal("original successful child proof was changed", err)
			}
			replay, err := SaveConfiguration(ctx, s.Store, request)
			if err != nil || !replay.Replayed || !bytes.Equal(replay.Data, accepted.Data) {
				t.Fatal("original receipt did not survive source refusal", err)
			}
		})
	}
}
