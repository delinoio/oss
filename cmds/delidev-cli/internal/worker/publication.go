package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type PublicationConfig struct {
	Root       string
	Credential Credential
	Instance   domain.ID
	Assignment *pb.Resource
	Client     delidevv1connect.WorkerServiceClient
	Logger     *slog.Logger
}

type pendingPublication struct {
	RequestID domain.ID             `json:"request_id"`
	Event     domain.ExecutionEvent `json:"event"`
}

type publicationJournal struct {
	Version          uint32              `json:"version"`
	JobID            domain.ID           `json:"job_id"`
	InstanceID       domain.ID           `json:"instance_id"`
	ServerID         domain.ID           `json:"server_id"`
	DeviceID         domain.ID           `json:"device_id"`
	Revision         uint64              `json:"revision"`
	AssignmentDigest string              `json:"assignment_digest"`
	LastSequence     uint64              `json:"last_sequence"`
	Pending          *pendingPublication `json:"pending,omitempty"`
}

// ExecutionPublisher owns one bounded durable outbox. A lost RPC acknowledgment
// leaves the exact request/event available for ReplayPending; it never causes
// another native prompt, side effect or automatic event substitution.
type ExecutionPublisher struct {
	mu        sync.Mutex
	config    PublicationConfig
	path      string
	state     publicationJournal
	execution domain.ID
	input     domain.ExecutionJobInput
	closed    bool
	release   func() error
}

func publicationUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "Native event publication requires reconciliation.", "Retain its original Worker outbox and replay only the pending event receipt; never resend native input.")
}

func OpenExecutionPublisher(config PublicationConfig) (publisher *ExecutionPublisher, returned error) {
	if config.Assignment == nil || config.Client == nil || config.Assignment.Kind != pb.EntityKind_ENTITY_KIND_JOB || config.Assignment.SchemaVersion != 1 || config.Assignment.Revision == 0 || config.Credential.Type != domain.WorkerDevice {
		return nil, publicationUncertain()
	}
	if err := config.Credential.Validate(); err != nil {
		return nil, err
	}
	if err := config.Instance.Validate(); err != nil {
		return nil, err
	}
	jobID := domain.ID(config.Assignment.Id)
	if err := jobID.Validate(); err != nil {
		return nil, err
	}
	var job domain.Job
	if domain.Decode(config.Assignment.DocumentJson, &job) != nil || job.Validate() != nil || job.State != domain.JobClaimed || job.Type != domain.ExecuteSessionJob || job.MachineID != config.Credential.MachineID || job.InstanceID != config.Instance {
		return nil, publicationUncertain()
	}
	var input domain.ExecutionJobInput
	if domain.Decode(job.Input, &input) != nil || input.Validate() != nil || string(input.SessionID) != config.Assignment.SessionId || input.MachineID != job.MachineID {
		return nil, publicationUncertain()
	}
	directory := filepath.Join(config.Root, "jobs", string(jobID))
	for _, path := range []string{config.Root, filepath.Join(config.Root, "jobs"), directory} {
		if err := security.PrivateDir(path); err != nil {
			return nil, domain.SafeError(err)
		}
	}
	lock, err := security.TryLock(filepath.Join(directory, "publication.lock"))
	if err != nil {
		return nil, err
	}
	defer func() {
		if returned != nil {
			_ = lock.Close()
		}
	}()
	digest := sha256.Sum256(config.Assignment.DocumentJson)
	state := publicationJournal{Version: 1, JobID: jobID, InstanceID: config.Instance, ServerID: config.Credential.ServerID, DeviceID: config.Credential.DeviceID, Revision: config.Assignment.Revision, AssignmentDigest: hex.EncodeToString(digest[:])}
	path := filepath.Join(directory, "publication.json")
	raw, err := security.ReadPrivate(path, 1<<20)
	if err == nil {
		var retained publicationJournal
		if domain.Decode(raw, &retained) != nil || retained.Version != state.Version || retained.JobID != state.JobID || retained.InstanceID != state.InstanceID || retained.ServerID != state.ServerID || retained.DeviceID != state.DeviceID || retained.Revision != state.Revision || retained.AssignmentDigest != state.AssignmentDigest || retained.LastSequence > domain.MaxExecutionEvents {
			return nil, publicationUncertain()
		}
		if p := retained.Pending; p != nil && (p.RequestID.Validate() != nil || p.Event.Validate() != nil || p.Event.ExecutionID != input.ExecutionID || p.Event.Sequence != retained.LastSequence+1) {
			return nil, publicationUncertain()
		}
		state = retained
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, publicationUncertain()
	} else if err := writeJSON(path, state); err != nil {
		return nil, publicationUncertain()
	}
	return &ExecutionPublisher{config: config, path: path, state: state, execution: input.ExecutionID, input: input, release: lock.Close}, nil
}

func (p *ExecutionPublisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return p.release()
}

func (p *ExecutionPublisher) Publish(ctx context.Context, event domain.ExecutionEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.state.Pending != nil {
		return publicationUncertain()
	}
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	if (event.Version != 0 && event.Version != 1) || (event.ExecutionID != "" && event.ExecutionID != p.execution) || (event.Sequence != 0 && event.Sequence != p.state.LastSequence+1) {
		return publicationUncertain()
	}
	event.Version = 1
	event.ExecutionID = p.execution
	event.Sequence = p.state.LastSequence + 1
	if err := event.Validate(); err != nil {
		return err
	}
	// Freeze caller-owned pointers/slices before retaining or sending the event.
	raw, err := json.Marshal(event)
	if err != nil {
		return domain.SafeError(err)
	}
	var frozen domain.ExecutionEvent
	if err := domain.Decode(raw, &frozen); err != nil {
		return err
	}
	state := p.state
	state.Pending = &pendingPublication{RequestID: domain.NewID(), Event: frozen}
	p.state = state
	return p.sendPending(ctx)
}

func (p *ExecutionPublisher) ReplayPending(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return publicationUncertain()
	}
	if p.state.Pending == nil {
		return nil
	}
	return p.sendPending(ctx)
}

func (p *ExecutionPublisher) sendPending(ctx context.Context) error {
	pending := p.state.Pending
	// Retain the pending identity in memory even when synchronization fails.
	// A later explicit retry persists this same envelope before any RPC; it
	// cannot overwrite a possibly committed file with a different event.
	if err := writeJSON(p.path, p.state); err != nil {
		return publicationUncertain()
	}
	raw, err := json.Marshal(pending.Event)
	if err != nil {
		return publicationUncertain()
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	response, err := p.config.Client.PublishExecution(bounded, authenticated(p.config.Credential, &pb.PublishExecutionRequest{Mutation: &pb.Mutation{RequestId: string(pending.RequestID), Id: string(p.state.JobID), ExpectedRevision: p.state.Revision}, MachineId: string(p.config.Credential.MachineID), InstanceId: string(p.state.InstanceID), EventJson: raw}))
	if err != nil || response == nil || response.Msg == nil || response.Msg.AcknowledgedSequence != pending.Event.Sequence {
		code := domain.RecoveryRequired
		if err != nil {
			code = rpc.ClientError(err).Code
		}
		if p.config.Logger != nil {
			p.config.Logger.WarnContext(ctx, "execution_event_publication_uncertain", "job_id", p.state.JobID, "sequence", pending.Event.Sequence, "kind", pending.Event.Kind, "code", code)
		}
		return publicationUncertain()
	}
	state := p.state
	state.LastSequence = pending.Event.Sequence
	state.Pending = nil
	if err := writeJSON(p.path, state); err != nil {
		return publicationUncertain()
	}
	p.state = state
	return nil
}
