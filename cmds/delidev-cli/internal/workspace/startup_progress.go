// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type startupObserverKey struct{}
type startupRepositoryKey struct{}

// WithStartupObserver scopes best-effort metadata to this invocation. It cannot
// alter ownership, deadlines or returned operation results. The Worker callback
// must be nonblocking; no URLs, paths or native output cross this boundary.
func WithStartupObserver(ctx context.Context, observe func(domain.StartupProgressStep)) context.Context {
	return context.WithValue(ctx, startupObserverKey{}, observe)
}
func startupRepository(ctx context.Context, id domain.ID, ordinal, count int) context.Context {
	return context.WithValue(ctx, startupRepositoryKey{}, domain.StartupProgressStep{RepositoryID: id, RepositoryOrdinal: uint32(ordinal), RepositoryCount: uint32(count)})
}
func startupProgress(ctx context.Context, op domain.StartupWorkspaceOperation, state domain.StartupProgressState) {
	observer, _ := ctx.Value(startupObserverKey{}).(func(domain.StartupProgressStep))
	if observer == nil {
		return
	}
	s, _ := ctx.Value(startupRepositoryKey{}).(domain.StartupProgressStep)
	s.WorkspaceOperation, s.State = op, state
	observer(s)
}

// StartupProgressPlan contains only original IDs and closed applicable stages.
// It is derived from the immutable request by the server, never Worker claims.
func StartupProgressPlan(r PrepareRequest) []domain.StartupProgressStep {
	steps := []domain.StartupProgressStep{{WorkspaceOperation: domain.StartupWorkspaceSetup}}
	for i, spec := range r.Repositories {
		ops := []domain.StartupWorkspaceOperation{domain.StartupWorkspaceInspect}
		if spec.SourceKind.managed() {
			ops = []domain.StartupWorkspaceOperation{domain.StartupWorkspaceClone, domain.StartupWorkspaceInspect}
		}
		if r.Type != domain.Local {
			ops = append(ops, domain.StartupWorkspaceReference, domain.StartupWorkspaceCheckout)
		}
		for _, op := range ops {
			steps = append(steps, domain.StartupProgressStep{WorkspaceOperation: op, RepositoryID: spec.ID, RepositoryOrdinal: uint32(i + 1), RepositoryCount: uint32(len(r.Repositories))})
		}
	}
	return append(steps, domain.StartupProgressStep{WorkspaceOperation: domain.StartupWorkspaceVerify}, domain.StartupProgressStep{WorkspaceOperation: domain.StartupWorkspacePublish})
}
