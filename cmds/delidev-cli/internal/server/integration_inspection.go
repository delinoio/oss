package server

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type integrationCheck struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Called under the gate. Completion closes before map cleanup takes the gate,
// so cancellation can join HTTP and token clearing without a lock inversion.
func (s *Service) stopIntegrationCheck(ctx context.Context, id domain.ID) error {
	check := s.integrationChecks[id]
	if check == nil {
		return nil
	}
	check.cancel()
	select {
	case <-check.done:
		delete(s.integrationChecks, id)
		return nil
	case <-ctx.Done():
		return domain.SafeError(ctx.Err())
	case <-time.After(5 * time.Second):
		return domain.Fail(domain.Unavailable, "The previous GitHub request is still stopping.", "Retry the original profile operation after its request completes.")
	}
}

func (s *Service) inspectIntegration(ctx context.Context, meta *pb.Mutation, generation domain.ID, correlation string) (store.Result, error) {
	input, err := integrationMutation(ctx, meta, false)
	if err != nil {
		return store.Result{}, err
	}
	input.GenerationID = generation
	unlock, err := s.lockIntegrations(ctx)
	if err != nil {
		return store.Result{}, err
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	result, replayed, err := s.Store.Replay(ctx, domain.ID(meta.RequestId), "integration.validate", input)
	if err != nil || replayed {
		return result, err
	}
	var original domain.Integration
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, value, e := integrationFromTx(tx, input.ID, input.Revision)
		if e != nil {
			return e
		}
		if value.Connection == nil || value.Pending != nil || (generation != "" && value.Connection.GenerationID != generation) {
			return domain.Fail(domain.Conflict, "No matching active token generation is available.", "Complete the profile's pending change and validate its current connection.")
		}
		original = value
		return nil
	})
	if err != nil {
		return store.Result{}, err
	}
	if s.integrationChecks[input.ID] != nil {
		return store.Result{}, domain.Fail(domain.Conflict, "This profile already has a running validation.", "Wait for it to finish before validating again.")
	}
	if len(s.integrationChecks) >= 8 {
		return store.Result{}, domain.Fail(domain.ResourceExhausted, "The GitHub validation limit is reached.", "Retry after another profile's validation finishes.")
	}
	if s.integrationChecks == nil {
		s.integrationChecks = map[domain.ID]*integrationCheck{}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	check := &integrationCheck{cancel: cancel, done: make(chan struct{})}
	s.integrationChecks[input.ID] = check
	defer func() {
		cancel()
		close(check.done)
		if !locked {
			unlock, _ = s.lockIntegrations(context.Background())
			locked = true
		}
		if s.integrationChecks[input.ID] == check {
			delete(s.integrationChecks, input.ID)
		}
	}()
	if s.github == nil {
		s.github = gh.New(s.outboundResolver())
	}
	client := s.github
	validation := domain.IntegrationValidation{GenerationID: original.Connection.GenerationID, State: domain.IntegrationUnavailable}
	vault, credentialErr := s.patSecrets()
	var token []byte
	if credentialErr == nil {
		token, credentialErr = vault.Get(checkCtx, credentials.PATRef{ProfileID: input.ID, GenerationID: validation.GenerationID})
	}
	defer clear(token)
	unlock()
	locked = false
	s.logger.Info("integration_validation_started", "profile_id", input.ID, "request_id", meta.RequestId, "correlation_id", correlation)
	if credentialErr != nil {
		validation.Problem = domain.SafeError(credentialErr)
	} else {
		observation, e := client.Identity(checkCtx, token)
		if e != nil {
			validation.Problem = domain.SafeError(e)
		} else {
			validation.State, validation.Identity, validation.Problem = observation.State, observation.Identity, observation.Problem
		}
	}
	clear(token)
	if checkCtx.Err() != nil {
		s.logger.Info("integration_validation_canceled", "profile_id", input.ID, "request_id", meta.RequestId, "correlation_id", correlation)
		return store.Result{}, domain.SafeError(checkCtx.Err())
	}
	validation.CheckedAt = time.Now().UTC().Truncate(time.Millisecond)
	if validation.Problem != nil {
		problem := *validation.Problem
		problem.CorrelationID = correlation
		validation.Problem = &problem
	}
	if err = validation.Validate(); err != nil {
		return store.Result{}, err
	}
	unlock, err = s.lockIntegrations(checkCtx)
	if err != nil {
		return store.Result{}, err
	}
	locked = true
	result, err = s.Store.Mutate(checkCtx, domain.ID(meta.RequestId), "integration.validate", input, func(tx *store.Tx) (any, error) {
		record, current, err := integrationFromTx(tx, input.ID, input.Revision)
		if err != nil {
			return nil, err
		}
		if current.Connection == nil || current.Pending != nil || current.Connection.GenerationID != validation.GenerationID {
			return nil, domain.Fail(domain.Conflict, "The validated token generation changed.", "Validate the current profile explicitly.")
		}
		current.Connection.Validation = &validation
		if _, err = tx.Put(domain.IntegrationKind, input.ID, record.Revision, "", "", current); err != nil {
			return nil, err
		}
		return integrationReceipt{ID: input.ID}, nil
	})
	var code domain.Code
	if err != nil {
		code = domain.SafeError(err).Code
	} else if validation.Problem != nil {
		code = validation.Problem.Code
	}
	s.logger.Info("integration_validation_finished", "profile_id", input.ID, "request_id", meta.RequestId, "state", validation.State, "error_code", code, "correlation_id", correlation)
	return result, err
}
func (s *Service) ValidateIntegrationProfile(ctx context.Context, req *connect.Request[pb.ValidateIntegrationProfileRequest]) (*connect.Response[pb.ValidateIntegrationProfileResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	result, err := s.inspectIntegration(ctx, req.Msg.Mutation, "", correlation)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	record, value, err := s.integrationRecord(ctx, domain.ID(req.Msg.Mutation.Id))
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var problem error
	if value.Connection != nil && value.Connection.Validation != nil && value.Connection.Validation.Problem != nil {
		problem = value.Connection.Validation.Problem
	}
	response := connect.NewResponse(&pb.ValidateIntegrationProfileResponse{Profile: rpc.Resource(record), RequestId: req.Msg.Mutation.RequestId, Replayed: result.Replayed, ProblemJson: integrationProblem(problem, correlation)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
