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
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
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
	return watchWithTimeout(ctx, config, client, credential, instance, domain.WorkerConnectionTimeout)
}

func watchWithTimeout(ctx context.Context, config Config, client delidevv1connect.WorkerServiceClient, credential Credential, instance domain.ID, heartbeatTimeout time.Duration) error {
	ctx, cancel := context.WithCancelCause(ctx)
	// TCP can remain half-open after connectivity is lost. The server emits a
	// heartbeat every ten seconds; silence past its lease is authority loss too.
	deadline := time.AfterFunc(heartbeatTimeout, func() {
		cancel(domain.Fail(domain.Unavailable, "The Worker connection heartbeat expired.", "Reconnect and reconcile the accepted operation before further execution."))
	})
	defer deadline.Stop()
	stream, err := client.WatchWork(ctx, authenticated(credential, &pb.WatchWorkRequest{MachineId: string(credential.MachineID), InstanceId: string(instance)}))
	if err != nil {
		if ctx.Err() != nil {
			err = context.Cause(ctx)
		}
		cancel(err)
		return err
	}
	type assignment struct {
		resource *pb.Resource
		context  context.Context
		cancel   context.CancelFunc
	}
	jobs := make(chan assignment, 1)
	var active sync.Map
	received := make(chan struct{})
	// Receive independently of native execution. Revocation, stream replacement
	// and server loss must cancel owned Git/harness work while it is running.
	// The server sends one unresolved assignment per stream; overflow is a
	// protocol failure, never permission to buffer arbitrary future execution.
	go func() {
		defer close(received)
		for stream.Receive() {
			deadline.Reset(heartbeatTimeout)
			message := stream.Msg()
			if message.CancelJobId != "" {
				if message.Job != nil || message.Heartbeat || message.CancelRequested || domain.ID(message.CancelJobId).Validate() != nil {
					cancel(domain.Fail(domain.RecoveryRequired, "The Worker received an invalid cancellation control.", "Check server protocol compatibility."))
					return
				}
				if stop, ok := active.Load(message.CancelJobId); ok {
					stop.(context.CancelFunc)()
				}
				continue
			}
			if message.Job == nil {
				if !message.Heartbeat || message.CancelRequested {
					cancel(domain.Fail(domain.RecoveryRequired, "The Worker received an invalid stream record.", "Check server protocol compatibility."))
					return
				}
				continue
			}
			if message.Heartbeat {
				cancel(domain.Fail(domain.RecoveryRequired, "The Worker received an ambiguous assignment.", "Check server protocol compatibility."))
				return
			}
			resource := proto.Clone(message.Job).(*pb.Resource)
			if domain.ID(resource.Id).Validate() != nil {
				cancel(domain.Fail(domain.RecoveryRequired, "The Worker received an invalid assignment identity.", "Check server protocol compatibility."))
				return
			}
			jobContext, stopJob := context.WithCancel(ctx)
			if _, loaded := active.LoadOrStore(resource.Id, stopJob); loaded {
				stopJob()
				cancel(domain.Fail(domain.RecoveryRequired, "The Worker received a duplicate live assignment.", "Reconcile its original operation before another send."))
				return
			}
			if message.CancelRequested {
				stopJob()
			}
			select {
			case jobs <- assignment{resource: resource, context: jobContext, cancel: stopJob}:
			case <-ctx.Done():
				return
			default:
				cancel(domain.Fail(domain.ResourceExhausted, "The server exceeded the Worker assignment window.", "Reconcile accepted jobs before reconnecting."))
				return
			}
		}
		err := stream.Err()
		if err == nil {
			err = domain.Fail(domain.Unavailable, "The Worker work stream ended.", "Reconnect and reconcile the accepted operation.")
		}
		cancel(err)
	}()
	defer func() { cancel(context.Canceled); _ = stream.Close(); <-received }()
	for {
		var work assignment
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case work = <-jobs:
		}
		resource := work.resource
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
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
		result, err := runJob(work.context, config, instance, resource, job)
		work.cancel()
		active.Delete(resource.Id)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		report := &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(result.ReportID), Id: string(result.JobID), ExpectedRevision: result.Revision}, MachineId: string(credential.MachineID), InstanceId: string(instance), OutputJson: result.Output}
		if result.Problem != nil {
			report.Problem = &pb.ErrorDetail{Code: string(result.Problem.Code)}
		}
		attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err = client.ReportWork(attempt, authenticated(credential, report))
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return err
		}
		result.State = journalReported
		if err := writeJSON(filepath.Join(config.Root, "jobs", string(id)+".json"), result); err != nil {
			return err
		}
		config.Logger.InfoContext(ctx, "worker job completed", "machine_id", credential.MachineID, "job_id", id, "type", job.Type, "failed", result.Problem != nil)
	}
}
func runJob(ctx context.Context, config Config, instance domain.ID, resource *pb.Resource, job domain.Job) (journal, error) {
	if job.Type == domain.PrepareWorkspaceJob {
		var input workspace.PrepareRequest
		if err := domain.Decode(job.Input, &input); err != nil {
			return journal{}, err
		}
		if string(input.SessionID) != resource.SessionId || input.MachineID != job.MachineID {
			return journal{}, domain.Fail(domain.RecoveryRequired, "The workspace assignment has inconsistent ownership.", "Reconcile the accepted session and job before executing work.")
		}
	}
	if job.Type == domain.RecoverWorkspaceJob {
		var input workspace.RecoveryRequest
		if err := domain.Decode(job.Input, &input); err != nil {
			return journal{}, err
		}
		if string(input.Preparation.SessionID) != resource.SessionId || input.Preparation.MachineID != job.MachineID || input.JobID != job.ParentID {
			return journal{}, workspace.ResultUncertain()
		}
	}
	root := config.Root
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
		if err := ctx.Err(); err != nil {
			result.Problem = domain.SafeError(err)
		} else {
			output, err := execute(ctx, config, domain.ID(resource.Id), job)
			if err != nil {
				result.Problem = domain.SafeError(err)
			} else {
				result.Output = output
			}
		}
	}
	result.State = journalFinished
	if err := writeJSON(path, result); err != nil {
		return journal{}, err
	}
	return result, nil
}
func execute(ctx context.Context, config Config, owner domain.ID, job domain.Job) (json.RawMessage, error) {
	root := config.Root
	switch job.Type {
	case domain.RecoverWorkspaceJob:
		bounded, stopRecovery := context.WithTimeout(ctx, 2*time.Minute)
		defer stopRecovery()
		ctx = bounded
		var input workspace.RecoveryRequest
		if err := domain.Decode(job.Input, &input); err != nil {
			return nil, err
		}
		if err := input.Validate(); err != nil {
			return nil, err
		}
		raw, err := security.ReadPrivate(filepath.Join(root, "jobs", string(input.JobID)+".json"), 2<<20)
		if err != nil {
			return nil, workspace.ResultUncertain()
		}
		var prior journal
		if domain.Decode(raw, &prior) != nil || prior.Version != 1 || prior.JobID != input.JobID || prior.InstanceID != input.InstanceID || prior.Revision != input.Revision || prior.Digest != input.AssignmentDigest || prior.ReportID.Validate() != nil {
			return nil, workspace.ResultUncertain()
		}
		if prior.State != journalStarted && prior.State != journalFinished && prior.State != journalReported {
			return nil, workspace.ResultUncertain()
		}
		completedClean := false
		if prior.State == journalStarted {
			if prior.Problem != nil || len(prior.Output) != 0 {
				return nil, workspace.ResultUncertain()
			}
		} else if prior.Problem != nil {
			if len(prior.Output) != 0 {
				return nil, workspace.ResultUncertain()
			}
			switch prior.Problem.Code {
			case domain.InvalidArgument, domain.NotFound, domain.Conflict, domain.PermissionDenied, domain.Unavailable, domain.MissingInput, domain.Unsupported, domain.ResourceExhausted, domain.Canceled, domain.Internal:
				completedClean = true
			case domain.RecoveryRequired:
			default:
				return nil, workspace.ResultUncertain()
			}
		} else if len(prior.Output) == 0 {
			return nil, workspace.ResultUncertain()
		}
		manager := workspace.Manager{Root: root, Logger: config.Logger}
		result, err := manager.Recover(ctx, input, completedClean)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case domain.PrepareWorkspaceJob:
		var input workspace.PrepareRequest
		if err := domain.Decode(job.Input, &input); err != nil {
			return nil, err
		}
		if input.MachineID != job.MachineID {
			return nil, domain.Fail(domain.PermissionDenied, "Workspace preparation targets another machine.", "Reconcile the accepted assignment before retrying.")
		}
		manager := workspace.Manager{Root: root, Logger: config.Logger}
		manifest, err := manager.Prepare(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(manifest)
	case domain.HarnessDiscoveryJob:
		var input domain.HarnessDiscoveryInput
		if err := domain.Decode(job.Input, &input); err != nil {
			return nil, err
		}
		result, err := harness.Discover(ctx, harness.DiscoveryConfig{Root: root, OwnerID: owner, Logger: config.Logger}, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case domain.InspectRepositoryJob:
		var input domain.RepositoryInspectionInput
		if err := domain.Decode(job.Input, &input); err != nil {
			return nil, err
		}
		git := workspace.Git{HooksDir: filepath.Join(root, "empty-hooks"), ProcessRoot: filepath.Join(root, "processes"), OwnerID: owner, Logger: config.Logger}
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
