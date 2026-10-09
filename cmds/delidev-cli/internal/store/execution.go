package store

import (
	"database/sql"
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Routing reads one Agent Worker's persisted selection state. Existing stores
// may predate its dedicated writer; contradictory duplicates require recovery.
func (t *Tx) Routing(agentID domain.ID) (Record, domain.RoutingState, error) {
	if err := agentID.Validate(); err != nil {
		return Record{}, domain.RoutingState{}, err
	}
	records, _, err := t.sessionPage(2, "SELECT "+recordColumns+" FROM entities WHERE kind='routing' AND json_extract(body,'$.agent_id')=? ORDER BY id LIMIT 2", agentID)
	if err != nil {
		return Record{}, domain.RoutingState{}, err
	}
	if len(records) > 1 {
		return Record{}, domain.RoutingState{}, domain.Fail(domain.RecoveryRequired, "Agent routing ownership is ambiguous.", "Preserve the original data and reconcile duplicate routing records.")
	}
	if len(records) == 0 {
		return Record{}, domain.RoutingState{}, nil
	}
	route, err := Decode[domain.AgentRouting](records[0])
	return records[0], route.State, err
}

func decodeEntity[T any](t *Tx, kind domain.Kind, id domain.ID) (Record, T, error) {
	r, err := t.Get(kind, id)
	if err != nil {
		var empty T
		return r, empty, err
	}
	value, err := Decode[T](r)
	return r, value, err
}

// ClaimInitialExecution is a transaction primitive, not execution authority or
// a public command. The coordinator must first validate native/account/workspace
// readiness and set DispatchReady, then compare this exact returned selection
// with its validated evidence before committing the same transaction. No native
// send, credential grant or process launch is permitted inside the transaction.
// The public first-dispatch coordinator uses this primitive and queues the exact
// validated Worker assignment in the same transaction.
func (t *Tx) ClaimInitialExecution(sessionID domain.ID, sessionRevision uint64, inputID domain.ID, inputRevision uint64) (domain.InitialExecution, error) {
	var empty domain.InitialExecution
	if err := t.writeAllowed(); err != nil {
		return empty, err
	}
	sr, session, err := decodeEntity[domain.Session](t, domain.SessionKind, sessionID)
	if err != nil {
		return empty, err
	}
	if !session.WorkspaceAvailable() || sr.Revision != sessionRevision || session.InitialExecution != nil || session.CurrentExecution != nil || session.NextExecutionIntent != "" || session.ActiveExecutionID != "" || session.Outcome != domain.ExecutionNotStarted || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.Dispatch != domain.DispatchReady {
		return empty, domain.Fail(domain.Conflict, "The session is not ready for its first execution claim.", "Revalidate the current session and native readiness without replacing an existing snapshot.")
	}
	if session.Preparation == nil || session.Preparation.State != domain.PreparationReady {
		return empty, domain.Fail(domain.Conflict, "The workspace is not ready for execution.", "Finish preparation and reconcile its ownership before dispatch.")
	}
	pr, preparation, err := decodeEntity[domain.Job](t, domain.JobKind, session.Preparation.JobID)
	if err != nil {
		return empty, err
	}
	if pr.SessionID != sessionID || preparation.Type != domain.PrepareWorkspaceJob || preparation.State != domain.JobSucceeded || preparation.MachineID != session.MachineID {
		return empty, domain.Fail(domain.RecoveryRequired, "Workspace preparation ownership is inconsistent.", "Reconcile the original preparation before execution.")
	}
	ir, input, err := decodeEntity[domain.QueuedInput](t, domain.QueueKind, inputID)
	if err != nil {
		return empty, err
	}
	if ir.SessionID != sessionID || ir.Revision != inputRevision || input.Delivery != domain.InputQueued || input.ExecutionID != "" || input.NativeRequestID != "" {
		return empty, domain.Fail(domain.Conflict, "The input no longer matches the queued selection.", "Read its current ownership, content revision and delivery state.")
	}
	if err := (domain.SessionInput{Prompt: input.Prompt, Mode: input.Mode, Attachments: input.Attachments}).Validate(); err != nil {
		return empty, err
	}
	var head domain.ID
	err = t.tx.QueryRowContext(t.ctx, "SELECT id FROM entities WHERE kind='queue' AND session_id=? AND json_extract(body,'$.delivery')='queued' ORDER BY json_extract(body,'$.sequence') LIMIT 1", sessionID).Scan(&head)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return empty, storageError(err)
	}
	if head != inputID {
		return empty, domain.Fail(domain.Conflict, "An earlier input must be dispatched first.", "Keep the transaction-assigned queue order.")
	}
	if session.PendingInputs == 0 || session.PendingInputBytes < uint64(len(input.Prompt)) {
		return empty, domain.Fail(domain.RecoveryRequired, "Pending input accounting is inconsistent.", "Reconcile retained input ownership before dispatch.")
	}
	preview, err := t.PreviewInitialExecution(session)
	if err != nil {
		return empty, err
	}
	accepted := domain.InitialExecution{ID: domain.NewID(), InputID: inputID, Configuration: preview.Configuration, ConfigurationDigest: preview.ConfigurationDigest, InitialAccountID: preview.AccountID, ConnectionID: preview.ConnectionID, Route: preview.Route, AcceptedAt: t.now}
	input.Delivery = domain.InputClaimed
	input.ExecutionID = accepted.ID
	input.NativeRequestID = domain.NewID()
	session.InitialExecution = &accepted
	session.ActiveExecutionID = accepted.ID
	session.Dispatch = domain.DispatchClaimed
	session.Problem = nil
	// Queue accounting includes a claimed input until native acceptance is
	// proven. A transaction claim itself cannot release capacity or claim a turn.
	if _, err := t.Put(domain.QueueKind, ir.ID, ir.Revision, sr.ID, sr.ProjectID, input); err != nil {
		return empty, err
	}
	if _, err := t.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
		return empty, err
	}
	routingRecord := preview.routingRecord
	if routingRecord.ID == "" {
		routingRecord.ID = domain.NewID()
	}
	if _, err := t.Put(domain.RoutingKind, routingRecord.ID, routingRecord.Revision, "", "", domain.AgentRouting{AgentID: session.AgentID, State: preview.nextRouting}); err != nil {
		return empty, err
	}
	return accepted, nil
}

// InitialExecutionPreview is a read-only selection from one current database
// snapshot. It does not reserve an account, advance routing or grant execution.
// Claims always resolve it again inside their own atomic mutation.
type InitialExecutionPreview struct {
	Configuration       domain.ExecutionConfiguration
	ConfigurationDigest string
	AccountID           domain.ID
	ConnectionID        domain.ID
	Route               domain.Route
	routingRecord       Record
	nextRouting         domain.RoutingState
}

func (t *Tx) PreviewInitialExecution(session domain.Session) (InitialExecutionPreview, error) {
	var empty InitialExecutionPreview
	if err := t.Authorize(); err != nil {
		return empty, err
	}
	ar, agent, err := decodeEntity[domain.Agent](t, domain.AgentKind, session.AgentID)
	if err != nil {
		return empty, err
	}
	if err := agent.Validate(); err != nil {
		return empty, err
	}
	_, machine, err := decodeEntity[domain.Machine](t, domain.MachineKind, session.MachineID)
	if err != nil {
		return empty, err
	}
	if machine.Disabled {
		return empty, domain.Fail(domain.Unavailable, "The execution machine is disabled.", "Enable and revalidate its native execution readiness.")
	}
	var project *domain.Project
	var projectRecord Record
	if session.ProjectID != "" {
		record, value, err := decodeEntity[domain.Project](t, domain.ProjectKind, session.ProjectID)
		if err != nil {
			return empty, err
		}
		project = &value
		projectRecord = record
	}
	policy, err := t.DefaultRoutingPolicy()
	if err != nil {
		return empty, err
	}
	templates := make([]domain.AppliedTemplate, 0, len(agent.Templates))
	instructionBytes := 0
	for _, id := range agent.Templates {
		r, template, err := decodeEntity[domain.Template](t, domain.TemplateKind, id)
		if err != nil {
			return empty, err
		}
		instructionBytes += len(template.Contents)
		if len(templates) > 0 {
			instructionBytes += 2
		}
		// Bound accumulation while reading, before collecting every otherwise
		// valid linked template into a potentially much larger temporary slice.
		if instructionBytes > domain.MaxAppliedInstructions {
			return empty, domain.Fail(domain.ResourceExhausted, "Combined instructions exceed the native adapter bound.", "Reduce the ordered templates before first execution; no contents were truncated.")
		}
		templates = append(templates, domain.AppliedTemplate{ID: id, Revision: r.Revision, Contents: template.Contents})
	}
	preview, err := t.PreviewSourceRouting(ar.ID, agent, project, policy)
	if err != nil {
		return empty, err
	}
	agent, model := preview.Agent, preview.Model
	configuration, err := domain.ResolveExecutionConfiguration(ar.ID, ar.Revision, agent, preview.ModelRevision, model, preview.Route.Policy, templates)
	if err != nil {
		return empty, err
	}
	settingsRecord, settings, err := t.SessionDefaultSettings()
	if err != nil {
		return empty, err
	}
	configuration.BranchPrefix = &domain.BranchPrefixSelection{Version: 1, Prefix: project.EffectiveBranchPrefix(settings.EffectiveBranchPrefix()), SettingsID: settingsRecord.ID, SettingsRevision: settingsRecord.Revision, ProjectID: projectRecord.ID, ProjectRevision: projectRecord.Revision}
	if err := configuration.Validate(); err != nil {
		return empty, err
	}
	var provider domain.Provider
	if model.SourceKind != domain.SubscriptionModel {
		_, provider, err = decodeEntity[domain.Provider](t, domain.ProviderKind, model.ProviderID)
		if err != nil {
			return empty, err
		}
	}
	route, accounts := preview.Route, preview.Accounts
	selected := accounts[route.Selected]
	authentication := provider.Authentication
	if configuration.Subscription {
		authentication = domain.SubscriptionAuth
	}
	if configuration.IsOpenCodeGo() {
		authentication = domain.BearerAuth
	}
	if selected.Connection == nil || selected.Connection.ID.Validate() != nil || selected.Connection.Authentication != authentication || !model.MatchesAccount(selected, agent.Harness) {
		return empty, domain.Fail(domain.Conflict, "The selected account connection is incompatible with the provider.", "Revalidate the current account connection before dispatch.")
	}
	if err := t.resolveCodexSubagentModel(&configuration, selected); err != nil {
		return empty, err
	}
	digest, err := configuration.Digest()
	if err != nil {
		return empty, err
	}
	return InitialExecutionPreview{Configuration: configuration, ConfigurationDigest: digest, AccountID: route.Selected, ConnectionID: selected.Connection.ID, Route: route, routingRecord: preview.routingRecord, nextRouting: preview.nextRouting}, nil
}
