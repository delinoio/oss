// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"path"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func cloneAuthority(tx *store.Tx, input domain.RepositoryCloneInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	if err := validateLocalOrigin(tx, domain.Session{Workspace: domain.Local, MachineID: input.MachineID, LocalOrigin: &input.LocalOrigin}); err != nil {
		return err
	}
	_, machine, err := activeMachine(tx, input.MachineID)
	if err != nil {
		return err
	}
	if !slices.Contains(machine.WorkerCapabilities, domain.RepositoryCloneV1) {
		return domain.Fail(domain.Unsupported, "This computer's Worker does not support repository clone.", "Update and reconnect it. Existing folder registration remains available.")
	}
	if input.GitHub != nil {
		_, profile, err := integrationFromTx(tx, input.GitHub.ProfileID, input.GitHub.Revision)
		if err != nil {
			return err
		}
		if profile.Connection == nil || profile.Pending != nil || profile.Connection.GenerationID != input.GitHub.GenerationID || !profile.AllowsGitHubOwner(input.GitHub.Owner) {
			return domain.Fail(domain.Conflict, "The selected GitHub profile is no longer available.", "Choose the current profile and repository explicitly again.")
		}
	}
	return nil
}
func (s *Service) CloneRepository(ctx context.Context, req *connect.Request[pb.CloneRepositoryRequest]) (*connect.Response[pb.CloneRepositoryResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	machine := domain.ID(req.Msg.MachineId)
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) || len(req.Msg.LocalWorkerToken) != 43 {
		return nil, rpc.Error(localOriginRequired(), correlation)
	}
	proof, err := base64.RawURLEncoding.DecodeString(req.Msg.LocalWorkerToken)
	if err != nil || len(proof) != sha256.Size || base64.RawURLEncoding.EncodeToString(proof) != req.Msg.LocalWorkerToken {
		clear(proof)
		return nil, rpc.Error(localOriginRequired(), correlation)
	}
	clear(proof)
	digest := sha256.Sum256([]byte(req.Msg.LocalWorkerToken))
	// The receipt digest includes the proof commitment, but jobs and responses
	// retain only original non-secret Worker identities. Replays run no admission.
	type cloneIntent struct {
		Actor                          domain.Principal
		MachineID                      domain.ID
		ParentPath, URL, DirectoryName string
		ProofDigest                    [32]byte
		Selection                      *pb.RepositoryCloneGitHubSelection
	}
	intent := cloneIntent{actor, machine, req.Msg.ParentPath, req.Msg.Url, req.Msg.DirectoryName, digest, req.Msg.GithubSelection}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "repository.clone", intent, func(tx *store.Tx) (any, error) {
		// Authenticate secondary Worker proof only for new admission, within the
		// same transaction as acceptance. An exact actor-bound receipt remains a
		// read after Worker retirement; it grants no replacement Clone authority.
		origin, err := tx.Authenticate(digest[:])
		if err != nil {
			return nil, err
		}
		if origin.Type != domain.WorkerDevice || origin.MachineID != machine {
			return nil, localOriginRequired()
		}
		input := domain.RepositoryCloneInput{RepositoryID: domain.NewID(), MachineID: machine, LocalOrigin: domain.LocalOrigin{MachineID: origin.MachineID, DeviceID: origin.DeviceID}, ParentPath: intent.ParentPath, URL: intent.URL, DirectoryName: intent.DirectoryName}
		if selected := intent.Selection; selected != nil {
			if selected.ExpectedRevision == 0 {
				return nil, domain.Fail(domain.InvalidArgument, "A current profile revision is required.", "Select the profile explicitly.")
			}
			_, profile, e := integrationFromTx(tx, domain.ID(selected.ProfileId), selected.ExpectedRevision)
			if e != nil {
				return nil, e
			}
			if profile.Connection == nil || profile.Pending != nil {
				return nil, domain.Fail(domain.Conflict, "The GitHub profile has no usable token generation.", "Finish the profile change before choosing a repository.")
			}
			input.GitHub = &domain.RepositoryCloneSelection{ProfileID: domain.ID(selected.ProfileId), Revision: selected.ExpectedRevision, GenerationID: profile.Connection.GenerationID, RepositoryID: selected.RepositoryId, NodeID: selected.NodeId, Owner: selected.Owner, Name: selected.Name}
		}
		if err := cloneAuthority(tx, input); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		return tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.CloneRepositoryJob, State: domain.JobQueued, MachineID: machine, Input: raw, AcceptedAt: time.Now().UTC()})
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var record store.Record
	if err := domain.Decode(result.Data, &record); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "repository_clone_accepted", "job_id", record.ID, "machine_id", machine, "replayed", result.Replayed, "correlation_id", correlation)
	response := connect.NewResponse(&pb.CloneRepositoryResponse{Job: rpc.Resource(record), RequestId: req.Msg.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func validateCloneInspection(input domain.RepositoryCloneInput, output workspace.Inspection) error {
	root := strings.ReplaceAll(output.Root, "\\", "/")
	if domain.ValidateRepositoryCloneParent(output.Root) != nil || path.Base(root) != input.DirectoryName || path.Clean(root) != strings.TrimSuffix(root, "/") || output.Name != input.DirectoryName || !slices.Contains(output.Remotes, "origin") || len(output.Remotes) > 128 || len(output.DefaultRefs) > 128 || output.ValidateGitHubRepositories() != nil {
		return domain.Fail(domain.RecoveryRequired, "The clone checkout could not be verified.", "Inspect the original job and its retained files before another operation.")
	}
	for _, remote := range output.Remotes {
		if domain.Text(remote, "remote name", 256, true) != nil {
			return domain.Fail(domain.RecoveryRequired, "The clone remote metadata is invalid.", "Inspect the original operation.")
		}
	}
	for remote, ref := range output.DefaultRefs {
		if !slices.Contains(output.Remotes, remote) || domain.Text(ref, "default reference", 4096, true) != nil {
			return domain.Fail(domain.RecoveryRequired, "The clone branch metadata is invalid.", "Inspect the original operation.")
		}
	}
	if input.GitHub != nil {
		identity := output.GitHubRepositories["origin"]
		if !strings.EqualFold(identity.Owner, input.GitHub.Owner) || !strings.EqualFold(identity.Name, input.GitHub.Name) {
			return domain.Fail(domain.RecoveryRequired, "The cloned repository identity changed.", "Preserve the checkout and inspect the original selection.")
		}
	}
	return nil
}

// Registration and the terminal job publish in the original report transaction.
// A frontend callback is neither needed nor authorized to register this checkout.
func finishRepositoryClone(tx *store.Tx, record store.Record, job domain.Job, revision uint64, raw []byte, problem *domain.Error) (store.Record, error) {
	var input domain.RepositoryCloneInput
	if domain.Decode(job.Input, &input) != nil || input.Validate() != nil || record.Revision != revision {
		return store.Record{}, domain.Fail(domain.RecoveryRequired, "The clone assignment changed.", "Observe the original accepted job.")
	}
	outcome := workspace.CloneResult{}
	if problem == nil {
		if domain.Decode(raw, &outcome) != nil || outcome.RepositoryID != "" || outcome.RepositoryRevision != 0 {
			return store.Record{}, domain.Fail(domain.InvalidArgument, "The clone result is malformed.", "Report only the original Worker clone outcome.")
		}
		if outcome.Problem != nil {
			problem = workerProblem(&pb.ErrorDetail{Code: string(outcome.Problem.Code)})
			if outcome.Problem.Code == domain.PermissionDenied && outcome.Problem.Cause == "clone_authentication" {
				problem = domain.Fail(domain.PermissionDenied, "This computer's Git or SSH authentication failed.", "Configure its existing Git credentials or SSH key. The GitHub PAT only reads repository metadata.")
			}
			if outcome.Problem.Code == domain.Unavailable && outcome.Problem.Cause == "clone_timeout" {
				problem = domain.Fail(domain.Unavailable, "Repository clone exceeded its ten-minute deadline.", "Inspect the original job and preserved files before another clone.")
			}
		}
		if outcome.Inspection != nil {
			if err := validateCloneInspection(input, *outcome.Inspection); err != nil {
				return store.Record{}, err
			}
		} else if problem == nil {
			problem = domain.Fail(domain.RecoveryRequired, "The clone returned no checkout.", "Inspect the original clone job and retained files.")
		}
	}
	if problem == nil {
		err := cloneAuthority(tx, input)
		if err == nil && job.AssignedDeviceID != input.LocalOrigin.DeviceID {
			err = localOriginRequired()
		}
		inspection := outcome.Inspection
		repository := domain.Repository{RemoteURL: input.URL, Name: input.DirectoryName, Checkouts: []domain.Checkout{{MachineID: input.MachineID, Path: inspection.Root}}, PreferredRemote: "origin", AutoFetch: true}
		if identity, ok := inspection.GitHubRepositories["origin"]; ok {
			repository.GitHubOwner, repository.GitHubName = identity.Owner, identity.Name
		}
		if input.GitHub != nil {
			repository.IntegrationID = input.GitHub.ProfileID
		}
		if err == nil {
			err = repository.Validate()
		}
		if err == nil {
			err = repositoryRevision(tx, input.RepositoryID, 0)
		}
		if err == nil {
			err = validateRelationships(tx, domain.RepositoryKind, input.RepositoryID, 0, &repository)
		}
		if err != nil {
			problem = domain.SafeError(err)
		} else {
			saved, e := tx.Put(domain.RepositoryKind, input.RepositoryID, 0, "", "", repository)
			if e != nil {
				return store.Record{}, e
			}
			outcome.RepositoryID, outcome.RepositoryRevision = saved.ID, saved.Revision
		}
	}
	outcome.Problem = problem
	now := time.Now().UTC()
	job.FinishedAt = &now
	job.State, job.Problem = domain.JobSucceeded, nil
	if problem != nil {
		job.State, job.Problem = domain.JobFailed, problem
		if problem.Code == domain.RecoveryRequired {
			job.State = domain.JobUncertain
		}
		if problem.Code == domain.Canceled {
			job.State = domain.JobCanceled
		}
	}
	var err error
	job.Output, err = json.Marshal(outcome)
	if err != nil {
		return store.Record{}, err
	}
	return tx.PutJob(record.ID, revision, "", "", job)
}
