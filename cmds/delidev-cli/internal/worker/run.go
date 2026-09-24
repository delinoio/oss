package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type Config struct {
	Root   string
	Logger *slog.Logger
	Ready  func(domain.ID)
}
type journalState string

const (
	journalStarted  journalState = "started"
	journalFinished journalState = "finished"
	journalReported journalState = "reported"
)

type journal struct {
	Version    int             `json:"version"`
	JobID      domain.ID       `json:"job_id"`
	InstanceID domain.ID       `json:"instance_id"`
	Revision   uint64          `json:"revision"`
	Digest     string          `json:"digest"`
	State      journalState    `json:"state"`
	ReportID   domain.ID       `json:"report_id"`
	Output     json.RawMessage `json:"output,omitempty"`
	Problem    *domain.Error   `json:"problem,omitempty"`
}

func authenticated[T any](credential Credential, message *T) *connect.Request[T] {
	r := connect.NewRequest(message)
	r.Header().Set("Authorization", "Bearer "+credential.Token)
	return r
}
func Run(ctx context.Context, config Config) error {
	credential, err := LoadCredential(config.Root)
	if err != nil {
		return err
	}
	if credential.Type != domain.WorkerDevice {
		return domain.Fail(domain.PermissionDenied, "This scope contains a client credential.", "Pair a separate Worker scope.")
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	lock, err := security.TryLock(filepath.Join(config.Root, "worker.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	for _, name := range []string{"jobs", "empty-hooks"} {
		if err := security.PrivateDir(filepath.Join(config.Root, name)); err != nil {
			return err
		}
	}
	httpClient, transport := rpc.HTTPClient()
	defer transport.CloseIdleConnections()
	client := delidevv1connect.NewWorkerServiceClient(httpClient, credential.Endpoint, connect.WithReadMaxBytes(2<<20), connect.WithSendMaxBytes(2<<20))
	instance, attachID := domain.NewID(), domain.NewID()
	backoff := time.Second
	ready := false
	for ctx.Err() == nil {
		attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
		attached, err := client.AttachWorker(attempt, authenticated(credential, &pb.AttachWorkerRequest{RequestId: string(attachID), MachineId: string(credential.MachineID), InstanceId: string(instance), Version: rpc.Version}))
		cancel()
		if err == nil && attached.Msg.ServerId != string(credential.ServerID) {
			return domain.Fail(domain.RecoveryRequired, "The configured server identity changed.", "Inspect the paired endpoint before reconnecting.")
		}
		if err == nil {
			if !ready {
				ready = true
				if config.Ready != nil {
					config.Ready(credential.MachineID)
				}
			}
			config.Logger.InfoContext(ctx, "worker connected", "machine_id", credential.MachineID, "instance_id", instance)
			started := time.Now()
			err = watch(ctx, config, client, credential, instance)
			if time.Since(started) > 30*time.Second {
				backoff = time.Second
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		safe := rpc.ClientError(err)
		var typed *domain.Error
		if errors.As(err, &typed) {
			safe = typed
		}
		if safe.Code == domain.Unauthenticated || safe.Code == domain.PermissionDenied || safe.Code == domain.Unsupported || safe.Code == domain.RecoveryRequired {
			return safe
		}
		config.Logger.WarnContext(ctx, "worker connection interrupted", "machine_id", credential.MachineID, "code", safe.Code, "retry_seconds", backoff.Seconds())
		timer := time.NewTimer(backoff + time.Duration(rand.Int64N(int64(backoff/4)+1)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		backoff = min(15*time.Second, backoff*2)
	}
	return nil
}
func watch(ctx context.Context, config Config, client delidevv1connect.WorkerServiceClient, credential Credential, instance domain.ID) error {
	stream, err := client.WatchWork(ctx, authenticated(credential, &pb.WatchWorkRequest{MachineId: string(credential.MachineID), InstanceId: string(instance)}))
	if err != nil {
		return err
	}
	defer stream.Close()
	for stream.Receive() {
		if stream.Msg().Job == nil {
			if !stream.Msg().Heartbeat {
				return domain.Fail(domain.RecoveryRequired, "The Worker received an invalid stream record.", "Check server protocol compatibility.")
			}
			continue
		}
		resource := stream.Msg().Job
		if resource.Kind != pb.EntityKind_ENTITY_KIND_JOB || resource.SchemaVersion != 1 || resource.Revision == 0 {
			return domain.Fail(domain.RecoveryRequired, "The Worker received an invalid job envelope.", "Check server protocol compatibility.")
		}
		id := domain.ID(resource.Id)
		if err := id.Validate(); err != nil {
			return err
		}
		var job domain.Job
		if err := domain.Decode(resource.DocumentJson, &job); err != nil {
			return err
		}
		if err := job.Validate(); err != nil {
			return err
		}
		if job.MachineID != credential.MachineID || job.InstanceID != instance || job.State != domain.JobClaimed {
			return domain.Fail(domain.PermissionDenied, "The received job belongs to another machine or process.", "Inspect the paired server and job ownership.")
		}
		result, err := runJob(ctx, config.Root, instance, resource, job)
		if err != nil {
			return err
		}
		report := &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(result.ReportID), Id: string(result.JobID), ExpectedRevision: result.Revision}, MachineId: string(credential.MachineID), InstanceId: string(instance), OutputJson: result.Output}
		if result.Problem != nil {
			report.Problem = &pb.ErrorDetail{Code: string(result.Problem.Code)}
		}
		attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err = client.ReportWork(attempt, authenticated(credential, report))
		cancel()
		if err != nil {
			return err
		}
		result.State = journalReported
		if err := writeJSON(filepath.Join(config.Root, "jobs", string(id)+".json"), result); err != nil {
			return err
		}
		config.Logger.InfoContext(ctx, "worker job completed", "machine_id", credential.MachineID, "job_id", id, "type", job.Type, "failed", result.Problem != nil)
	}
	return stream.Err()
}
func runJob(ctx context.Context, root string, instance domain.ID, resource *pb.Resource, job domain.Job) (journal, error) {
	hash := sha256.Sum256(resource.DocumentJson)
	digest := hex.EncodeToString(hash[:])
	path := filepath.Join(root, "jobs", resource.Id+".json")
	result := journal{Version: 1, JobID: domain.ID(resource.Id), InstanceID: instance, Revision: resource.Revision, Digest: digest, State: journalStarted, ReportID: domain.NewID()}
	raw, err := security.ReadPrivate(path, 2<<20)
	if err == nil {
		if err := domain.Decode(raw, &result); err != nil {
			return journal{}, err
		}
		if result.Version != 1 || result.JobID != domain.ID(resource.Id) || result.InstanceID != instance || result.Revision != resource.Revision || result.Digest != digest {
			return journal{}, domain.Fail(domain.RecoveryRequired, "A Worker journal conflicts with the assigned operation.", "Preserve the journal and reconcile the original accepted execution.")
		}
		if err := result.ReportID.Validate(); err != nil {
			return journal{}, err
		}
		if result.State == journalFinished || result.State == journalReported {
			return result, nil
		}
		if result.State != journalStarted {
			return journal{}, domain.Fail(domain.RecoveryRequired, "A Worker journal has an unknown state.", "Preserve it for recovery.")
		}
		result.Problem = domain.Fail(domain.RecoveryRequired, "Execution began without a durable completion record.", "Reconcile the accepted operation before retrying.")
	} else if !errors.Is(err, os.ErrNotExist) {
		return journal{}, err
	} else {
		if err := writeJSON(path, result); err != nil {
			return journal{}, err
		}
		output, err := execute(ctx, root, domain.ID(resource.Id), job)
		if err != nil {
			result.Problem = domain.SafeError(err)
		} else {
			result.Output = output
		}
	}
	result.State = journalFinished
	if err := writeJSON(path, result); err != nil {
		return journal{}, err
	}
	return result, nil
}
func execute(ctx context.Context, root string, owner domain.ID, job domain.Job) (json.RawMessage, error) {
	switch job.Type {
	case domain.InspectRepositoryJob:
		var input domain.RepositoryInspectionInput
		if err := domain.Decode(job.Input, &input); err != nil {
			return nil, err
		}
		git := workspace.Git{HooksDir: filepath.Join(root, "empty-hooks"), ProcessRoot: filepath.Join(root, "processes"), OwnerID: owner}
		inspection, err := git.Inspect(ctx, input.Path)
		if err != nil {
			return nil, err
		}
		remotes := append([]string{}, input.RequiredRemotes...)
		if input.PreferredRemote != "" {
			remotes = append(remotes, input.PreferredRemote)
		}
		for _, remote := range remotes {
			if !slices.Contains(inspection.Remotes, remote) {
				return nil, domain.Fail(domain.InvalidArgument, "The configured remote is missing on this Worker.", "Refresh inspection and select an existing remote.")
			}
		}
		return json.Marshal(inspection)
	default:
		return nil, domain.Fail(domain.Unsupported, "This Worker cannot execute the requested operation.", "Use a compatible supported Worker operation.")
	}
}
