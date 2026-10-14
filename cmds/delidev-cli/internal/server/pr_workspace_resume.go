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
		if resume.failedDisplacement.Load() {
			return repositoryIntegrationSelection{}, attempts, errIntegrationDisplacementFailed
		}
		attempts++
		if attempts > 1 {
			s.logger.Info("pull_request_workspace_resumed", "repository_id", id, "attempt_count", attempts, "correlation_id", correlation)
		}
		selected, err := s.admitRepositoryIntegration(ctx, id, "workspace", correlation, attempts, resume, read)
		if resume.failedDisplacement.Load() {
			return repositoryIntegrationSelection{}, attempts, errIntegrationDisplacementFailed
		}
		if errors.Is(err, errIntegrationDisplacementFailed) {
			return repositoryIntegrationSelection{}, attempts - 1, err
		}
		if !errors.Is(err, errIntegrationPreempted) {
			return selected, attempts, err
		}
		if resume.failedDisplacement.Load() {
			return repositoryIntegrationSelection{}, attempts, errIntegrationDisplacementFailed
		}
		s.logger.Info("pull_request_workspace_requeued", "repository_id", id, "attempt_count", attempts, "correlation_id", correlation)
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = domain.SafeError(ctxErr)
		} else if s.stopping.Load() {
			err = domain.SafeError(context.Canceled)
		} else {
			continue
		}
		return repositoryIntegrationSelection{}, attempts, err
	}
}
