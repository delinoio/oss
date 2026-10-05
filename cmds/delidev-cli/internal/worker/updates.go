// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

type UpdatePhase string

const (
	UpdateClaiming UpdatePhase = "claiming"
	UpdateClaimed  UpdatePhase = "claimed"
	UpdatePrepared UpdatePhase = "prepared"
	UpdateStarting UpdatePhase = "starting"
	UpdateRollback UpdatePhase = "rollback"
	UpdateComplete UpdatePhase = "complete"
	UpdateUnknown  UpdatePhase = "uncertain"
)

type UpdateJournal struct {
	Version            uint32                 `json:"version"`
	ID                 domain.ID              `json:"id"`
	Phase              UpdatePhase            `json:"phase"`
	Operation          updates.Operation      `json:"operation"`
	ClaimID            domain.ID              `json:"claim_id"`
	ExpectedRevision   uint64                 `json:"expected_revision,string"`
	ReportID           domain.ID              `json:"report_id"`
	OldGeneration      domain.ID              `json:"old_generation"`
	NewGeneration      domain.ID              `json:"new_generation,omitempty"`
	RollbackGeneration domain.ID              `json:"rollback_generation,omitempty"`
	ArtifactPath       string                 `json:"artifact_path,omitempty"`
	PreviousPath       string                 `json:"previous_path,omitempty"`
	PreviousArtifact   *updates.Artifact      `json:"previous_artifact,omitempty"`
	Outcome            pb.WorkerUpdateOutcome `json:"outcome,omitempty"`
}
type UpdateHandoff struct{ ID domain.ID }

func (e *UpdateHandoff) Error() string {
	return "The original Worker update is ready for joined replacement."
}
func updatePath(root string, id domain.ID) string {
	return filepath.Join(root, "worker-updates", string(id)+".json")
}
func WriteUpdateJournal(root string, j UpdateJournal) error {
	if !j.valid() {
		return updateFailure()
	}
	if e := security.PrivateDir(filepath.Join(root, "worker-updates")); e != nil {
		return e
	}
	raw, e := json.Marshal(j)
	if e != nil {
		return e
	}
	return security.WriteAtomic(updatePath(root, j.ID), raw)
}
func ReadUpdateJournal(root string, id domain.ID) (UpdateJournal, error) {
	var j UpdateJournal
	if id.Validate() != nil {
		return j, updateFailure()
	}
	raw, e := security.ReadPrivate(updatePath(root, id), 256<<10)
	if e != nil {
		return j, e
	}
	if domain.DecodeWithLimit(raw, &j, 256<<10) != nil || j.ID != id || !j.valid() {
		return j, updateFailure()
	}
	return j, nil
}
func updateFailure() error {
	return domain.Fail(domain.RecoveryRequired, "The original Worker update is unconfirmed.", "Preserve both working binaries, registration and workspaces; inspect this exact operation without repeating replacement.")
}
func watchUpdates(ctx context.Context, config Config, credential Credential, instance domain.ID) error {
	httpClient, transport := rpc.HTTPClient()
	defer transport.CloseIdleConnections()
	client := delidevv1connect.NewInstallationServiceClient(httpClient, credential.Endpoint)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		response, e := client.PollWorkerUpdate(bounded, authenticated(credential, &pb.PollWorkerUpdateRequest{InstanceId: string(instance)}))
		cancel()
		if e == nil && response.Msg.Update != nil && response.Msg.Idle {
			var operation updates.Operation
			r := response.Msg.Update
			if domain.Decode(r.DocumentJson, &operation) != nil || operation.ServerID != credential.ServerID || operation.DeviceID != credential.DeviceID || operation.MachineID != credential.MachineID {
				return updateFailure()
			}
			if operation.State == updates.Waiting || operation.State == updates.Running {
				status, e := Status(config.Root)
				if e != nil || status.State != StateRunning {
					return updateFailure()
				}
				id := domain.ID(r.Id)
				j, readErr := ReadUpdateJournal(config.Root, id)
				recovering := operation.State == updates.Running && readErr == nil && (j.Phase == UpdateClaiming || j.Phase == UpdateClaimed || j.Phase == UpdatePrepared) && operation.ClaimRequestID == j.ClaimID && operation.ClaimedRevision != 0 && operation.DeviceID == j.Operation.DeviceID && operation.ManifestSHA256 == j.Operation.ManifestSHA256
				if recovering {
					j.Operation = operation
					j.OldGeneration = status.Lifecycle.Generation
				} else {
					if operation.State != updates.Waiting || !errors.Is(readErr, os.ErrNotExist) {
						return updateFailure()
					}
					j = UpdateJournal{Version: 1, ID: id, Phase: UpdateClaiming, Operation: operation, ClaimID: domain.NewID(), ReportID: domain.NewID(), ExpectedRevision: r.Revision, OldGeneration: status.Lifecycle.Generation}
					if e = WriteUpdateJournal(config.Root, j); e != nil {
						return e
					}
					claimCtx, stop := context.WithTimeout(ctx, 10*time.Second)
					claimed, problem := client.ClaimWorkerUpdate(claimCtx, authenticated(credential, &pb.ClaimWorkerUpdateRequest{Mutation: &pb.Mutation{Id: r.Id, ExpectedRevision: r.Revision, RequestId: string(j.ClaimID)}, InstanceId: string(instance)}))
					stop()
					if problem != nil {
						if rpc.ClientError(problem).Code == domain.Conflict {
							// A definitive transaction rejection grants no claim.
							// Remove only its pre-dispatch journal and wait for idle.
							if e = os.Remove(updatePath(config.Root, id)); e != nil {
								return e
							}
							select {
							case <-ctx.Done():
								return nil
							case <-ticker.C:
							}
							continue
						}
						config.Logger.WarnContext(ctx, "worker_update_claim_unconfirmed", "operation_id", id, "code", rpc.ClientError(problem).Code)
						return updateFailure()
					}
					if claimed.Msg.Update == nil || domain.Decode(claimed.Msg.Update.DocumentJson, &j.Operation) != nil || j.Operation.State != updates.Running || j.Operation.ClaimRequestID != j.ClaimID || j.Operation.ClaimedInstance != instance {
						return updateFailure()
					}
				}
				j.Phase = UpdateClaimed
				if e = WriteUpdateJournal(config.Root, j); e != nil {
					return e
				}
				release, e := updates.NewClient()
				if e == nil {
					verified, problem := release.VerifyManifest(j.Operation.Manifest, rpc.Version, time.Now().UTC())
					e = problem
					target, problem := updates.SelectTarget(runtime.GOOS, runtime.GOARCH)
					if e == nil && (problem != nil || target != j.Operation.Target || verified.ManifestSHA256 != j.Operation.ManifestSHA256 || verified.Payload.Version != j.Operation.Version) {
						e = updateFailure()
					}
					if e == nil {
						j.ArtifactPath, e = release.Download(ctx, verified, updates.Worker, target, filepath.Join(config.Root, "worker-updates", "bin"))
					}
					release.Close()
				}
				if e == nil {
					e = os.Chmod(j.ArtifactPath, 0700)
				}
				if e != nil {
					j.Outcome = pb.WorkerUpdateOutcome_WORKER_UPDATE_OUTCOME_FAILED
					if problem := reportUpdate(ctx, config.Root, client, credential, instance, &j, rpc.Version); problem != nil {
						return problem
					}
					config.Logger.WarnContext(ctx, "worker_update_download_failed", "operation_id", id, "code", domain.SafeError(e).Code)
					continue
				}
				j.Phase = UpdatePrepared
				if e = WriteUpdateJournal(config.Root, j); e != nil {
					return e
				}
				config.Logger.InfoContext(ctx, "worker_update_drained", "operation_id", id, "version", j.Operation.Version, "target", j.Operation.Target)
				return &UpdateHandoff{ID: id}
			}
		}
		if e != nil && ctx.Err() == nil {
			config.Logger.WarnContext(ctx, "worker_update_poll_failed", "code", rpc.ClientError(e).Code)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}
func reportUpdate(ctx context.Context, root string, client delidevv1connect.InstallationServiceClient, credential Credential, instance domain.ID, j *UpdateJournal, version string) error {
	if j.Outcome == pb.WorkerUpdateOutcome_WORKER_UPDATE_OUTCOME_UNSPECIFIED {
		return updateFailure()
	}
	if e := WriteUpdateJournal(root, *j); e != nil {
		return e
	}
	response, e := client.ReportWorkerUpdate(ctx, authenticated(credential, &pb.ReportWorkerUpdateRequest{Mutation: &pb.Mutation{Id: string(j.ID), ExpectedRevision: j.Operation.ClaimedRevision, RequestId: string(j.ReportID)}, InstanceId: string(instance), Outcome: j.Outcome, InstalledVersion: version}))
	if e != nil {
		return rpc.ClientError(e)
	}
	if response.Msg.Update == nil {
		return updateFailure()
	}
	j.Phase = UpdateComplete
	return WriteUpdateJournal(root, *j)
}

// A new/rollback process reports only the original claim after its authenticated
// attachment. The durable report ID can be replayed without another native effect.
func settleUpdateOnAttach(ctx context.Context, root string, credential Credential, instance domain.ID) error {
	directory := filepath.Join(root, "worker-updates")
	entries, e := os.ReadDir(directory)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil || len(entries) > 4096 {
		return updateFailure()
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".pending-") || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := domain.ID(entry.Name()[:len(entry.Name())-5])
		if id.Validate() != nil {
			return updateFailure()
		}
		j, e := ReadUpdateJournal(root, id)
		if e != nil {
			return e
		}
		if j.Phase != UpdateStarting && j.Phase != UpdateRollback {
			continue
		}
		if j.Operation.ServerID != credential.ServerID || j.Operation.DeviceID != credential.DeviceID || j.Operation.MachineID != credential.MachineID {
			return updateFailure()
		}
		status, e := Status(root)
		if e != nil {
			return e
		}
		expected := j.NewGeneration
		outcome := pb.WorkerUpdateOutcome_WORKER_UPDATE_OUTCOME_SUCCEEDED
		version := j.Operation.Version
		if j.Phase == UpdateRollback {
			expected = j.RollbackGeneration
			outcome = pb.WorkerUpdateOutcome_WORKER_UPDATE_OUTCOME_FAILED
			version = j.Operation.CurrentVersion
		}
		if rpc.Version != version || expected == "" {
			return updateFailure()
		}
		if status.Lifecycle.Generation != expected {
			if j.Phase != UpdateStarting || j.Outcome != pb.WorkerUpdateOutcome_WORKER_UPDATE_OUTCOME_SUCCEEDED {
				return updateFailure()
			}
			h, tr := rpc.HTTPClient()
			client := delidevv1connect.NewInstallationServiceClient(h, credential.Endpoint)
			bounded, stop := context.WithTimeout(ctx, 10*time.Second)
			observed, e := client.PollWorkerUpdate(bounded, authenticated(credential, &pb.PollWorkerUpdateRequest{InstanceId: string(instance), OriginalUpdateId: string(j.ID)}))
			stop()
			tr.CloseIdleConnections()
			var original updates.Operation
			if e != nil || observed.Msg.Update == nil || observed.Msg.Update.Id != string(j.ID) || domain.Decode(observed.Msg.Update.DocumentJson, &original) != nil || original.State != updates.Succeeded || original.ClaimRequestID != j.ClaimID || original.ManifestSHA256 != j.Operation.ManifestSHA256 {
				return updateFailure()
			}
			j.Phase = UpdateComplete
			if e = WriteUpdateJournal(root, j); e != nil {
				return e
			}
			continue
		}
		j.Outcome = outcome
		httpClient, transport := rpc.HTTPClient()
		client := delidevv1connect.NewInstallationServiceClient(httpClient, credential.Endpoint)
		bounded, stop := context.WithTimeout(ctx, 10*time.Second)
		e = reportUpdate(bounded, root, client, credential, instance, &j, version)
		stop()
		transport.CloseIdleConnections()
		if e != nil {
			return e
		}
	}
	return nil
}

func (j UpdateJournal) valid() bool {
	if j.Version != 1 || j.ID.Validate() != nil || j.ClaimID.Validate() != nil || j.ReportID.Validate() != nil || j.OldGeneration.Validate() != nil || j.Operation.ServerID.Validate() != nil || j.Operation.DeviceID.Validate() != nil || j.Operation.MachineID.Validate() != nil || j.Operation.Component != updates.Worker || !j.Operation.Target.Valid() || j.ExpectedRevision == 0 || !slices.Contains([]UpdatePhase{UpdateClaiming, UpdateClaimed, UpdatePrepared, UpdateStarting, UpdateRollback, UpdateComplete, UpdateUnknown}, j.Phase) {
		return false
	}
	for _, id := range []domain.ID{j.NewGeneration, j.RollbackGeneration} {
		if id != "" && id.Validate() != nil {
			return false
		}
	}
	newer, e := updates.Newer(j.Operation.Version, j.Operation.CurrentVersion)
	if e != nil || !newer {
		return false
	}
	if j.Phase != UpdateClaiming && (j.Operation.ClaimRequestID != j.ClaimID || j.Operation.ClaimedRevision == 0 || j.Operation.ClaimedInstance.Validate() != nil) {
		return false
	}
	return len(j.Operation.Manifest) > 0 && len(j.Operation.Manifest) <= updates.ManifestLimit && len(j.Operation.ManifestSHA256) == 64
}

// Future explicit local starts use the highest positively installed generation,
// rather than a stale bundled/server CLI. Every selection reverifies its original
// compiled-root manifest and private immutable bytes; uncertain journals block.
func InstalledExecutable(root, fallback string) (string, error) {
	entries, e := os.ReadDir(filepath.Join(root, "worker-updates"))
	if errors.Is(e, os.ErrNotExist) {
		return fallback, nil
	}
	if e != nil || len(entries) > 4096 {
		return "", updateFailure()
	}
	selected, version := fallback, rpc.Version
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := domain.ID(strings.TrimSuffix(entry.Name(), ".json"))
		j, e := ReadUpdateJournal(root, id)
		if e != nil {
			return "", e
		}
		if j.Phase == UpdateClaiming || j.Phase == UpdateClaimed || j.Phase == UpdatePrepared {
			continue
		}
		reconcile := j.Phase == UpdateStarting && j.Outcome == pb.WorkerUpdateOutcome_WORKER_UPDATE_OUTCOME_SUCCEEDED
		if j.Phase != UpdateComplete && !reconcile {
			return "", updateFailure()
		}
		if j.Outcome != pb.WorkerUpdateOutcome_WORKER_UPDATE_OUTCOME_SUCCEEDED {
			continue
		}
		v, e := updates.NewVerifier()
		if e != nil {
			return "", e
		}
		signed, e := v.Verify(j.Operation.Manifest, j.Operation.CurrentVersion, time.Now().UTC())
		if e != nil || signed.ManifestSHA256 != j.Operation.ManifestSHA256 {
			return "", updateFailure()
		}
		artifact, e := signed.Artifact(updates.Worker, j.Operation.Target)
		if e != nil || updates.VerifyFile(j.ArtifactPath, artifact) != nil {
			return "", updateFailure()
		}
		newer, e := updates.Newer(j.Operation.Version, version)
		if e != nil {
			return "", e
		}
		if !newer {
			continue
		}
		selected, version = j.ArtifactPath, j.Operation.Version
	}
	return selected, nil
}
