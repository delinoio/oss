package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestRepositorySaveRetainedSourceProofFailsClosed(t *testing.T) {
	for _, source := range []string{"https://generic.invalid/team/repo", "https://generic.invalid/team/repo.git", "https://github.com/Owner/Repo.git"} {
		for _, proof := range []string{"exact", "historical", "missing", "changed"} {
			t.Run(source+"/"+proof, func(t *testing.T) {
				s, _ := newDoctorFixture(t)
				mid, rid, parentID, childID := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
				doctorPut(t, s, domain.MachineKind, mid, 0, domain.Machine{Name: "fixture", OS: "linux", Architecture: "amd64"})
				repository := domain.Repository{Name: "fixture", RemoteURL: source, Checkouts: []domain.Checkout{{MachineID: mid, Path: "/fixture"}}}
				digest, err := domain.RepositoryCloneSourceIdentity(source)
				if err != nil {
					t.Fatal(err)
				}
				if proof == "missing" {
					digest = ""
				}
				if proof == "changed" {
					digest, _ = domain.RepositoryCloneSourceIdentity("https://other.invalid/team/repo")
				}
				if proof == "historical" {
					value := "generic.invalid\x00https\x00\x00/team/repo"
					if source == "https://github.com/Owner/Repo.git" {
						value = "github.com\x00owner/repo"
					}
					old := sha256.Sum256([]byte(value))
					digest = hex.EncodeToString(old[:])
				}
				parentRaw, _ := json.Marshal(repositorySaveInput{ID: rid, Repository: repository})
				childRaw, _ := json.Marshal(domain.RepositoryInspectionInput{Path: "/fixture", ExpectedRemoteIdentity: digest})
				originalRaw := string(childRaw)
				output, _ := json.Marshal(workspace.Inspection{Root: "/fixture", Name: "fixture", Remotes: []string{"origin"}, DefaultRefs: map[string]string{}})
				now := time.Now().UTC()
				_, err = s.Store.Mutate(context.Background(), domain.NewID(), "fixture.accepted-source", nil, func(tx *store.Tx) (any, error) {
					if _, err := tx.PutJob(parentID, 0, "", "", domain.Job{Type: domain.SaveRepositoryJob, State: domain.JobQueued, Input: parentRaw, AcceptedAt: now}); err != nil {
						return nil, err
					}
					if _, err := tx.PutJob(childID, 0, "", "", domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobSucceeded, ParentID: parentID, MachineID: mid, Input: childRaw, Output: output, AcceptedAt: now, FinishedAt: &now}); err != nil {
						return nil, err
					}
					return nil, finishRepositorySave(tx, parentID)
				})
				if err != nil {
					t.Fatal(err)
				}
				record, err := s.Store.Get(context.Background(), domain.JobKind, parentID)
				if err != nil {
					t.Fatal(err)
				}
				parent, err := store.Decode[domain.Job](record)
				if err != nil {
					t.Fatal(err)
				}
				success := proof == "exact" || proof == "historical" && source == "https://github.com/Owner/Repo.git"
				if (parent.State == domain.JobSucceeded) != success {
					t.Fatal("original proof was silently promoted or ordinary authority lost")
				}
				if !success && (parent.Problem == nil || parent.Problem.Code != domain.RecoveryRequired) {
					t.Fatal("missing or changed proof was not visible")
				}
				resources, err := s.Store.List(context.Background(), store.Filter{Kind: domain.RepositoryKind, Limit: 10})
				if err != nil || (len(resources) == 1) != success {
					t.Fatal("source proof failure published repository")
				}
				childRecord, err := s.Store.Get(context.Background(), domain.JobKind, childID)
				if err != nil {
					t.Fatal(err)
				}
				child, err := store.Decode[domain.Job](childRecord)
				if err != nil || string(child.Input) != originalRaw || child.State != domain.JobSucceeded {
					t.Fatal("retained original proof/report rewritten")
				}
			})
		}
	}
}
