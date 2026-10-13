// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// workspaceOutcome is the stable final outcome label of a workspace request.
type workspaceOutcome string

const (
	workspaceOutcomeSucceeded workspaceOutcome = "succeeded"
	workspaceOutcomeFailed    workspaceOutcome = "failed"
)

// readResumablePullRequestWorkspace keeps one workspace request pending across
// foreground displacement. Only an attempt that ended with the typed scheduler
// displacement cause is requeued; every other outcome (provider failure,
// malformed data, parent cancellation, shutdown, changed or revoked scope,
// exhausted limits) is final. Each attempt restarts the complete observation,
// so the read callback must discard any state of earlier attempts. The returned
// count is the number of admissions made for the original request.
func (s *Service) readResumablePullRequestWorkspace(ctx context.Context, id domain.ID, correlation string, read func(context.Context, []byte, repositoryIntegrationSelection) error) (repositoryIntegrationSelection, int, error) {
	resume := &integrationResume{}
	attempts := 0
	for {
		attempts++
		if attempts > 1 {
			s.logger.Info("pull_request_workspace_resumed", "repository_id", id, "attempt_count", attempts, "correlation_id", correlation)
		}
		selected, err := s.admitRepositoryIntegration(ctx, id, "workspace", correlation, resume, read)
		if !errors.Is(err, errIntegrationPreempted) {
			outcome, code := workspaceOutcomeSucceeded, domain.Code("")
			if err != nil {
				outcome, code = workspaceOutcomeFailed, domain.SafeError(err).Code
			}
			s.logger.Info("pull_request_workspace_finished", "repository_id", id, "outcome", outcome, "error_code", code, "attempt_count", attempts, "correlation_id", correlation)
			return selected, attempts, err
		}
		s.logger.Info("pull_request_workspace_requeued", "repository_id", id, "attempt_count", attempts, "correlation_id", correlation)
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = domain.SafeError(ctxErr)
		} else if s.stopping.Load() {
			err = domain.SafeError(context.Canceled)
		} else {
			continue
		}
		s.logger.Info("pull_request_workspace_finished", "repository_id", id, "outcome", workspaceOutcomeFailed, "error_code", domain.SafeError(err).Code, "attempt_count", attempts, "correlation_id", correlation)
		return repositoryIntegrationSelection{}, attempts, err
	}
}
