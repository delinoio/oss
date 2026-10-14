package server

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type repositoryIntegrationSelection struct {
	record     store.Record
	repository domain.Repository
	profile    domain.Integration
}

func repositoryIntegrationFromTx(tx *store.Tx, id domain.ID) (repositoryIntegrationSelection, error) {
	if err := tx.Authorize(); err != nil {
		return repositoryIntegrationSelection{}, err
	}
	record, err := tx.Get(domain.RepositoryKind, id)
	if err != nil {
		return repositoryIntegrationSelection{}, err
	}
	repository, err := store.Decode[domain.Repository](record)
	if err != nil {
		return repositoryIntegrationSelection{}, err
	}
	if err = domain.ValidateGitHubRepository(repository.GitHubOwner, repository.GitHubName); err != nil {
		return repositoryIntegrationSelection{}, err
	}
	if repository.IntegrationID == "" {
		return repositoryIntegrationSelection{}, domain.Fail(domain.MissingInput, "The repository has no selected GitHub profile.", "Choose its profile explicitly in repository settings; no other token or system credential is used.")
	}
	_, profile, err := integrationFromTx(tx, repository.IntegrationID, 0)
	if err != nil {
		return repositoryIntegrationSelection{}, err
	}
	if !profile.AllowsGitHubOwner(repository.GitHubOwner) {
		return repositoryIntegrationSelection{}, domain.Fail(domain.PermissionDenied, "The selected profile targets another resource owner.", "Select a separate profile for this repository's owner.")
	}
	if profile.Connection == nil || profile.Pending != nil {
		return repositoryIntegrationSelection{}, domain.Fail(domain.Conflict, "The selected GitHub profile has no usable token generation.", "Complete its connection or pending cleanup before reading GitHub items.")
	}
	return repositoryIntegrationSelection{record: record, repository: repository, profile: profile}, nil
}

// errIntegrationPreempted is returned by one admitted attempt that a foreground
// read displaced. It never leaves the server package: only the resumable
// wrapper consumes it, and only after the attempt has joined (token cleared and
// completion signalled).
var errIntegrationPreempted = errors.New("integration read attempt preempted by a foreground read")

const (
	integrationQueueBound  = 35 * time.Second
	integrationActiveBound = 35 * time.Second
)

// integrationResume freezes the scope and the active deadline of a resumable
// read at its first activation so every later attempt and queue wait shares
// them. A nil value selects the ordinary single-attempt behavior.
type integrationResume struct {
	initialized    bool
	scope          repositoryIntegrationSelection
	activeDeadline time.Time
	// A foreground join failure is terminal even if the interrupted attempt
	// reported its typed cancellation before the foreground caller failed.
	failedDisplacement atomic.Bool
}

var errIntegrationDisplacementFailed = domain.Fail(domain.Unavailable, "The foreground GitHub read did not complete displacement.", "Retry the workspace read explicitly after the previous request completes.")

func (s *Service) withRepositoryIntegration(ctx context.Context, id domain.ID, operation, correlation string, read func(context.Context, []byte, repositoryIntegrationSelection) error) (repositoryIntegrationSelection, error) {
	return s.admitRepositoryIntegration(ctx, id, operation, correlation, 0, nil, read)
}

func (s *Service) admitRepositoryIntegration(ctx context.Context, id domain.ID, operation, correlation string, attempt int, resume *integrationResume, read func(context.Context, []byte, repositoryIntegrationSelection) error) (repositoryIntegrationSelection, error) {
	if _, err := integrationActor(ctx); err != nil {
		return repositoryIntegrationSelection{}, err
	}
	if err := id.Validate(); err != nil {
		return repositoryIntegrationSelection{}, err
	}
	unlock, err := s.lockIntegrations(ctx)
	if err != nil {
		return repositoryIntegrationSelection{}, err
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	if resume != nil && resume.failedDisplacement.Load() {
		return repositoryIntegrationSelection{}, errIntegrationDisplacementFailed
	}
	var selected repositoryIntegrationSelection
	err = s.Store.Read(ctx, func(tx *store.Tx) error { var e error; selected, e = repositoryIntegrationFromTx(tx, id); return e })
	if err != nil {
		return repositoryIntegrationSelection{}, err
	}
	profileID := selected.repository.IntegrationID
	if resume != nil && resume.initialized && (selected.record.Revision != resume.scope.record.Revision || profileID != resume.scope.repository.IntegrationID || selected.profile.Connection.GenerationID != resume.scope.profile.Connection.GenerationID) {
		return repositoryIntegrationSelection{}, domain.Fail(domain.Conflict, "The suspended repository read scope changed.", "Reload the exact repository before retrying this read.")
	}
	background := operation == "workspace" || operation == "avatar"
	if s.integrationQueued == nil {
		s.integrationQueued = map[domain.ID]int{}
		s.integrationForeground = map[domain.ID]int{}
	}
	queued := false
	defer func() {
		if queued {
			if !locked {
				unlock, _ = s.lockIntegrations(context.Background())
				locked = true
			}
			s.integrationQueued[profileID]--
			if !background {
				s.integrationForeground[profileID]--
			}
		}
	}()
	var displaced *integrationCheck
	foregroundReadStarted := false
	defer func() {
		// A completed join is insufficient if this caller exits before its read.
		// Keep replay denied before removing its pending foreground priority.
		if displaced != nil && !foregroundReadStarted && displaced.replayFailed != nil {
			displaced.replayFailed.Store(true)
		}
	}()
	// A resumed attempt shares the frozen active deadline for its queue wait; only
	// first admission receives the 35-second queue bound.
	var waitCtx context.Context
	var waitCancel context.CancelFunc
	if resume != nil && resume.initialized {
		waitCtx, waitCancel = context.WithDeadline(ctx, resume.activeDeadline)
	} else {
		waitCtx, waitCancel = context.WithTimeout(ctx, integrationQueueBound)
	}
	defer waitCancel()
	if err := waitCtx.Err(); err != nil && resume != nil && resume.initialized {
		return repositoryIntegrationSelection{}, domain.SafeError(err)
	}
	for {
		running := s.integrationChecks[profileID]
		if running == nil && (!background || s.integrationForeground[profileID] == 0) {
			break
		}
		if !queued {
			if s.integrationQueued[profileID] >= 64 {
				return repositoryIntegrationSelection{}, domain.Fail(domain.ResourceExhausted, "The profile read queue is full.", "Retry this read explicitly after foreground work completes.")
			}
			s.integrationQueued[profileID]++
			if !background {
				s.integrationForeground[profileID]++
			}
			queued = true
		}
		if !background && running != nil && running.background {
			if err := s.displaceIntegrationCheck(waitCtx, profileID); err != nil {
				return repositoryIntegrationSelection{}, err
			}
			displaced = running
			continue
		}
		var done <-chan struct{}
		if running != nil {
			done = running.done
		}
		unlock()
		locked = false
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-done:
		case <-timer.C:
		case <-waitCtx.Done():
			timer.Stop()
			return repositoryIntegrationSelection{}, domain.SafeError(waitCtx.Err())
		}
		timer.Stop()
		unlock, err = s.lockIntegrations(waitCtx)
		if err != nil {
			return repositoryIntegrationSelection{}, err
		}
		locked = true
		var current repositoryIntegrationSelection
		err = s.Store.Read(waitCtx, func(tx *store.Tx) error { var e error; current, e = repositoryIntegrationFromTx(tx, id); return e })
		if err != nil {
			return repositoryIntegrationSelection{}, err
		}
		if current.record.Revision != selected.record.Revision || current.repository.IntegrationID != profileID || current.profile.Connection.GenerationID != selected.profile.Connection.GenerationID {
			return repositoryIntegrationSelection{}, domain.Fail(domain.Conflict, "The queued repository read scope changed.", "Reload the exact repository before retrying this read.")
		}
	}
	if resume != nil && resume.initialized && s.stopping.Load() {
		return repositoryIntegrationSelection{}, domain.SafeError(context.Canceled)
	}
	if resume != nil && resume.failedDisplacement.Load() {
		return repositoryIntegrationSelection{}, errIntegrationDisplacementFailed
	}
	if queued {
		s.integrationQueued[profileID]--
		if !background {
			s.integrationForeground[profileID]--
		}
		queued = false
	}
	if len(s.integrationChecks)+len(s.integrationPreviews) >= 8 {
		return repositoryIntegrationSelection{}, domain.Fail(domain.ResourceExhausted, "The server's GitHub inspection limit is reached.", "Retry after an active inspection completes.")
	}
	if s.integrationChecks == nil {
		s.integrationChecks = map[domain.ID]*integrationCheck{}
	}
	activeDeadline := time.Now().Add(integrationActiveBound)
	if resume != nil {
		if resume.initialized {
			activeDeadline = resume.activeDeadline
		} else {
			resume.initialized, resume.scope, resume.activeDeadline = true, selected, activeDeadline
		}
	}
	causeCtx, cancelCause := context.WithCancelCause(ctx)
	checkCtx, cancelDeadline := context.WithDeadline(causeCtx, activeDeadline)
	cancel := func() {
		cancelCause(context.Canceled)
		cancelDeadline()
	}
	check := &integrationCheck{cancel: cancel, preempt: func() { cancelCause(errForegroundPreempted) }, done: make(chan struct{}), background: background}
	if resume != nil {
		check.replayFailed = &resume.failedDisplacement
	}
	// preempted is true only for the typed scheduler displacement while the
	// parent request, the server and the frozen deadline all remain live.
	preempted := func() bool {
		return resume != nil && !resume.failedDisplacement.Load() && checkCtx.Err() != nil && ctx.Err() == nil && !s.stopping.Load() && errors.Is(context.Cause(checkCtx), errForegroundPreempted)
	}
	s.integrationChecks[profileID] = check
	defer func() {
		cancel()
		close(check.done)
		if !locked {
			unlock, _ = s.lockIntegrations(context.Background())
			locked = true
		}
		if s.integrationChecks[profileID] == check {
			delete(s.integrationChecks, profileID)
		}
	}()
	vault, err := s.patSecrets()
	if err != nil {
		return repositoryIntegrationSelection{}, err
	}
	generation := selected.profile.Connection.GenerationID
	token, err := vault.Get(checkCtx, credentials.PATRef{ProfileID: profileID, GenerationID: generation})
	if err != nil {
		return repositoryIntegrationSelection{}, err
	}
	defer clear(token)
	unlock()
	locked = false
	s.logger.Info("repository_integration_read_started", "operation", operation, "repository_id", id, "profile_id", profileID, "correlation_id", correlation)
	foregroundReadStarted = true
	err = read(checkCtx, token, selected)
	clear(token)
	if preempted() {
		s.logger.Info("repository_integration_read_preempted", "operation", operation, "repository_id", id, "profile_id", profileID, "attempt_count", attempt, "correlation_id", correlation)
		return repositoryIntegrationSelection{}, errIntegrationPreempted
	}
	if checkCtx.Err() != nil {
		err = domain.SafeError(checkCtx.Err())
	}
	if err != nil {
		s.logger.Warn("repository_integration_read_failed", "operation", operation, "repository_id", id, "profile_id", profileID, "error_code", domain.SafeError(err).Code, "correlation_id", correlation)
		return repositoryIntegrationSelection{}, err
	}
	unlock, err = s.lockIntegrations(checkCtx)
	if err != nil {
		if preempted() {
			s.logger.Info("repository_integration_read_preempted", "operation", operation, "repository_id", id, "profile_id", profileID, "attempt_count", attempt, "correlation_id", correlation)
			return repositoryIntegrationSelection{}, errIntegrationPreempted
		}
		return repositoryIntegrationSelection{}, err
	}
	locked = true
	err = s.Store.Read(checkCtx, func(tx *store.Tx) error {
		current, e := repositoryIntegrationFromTx(tx, id)
		if e != nil {
			return e
		}
		if current.record.Revision != selected.record.Revision || current.repository.IntegrationID != profileID || current.profile.Connection.GenerationID != generation {
			return domain.Fail(domain.Conflict, "The inspected repository or token generation changed.", "Inspect the current repository selection explicitly.")
		}
		return nil
	})
	if err != nil && preempted() {
		s.logger.Info("repository_integration_read_preempted", "operation", operation, "repository_id", id, "profile_id", profileID, "attempt_count", attempt, "correlation_id", correlation)
		return repositoryIntegrationSelection{}, errIntegrationPreempted
	}
	if err != nil {
		s.logger.Warn("repository_integration_read_rejected", "operation", operation, "repository_id", id, "profile_id", profileID, "error_code", domain.SafeError(err).Code, "correlation_id", correlation)
		return repositoryIntegrationSelection{}, err
	}
	s.logger.Info("repository_integration_read_finished", "operation", operation, "repository_id", id, "profile_id", profileID, "correlation_id", correlation)
	return selected, nil
}

// InspectRepositoryIntegration retains the allocated RPC for older clients.
// Authorization precedes retirement; no store, credential or outbound read is admitted.
func (s *Service) InspectRepositoryIntegration(ctx context.Context, req *connect.Request[pb.InspectRepositoryIntegrationRequest]) (*connect.Response[pb.InspectRepositoryIntegrationResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := integrationActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	return nil, rpc.Error(domain.Fail(domain.Unsupported, "Standalone GitHub repository access inspection has been retired.", "Use GitHub item operations; each operation validates its own access."), correlation)
}
