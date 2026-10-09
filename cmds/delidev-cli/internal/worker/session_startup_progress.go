// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type sessionStartupReporter struct {
	mu                 sync.Mutex
	sequence           uint64
	closed             bool
	queue              chan domain.StartupProgressStep
	cancel             context.CancelFunc
	done               chan struct{}
	session, execution domain.ID
	config             PublicationConfig
}

func newSessionStartupReporter(ctx context.Context, config Config, job domain.Job) *sessionStartupReporter {
	if !config.startupProgress || config.execution == nil || config.execution.Client == nil || config.execution.Assignment == nil || job.Type != domain.PrepareWorkspaceJob && job.Type != domain.ExecuteSessionJob {
		return nil
	}
	c := *config.execution
	r := &sessionStartupReporter{queue: make(chan domain.StartupProgressStep, 806), done: make(chan struct{}), session: domain.ID(c.Assignment.SessionId), config: c}
	if job.Type == domain.ExecuteSessionJob {
		var input domain.ExecutionJobInput
		if domain.Decode(job.Input, &input) != nil || input.Version != 4 {
			return nil
		}
		r.execution = input.ExecutionID
	}
	bounded, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	go r.run(bounded)
	return r
}
func (r *sessionStartupReporter) observe(step domain.StartupProgressStep) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Fixed operation set and at most 100 repositories. Telemetry loss cannot
	// backpressure Git, native input, cleanup or any authoritative publication.
	if r.closed || r.sequence >= 806 {
		return
	}
	r.sequence++
	step.Sequence = r.sequence
	select {
	case r.queue <- step:
	default:
	}
}
func (r *sessionStartupReporter) native(phase domain.ExecutionStartupPhase, state domain.StartupProgressState) {
	r.observe(domain.StartupProgressStep{NativePhase: phase, State: state})
}
func (r *sessionStartupReporter) close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.queue)
	}
	r.mu.Unlock()
	// Close callback admission first, then drain already accepted metadata within
	// one aggregate reporting bound. This never renews the authoritative job.
	timer := time.NewTimer(1500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-r.done:
	case <-timer.C:
		r.cancel()
		<-r.done
	}
	r.cancel()
}
func (r *sessionStartupReporter) run(ctx context.Context) {
	defer close(r.done)
	for {
		select {
		case <-ctx.Done():
			return
		case step, ok := <-r.queue:
			if !ok {
				return
			}
			c := r.config
			q := &pb.ReportSessionStartupProgressRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: c.Assignment.Id, ExpectedRevision: c.Assignment.Revision}, MachineId: string(c.Credential.MachineID), InstanceId: string(c.Instance), SessionId: string(r.session), ExecutionId: string(r.execution), Sequence: step.Sequence, WorkspaceOperation: pb.SessionStartupWorkspaceOperation(step.WorkspaceOperation), NativePhase: pb.ExecutionStartupPhase(step.NativePhase), State: pb.SessionStartupProgressState(step.State), RepositoryId: string(step.RepositoryID), RepositoryOrdinal: step.RepositoryOrdinal, RepositoryCount: step.RepositoryCount}
			// One bounded transmission and one exact receipt retry. Never retry an
			// operation or renew a lease after a descriptive report failure.
			call, stop := context.WithTimeout(ctx, 1500*time.Millisecond)
			_, err := c.Client.ReportSessionStartupProgress(call, authenticated(c.Credential, q))
			if err != nil && call.Err() == nil {
				_, err = c.Client.ReportSessionStartupProgress(call, authenticated(c.Credential, q))
			}
			stop()
			if err != nil && c.Logger != nil {
				c.Logger.Debug("session_startup_progress_unavailable", "job_id", c.Assignment.Id, "sequence", step.Sequence, "workspace_operation", step.WorkspaceOperation, "native_phase", step.NativePhase, "code", domain.SafeError(err).Code)
			}
		}
	}
}
