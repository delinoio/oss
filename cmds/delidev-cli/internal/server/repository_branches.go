// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func repositoryBranchAuthority(tx *store.Tx, input domain.RepositoryBranchesInput) error {
	projectRecord, err := tx.Get(domain.ProjectKind, input.ProjectID)
	if err != nil {
		return err
	}
	project, err := store.Decode[domain.Project](projectRecord)
	if err != nil {
		return err
	}
	repositoryRecord, err := tx.Get(domain.RepositoryKind, input.RepositoryID)
	if err != nil {
		return err
	}
	repository, err := store.Decode[domain.Repository](repositoryRecord)
	if err != nil {
		return err
	}
	machineRecord, machine, err := activeMachine(tx, input.MachineID)
	if err != nil {
		return err
	}
	if projectRecord.Revision != input.ProjectRevision || repositoryRecord.Revision != input.RepositoryRevision || machineRecord.Revision != input.MachineRevision || !slices.Contains(project.Repositories, input.RepositoryID) || repository.RemoteURL != input.Source {
		return domain.Fail(domain.Conflict, "Repository branch authority changed.", "Refresh current project, repository and Worker configuration.")
	}
	if !slices.Contains(machine.WorkerCapabilities, domain.RepositoryBranchDiscoveryV1) {
		return domain.Fail(domain.Unsupported, "This Worker does not support remote branch discovery.", "Use the saved or manual starting reference.")
	}
	_, seen, err := tx.WorkerInstance(input.MachineID)
	if err != nil || time.Since(seen) > domain.WorkerConnectionTimeout {
		return domain.Fail(domain.Unavailable, "The selected Worker is offline.", "Reconnect the selected Worker or use the saved/manual reference.")
	}
	return input.Validate()
}
func (s *Service) DiscoverRepositoryBranches(ctx context.Context, req *connect.Request[pb.DiscoverRepositoryBranchesRequest]) (*connect.Response[pb.DiscoverRepositoryBranchesResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if actor, ok := domain.PrincipalFrom(ctx); !ok || actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Branch discovery requires an authorized owner or client.", "Use the original viewing connection."), correlation)
	}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "repository.branches", req.Msg, func(tx *store.Tx) (any, error) {
		repositoryRecord, err := tx.Get(domain.RepositoryKind, domain.ID(req.Msg.RepositoryId))
		if err != nil {
			return nil, err
		}
		repository, err := store.Decode[domain.Repository](repositoryRecord)
		if err != nil {
			return nil, err
		}
		identity, err := domain.RepositoryCloneSourceIdentity(repository.RemoteURL)
		if err != nil {
			return nil, err
		}
		remote := repository.PreferredRemote
		if remote == "" {
			remote = "origin"
		}
		input := domain.RepositoryBranchesInput{ProjectID: domain.ID(req.Msg.ProjectId), ProjectRevision: req.Msg.ProjectRevision, RepositoryID: domain.ID(req.Msg.RepositoryId), RepositoryRevision: req.Msg.RepositoryRevision, MachineID: domain.ID(req.Msg.MachineId), MachineRevision: req.Msg.MachineRevision, Source: repository.RemoteURL, SourceIdentity: identity, Remote: remote}
		if err := repositoryBranchAuthority(tx, input); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		return tx.PutJob(domain.NewID(), 0, "", input.ProjectID, domain.Job{Type: domain.DiscoverRepositoryBranchesJob, State: domain.JobQueued, MachineID: input.MachineID, Input: raw, AcceptedAt: time.Now().UTC()})
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var record store.Record
	if err := domain.Decode(result.Data, &record); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.DiscoverRepositoryBranchesResponse{Job: rpc.Resource(record), RequestId: req.Msg.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func finishRepositoryBranches(tx *store.Tx, job domain.Job, raw []byte) error {
	var input domain.RepositoryBranchesInput
	if err := domain.Decode(job.Input, &input); err != nil {
		return err
	}
	if err := repositoryBranchAuthority(tx, input); err != nil {
		return err
	}
	var result domain.RepositoryBranchesResult
	if err := domain.DecodeWithLimit(raw, &result, domain.MaxRepositoryBranchesBytes); err != nil {
		return err
	}
	return result.Validate(input)
}

func reportResultLimit(operation string) int {
	if operation == "worker.branches.report" {
		return 2 * domain.MaxRepositoryBranchesJobBytes
	}
	return 1 << 20
}
