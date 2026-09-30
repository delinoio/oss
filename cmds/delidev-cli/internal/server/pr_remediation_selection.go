package server

import (
	"context"
	"slices"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

// A successful match remains bound to the original candidate revision. Session
// activity, pause/Archive or preparation changes during the read invalidate it;
// the later acceptance transaction must compare this selection again.
func (s *Service) matchPRRemediationWorkspace(ctx context.Context, selected store.Record, target domain.PRGitTarget) (workspace.PRWorkspaceMatch, error) {
	var empty workspace.PRWorkspaceMatch
	if _, err := integrationActor(ctx); err != nil {
		return empty, err
	}
	if selected.Kind != domain.SessionKind || selected.ID.Validate() != nil || selected.Revision == 0 || target.Validate() != nil {
		return empty, domain.Fail(domain.InvalidArgument, "Invalid PR workspace candidate.", "Select an original linked session and current PR target.")
	}
	check := func(tx *store.Tx) error {
		r, session, err := sessionRecord(tx, selected.ID)
		if err != nil {
			return err
		}
		if r.Revision != selected.Revision || r.ProjectID != selected.ProjectID || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.Dispatch == domain.DispatchPaused || (session.Workspace != domain.Worktree && session.Workspace != domain.Local) {
			return domain.Fail(domain.Conflict, "The PR workspace candidate changed during inspection.", "Refresh eligible linked sessions without resuming paused or archived work.")
		}
		if _, err = sessionPRScope(tx, selected.ID, target.Target.RepositoryID); err != nil {
			return err
		}
		links, err := tx.List(store.Filter{Kind: domain.PullRequestKind, SessionID: selected.ID, Limit: domain.MaxSessionPullRequests + 1})
		if err != nil {
			return err
		}
		if len(links) > domain.MaxSessionPullRequests {
			return workspace.ResultUncertain()
		}
		matches := 0
		for _, row := range links {
			link, err := store.Decode[domain.SessionPullRequest](row)
			if err != nil || link.Validate() != nil || row.ProjectID != r.ProjectID || row.SessionID != r.ID {
				return workspace.ResultUncertain()
			}
			if link.SamePR(target.Target) && link.RepositoryID == target.Target.RepositoryID && link.RepositoryNodeID == target.Target.RepositoryNodeID && link.PullRequestNodeID == target.Target.PullRequestNodeID && link.Number == target.Target.Number {
				matches++
			}
		}
		if matches != 1 {
			return domain.Fail(domain.Conflict, "The original PR association changed during inspection.", "Refresh the exact linked session; historical names or another PR link cannot substitute for it.")
		}
		return nil
	}
	var input workspace.PrepareRequest
	var manifest workspace.Manifest
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := check(tx); err != nil {
			return err
		}
		var err error
		input, manifest, err = workspaceReadScope(tx, selected.ID)
		return err
	}); err != nil {
		return empty, err
	}
	raw, err := s.observeWorkerWorkspace(ctx, selected.ID, input, manifest, domain.WorkspaceReadQuery{}, &target)
	if err != nil {
		return empty, err
	}
	if err := s.Store.Read(ctx, check); err != nil {
		return empty, err
	}
	var result workspace.PRWorkspaceMatch
	if domain.Decode(raw, &result) != nil {
		return empty, workspace.ResultUncertain()
	}
	return result, nil
}

// A plan is a read-only configuration check, not a session, account reservation
// or native Git grant. Acceptance must replan in its transaction; native startup
// independently checks the original PR and the selected Worker's Git access.
type prRemediationWorkspacePlan struct {
	Preparation workspace.PrepareRequest
	Execution   store.InitialExecutionPreview
}

func planPRRemediationWorkspace(tx *store.Tx, sessionID, projectID domain.ID, policy domain.RemediationPolicy, target domain.PRGitTarget) (prRemediationWorkspacePlan, error) {
	var empty prRemediationWorkspacePlan
	if sessionID.Validate() != nil || projectID.Validate() != nil || policy.Validate() != nil || target.Validate() != nil {
		return empty, domain.Fail(domain.InvalidArgument, "Invalid PR workspace selection.", "Retain the exact original PR and explicitly select its project.")
	}
	if policy.AgentID == "" || policy.MachineID == "" {
		return empty, domain.Fail(domain.MissingInput, "A new PR session requires an explicit Agent Worker and execution machine.", "Configure both in this repository's effective remediation policy.")
	}
	selection := domain.CreateSession{AgentID: policy.AgentID, MachineID: policy.MachineID, ProjectID: projectID, Workspace: domain.Worktree}
	if err := validateSessionSelection(tx, selection); err != nil {
		return empty, err
	}
	pr, err := tx.Get(domain.ProjectKind, projectID)
	if err != nil {
		return empty, err
	}
	project, err := store.Decode[domain.Project](pr)
	if err != nil {
		return empty, err
	}
	if !slices.Contains(project.Repositories, target.Target.RepositoryID) {
		return empty, domain.Fail(domain.PermissionDenied, "The original PR repository is outside the selected project.", "Select a current project containing that repository; no project is inferred from a historical link.")
	}
	session := domain.Session{AgentID: policy.AgentID, MachineID: policy.MachineID, ProjectID: projectID, Workspace: domain.Worktree}
	_, machine, err := activeMachine(tx, session.MachineID)
	if err != nil {
		return empty, err
	}
	instance, seen, err := tx.WorkerInstance(session.MachineID)
	if err != nil {
		return empty, err
	}
	if instance.Validate() != nil || seen.After(time.Now().UTC().Add(time.Second)) || time.Since(seen) > domain.WorkerConnectionTimeout {
		return empty, domain.Fail(domain.Unavailable, "The selected PR Runner Device is not connected.", "Reconnect that machine before creating the remediation session.")
	}
	preview, err := tx.PreviewInitialExecution(session)
	if err != nil {
		return empty, err
	}
	input := domain.ExecutionJobInput{Version: 1, Input: domain.SessionInput{Mode: domain.ExecuteMode}, Configuration: preview.Configuration, ConfigurationDigest: preview.ConfigurationDigest, AccountID: preview.AccountID, ConnectionID: preview.ConnectionID}
	if _, err := checkedExecutionSelection(tx, session, machine, input); err != nil {
		return empty, err
	}
	if preview.Configuration.Harness == domain.GrokBuild {
		return empty, domain.Fail(domain.Unsupported, "The selected Grok profile does not support repository execution.", "Select an Agent Worker with verified repository execution support.")
	}
	if preview.Configuration.Options.Permission == domain.PermissionReadOnly {
		return empty, domain.Fail(domain.PermissionDenied, "The selected Agent Worker is read-only.", "Explicitly configure an execution permission that permits the requested PR edits; permissions are never elevated by remediation.")
	}
	preparation, err := sessionWorkspaceRequest(tx, sessionID, session)
	if err != nil {
		return empty, err
	}
	for i := range preparation.Repositories {
		repo := &preparation.Repositories[i]
		if repo.ID == target.Target.RepositoryID {
			// PR remediation always starts at its explicit head, including a
			// conflicting PR. Unrelated project repositories keep their ordinary
			// preparation policy and the project's original primary cwd.
			copy := target
			repo.PRTarget = &copy
			repo.Base = domain.Reference{Type: domain.CommitReference, Name: target.BaseSHA}
			repo.Starting = domain.Reference{Type: domain.CommitReference, Name: target.HeadSHA}
			repo.AutoFetch = true
		}
	}
	if preview.Configuration.Harness == domain.Codex {
		c := preview.Configuration
		settings := codex.ThreadSettings{Model: c.NativeModel, Provider: codex.APIProvider, Effort: c.Effort, Instructions: c.Instructions, Options: c.Options}
		for _, repo := range preparation.Repositories {
			if repo.ID == preparation.PrimaryRepository {
				settings.Cwd = repo.Checkout
			}
			if len(preparation.Repositories) > 1 {
				settings.WorkspaceRoots = append(settings.WorkspaceRoots, repo.Checkout)
			}
		}
		if err := codex.ValidateSelection(settings); err != nil {
			return empty, err
		}
	}
	return prRemediationWorkspacePlan{Preparation: preparation, Execution: preview}, nil
}
