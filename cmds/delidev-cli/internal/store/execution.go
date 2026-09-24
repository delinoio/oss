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
	if sr.Revision != sessionRevision || session.InitialExecution != nil || session.CurrentExecution != nil || session.NextExecutionIntent != "" || session.ActiveExecutionID != "" || session.Outcome != domain.ExecutionNotStarted || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.Dispatch != domain.DispatchReady {
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
	if err := (domain.SessionInput{Prompt: input.Prompt, Mode: input.Mode}).Validate(); err != nil {
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
	ar, agent, err := decodeEntity[domain.Agent](t, domain.AgentKind, session.AgentID)
	if err != nil {
		return empty, err
	}
	if err := agent.Validate(); err != nil {
		return empty, err
	}
	mr, model, err := decodeEntity[domain.Model](t, domain.ModelKind, agent.ModelID)
	if err != nil {
		return empty, err
	}
	_, provider, err := decodeEntity[domain.Provider](t, domain.ProviderKind, model.ProviderID)
	if err != nil {
		return empty, err
	}
	if err := provider.Validate(); err != nil {
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
	if session.ProjectID != "" {
		_, value, err := decodeEntity[domain.Project](t, domain.ProjectKind, session.ProjectID)
		if err != nil {
			return empty, err
		}
		project = &value
	}
	settings := domain.DefaultSettings()
	settingsRecords, err := t.List(Filter{Kind: domain.SettingsKind, Limit: 2})
	if err != nil {
		return empty, err
	}
	if len(settingsRecords) > 1 {
		return empty, domain.Fail(domain.RecoveryRequired, "Global settings ownership is ambiguous.", "Reconcile duplicate settings before dispatch.")
	}
	if len(settingsRecords) == 1 {
		settings, err = Decode[domain.Settings](settingsRecords[0])
		if err != nil {
			return empty, err
		}
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
	configuration, err := domain.ResolveExecutionConfiguration(ar.ID, ar.Revision, agent, mr.Revision, model, settings.DefaultRouting, templates)
	if err != nil {
		return empty, err
	}
	digest, err := configuration.Digest()
	if err != nil {
		return empty, err
	}
	accounts := make(map[domain.ID]domain.Account, len(agent.Accounts))
	for _, link := range agent.Accounts {
		_, account, err := decodeEntity[domain.Account](t, domain.AccountKind, link.ID)
		if err != nil {
			if domain.SafeError(err).Code == domain.NotFound {
				continue
			}
			return empty, err
		}
		accounts[link.ID] = account
	}
	routingRecord, routing, err := t.Routing(ar.ID)
	if err != nil {
		return empty, err
	}
	route, next, err := domain.RouteAccount(ar.ID, agent, model, project, accounts, configuration.Routing, routing, t.now)
	if err != nil {
		return empty, err
	}
	selected := accounts[route.Selected]
	if selected.Connection == nil || selected.Connection.ID.Validate() != nil || selected.Connection.Authentication != provider.Authentication || (selected.Type == domain.SubscriptionAccount) != (provider.Protocol == domain.NativeSubscription) {
		return empty, domain.Fail(domain.Conflict, "The selected account connection is incompatible with the provider.", "Revalidate the current account connection before dispatch.")
	}
	accepted := domain.InitialExecution{ID: domain.NewID(), InputID: inputID, Configuration: configuration, ConfigurationDigest: digest, InitialAccountID: route.Selected, ConnectionID: selected.Connection.ID, Route: route, AcceptedAt: t.now}
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
	if routingRecord.ID == "" {
		routingRecord.ID = domain.NewID()
	}
	if _, err := t.Put(domain.RoutingKind, routingRecord.ID, routingRecord.Revision, "", "", domain.AgentRouting{AgentID: ar.ID, State: next}); err != nil {
		return empty, err
	}
	return accepted, nil
}
