// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestRepositoryBranchesFreezeAuthorityAndRejectStaleReports(t *testing.T) {
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	machine, repository, project, instance := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	records := map[domain.ID]store.Record{}
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.branches", nil, func(tx *store.Tx) (any, error) {
		for _, item := range []struct {
			kind  domain.Kind
			id    domain.ID
			value any
		}{
			{domain.MachineKind, machine, domain.Machine{Name: "fixture", OS: "linux", Architecture: "amd64", Version: "0.1.0", WorkerCapabilities: []domain.WorkerCapability{domain.RepositoryBranchDiscoveryV1}}},
			{domain.RepositoryKind, repository, domain.Repository{Name: "fixture", RemoteURL: "https://github.com/fixture/repo.git", PreferredRemote: "upstream"}},
			{domain.ProjectKind, project, domain.Project{Name: "fixture", Repositories: []domain.ID{repository}, PrimaryRepository: repository}},
		} {
			r, err := tx.Put(item.kind, item.id, 0, "", "", item.value)
			if err != nil {
				return nil, err
			}
			records[item.id] = r
		}
		return nil, tx.SetWorkerInstance(machine, instance, time.Now().UTC())
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: db, logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	request := &pb.DiscoverRepositoryBranchesRequest{RequestId: string(domain.NewID()), ProjectId: string(project), ProjectRevision: 1, RepositoryId: string(repository), RepositoryRevision: 1, MachineId: string(machine), MachineRevision: 1}
	accepted, err := s.DiscoverRepositoryBranches(ctx, connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.DiscoverRepositoryBranches(ctx, connect.NewRequest(request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != accepted.Msg.Job.Id {
		t.Fatal("lost discovery receipt", err)
	}
	var job domain.Job
	if err := domain.Decode(accepted.Msg.Job.DocumentJson, &job); err != nil {
		t.Fatal(err)
	}
	var input domain.RepositoryBranchesInput
	if err := domain.Decode(job.Input, &input); err != nil {
		t.Fatal(err)
	}
	if input.Source != "https://github.com/fixture/repo.git" || input.Remote != "upstream" {
		t.Fatal(input)
	}
	result := domain.RepositoryBranchesResult{ProjectID: project, ProjectRevision: 1, RepositoryID: repository, RepositoryRevision: 1, MachineID: machine, MachineRevision: 1, Remote: "upstream", Branches: []string{"main"}, ObservedAt: time.Now().UTC()}
	raw, _ := json.Marshal(result)
	if err := db.Read(ctx, func(tx *store.Tx) error { return finishRepositoryBranches(tx, job, raw) }); err != nil {
		t.Fatal(err)
	}
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.change", nil, func(tx *store.Tx) (any, error) {
		value, _ := store.Decode[domain.Repository](records[repository])
		value.RemoteURL = "https://github.com/fixture/changed.git"
		return tx.Put(domain.RepositoryKind, repository, 1, "", "", value)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Read(ctx, func(tx *store.Tx) error { return finishRepositoryBranches(tx, job, raw) }); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("stale report accepted", err)
	}
	if _, err := s.DiscoverRepositoryBranches(ctx, connect.NewRequest(&pb.DiscoverRepositoryBranchesRequest{RequestId: string(domain.NewID()), ProjectId: string(project), ProjectRevision: 1, RepositoryId: string(repository), RepositoryRevision: 1, MachineId: string(machine), MachineRevision: 1})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale request accepted", err)
	}
}
func TestRepositoryBranchesLargeResultHasFeatureOnlyStoreAllowance(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	job := domain.Job{Type: domain.DiscoverRepositoryBranchesJob, State: domain.JobSucceeded, MachineID: domain.NewID(), Input: json.RawMessage(`{}`), Output: json.RawMessage(`{"fixture":"` + strings.Repeat("x", 5<<20) + `"}`), AcceptedAt: time.Now().UTC()}
	id := domain.NewID()
	_, err = db.Mutate(ctx, domain.NewID(), "worker.branches.report", job, func(tx *store.Tx) (any, error) { return tx.PutJob(id, 0, "", "", job) })
	if err != nil {
		t.Fatal(err)
	}
	record, err := db.Get(ctx, domain.JobKind, id)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := store.Decode[domain.Job](record)
	if err != nil || len(decoded.Output) != len(job.Output) {
		t.Fatal(err)
	}
	job.Type = domain.InspectRepositoryJob
	if job.Validate() == nil {
		t.Fatal("generic job acquired discovery output allowance")
	}
}
