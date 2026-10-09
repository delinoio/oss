package store

import (
	"database/sql"
	"errors"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func prRemediationConflict() error {
	return domain.Fail(domain.Conflict, "The PR remediation chain or execution ownership changed.", "Inspect the original attempt; do not create a second active fix or reset its attempt budget.")
}

func (t *Tx) prRemediationActor() (domain.PRProblemDismissal, error) {
	actor, err := t.prProblemActor()
	v := domain.PRProblemDismissal{RequestID: t.requestID, ActorType: actor.Type, DeviceID: actor.DeviceID, At: t.now}
	if actor.Type == domain.OwnerDevice {
		v.DeviceID = ""
	}
	if err == nil {
		err = t.writeAllowed()
	}
	if err == nil {
		err = v.Validate()
	}
	return v, err
}

func (t *Tx) GetPRRemediationAttempt(id domain.ID) (Record, domain.PRRemediationAttempt, error) {
	if _, err := t.prProblemActor(); err != nil {
		return Record{}, domain.PRRemediationAttempt{}, err
	}
	r, err := t.Get(domain.ProblemKind, id)
	if err != nil {
		return r, domain.PRRemediationAttempt{}, err
	}
	v, err := Decode[domain.PRRemediationAttempt](r)
	if err != nil {
		return r, v, err
	}
	if v.Validate() != nil || r.SessionID != "" || r.ProjectID != "" {
		return r, v, prRemediationConflict()
	}
	var set, chain domain.ID
	var sequence uint32
	var state domain.PRRemediationAttemptState
	var input sql.NullString
	err = t.tx.QueryRowContext(t.ctx, "SELECT set_id,chain_id,sequence,state,input_id FROM pr_remediation_attempts WHERE id=?", id).Scan(&set, &chain, &sequence, &state, &input)
	if err != nil {
		return r, v, storageError(err)
	}
	if set != v.SetID || chain != v.ChainID || sequence != v.Sequence || state != v.State || input.String != string(v.InputID) || input.Valid != (v.InputID != "") {
		return r, v, prRemediationConflict()
	}
	_, parent, err := t.GetPRProblemSet(set)
	if err != nil {
		return r, v, err
	}
	c := parent.Remediation
	if c == nil || c.ID != chain || c.Sequence < sequence || (state.Active() != (c.ActiveAttemptID == id)) {
		return r, v, prRemediationConflict()
	}
	return r, v, nil
}

func (t *Tx) putPRRemediationAttempt(id domain.ID, expected uint64, v domain.PRRemediationAttempt) (Record, error) {
	if err := v.Validate(); err != nil {
		return Record{}, err
	}
	if expected != 0 {
		old, err := t.Get(domain.ProblemKind, id)
		if err != nil {
			return Record{}, err
		}
		previous, err := Decode[domain.PRRemediationAttempt](old)
		if err != nil || previous.Validate() != nil {
			return Record{}, prRemediationConflict()
		}
	}
	r, err := t.Put(domain.ProblemKind, id, expected, "", "", v)
	if err != nil {
		return r, err
	}
	var input any
	if v.InputID != "" {
		input = v.InputID
	}
	if expected == 0 {
		_, err = t.tx.ExecContext(t.ctx, "INSERT INTO pr_remediation_attempts(id,set_id,chain_id,sequence,state,input_id) VALUES(?,?,?,?,?,?)", id, v.SetID, v.ChainID, v.Sequence, v.State, input)
	} else {
		_, err = t.tx.ExecContext(t.ctx, "UPDATE pr_remediation_attempts SET state=?,input_id=? WHERE id=?", v.State, input, id)
	}
	return r, storageError(err)
}

// A reservation is only durable coordination. It cannot dispatch input, claim
// native authority, handle evidence or consume an automatic attempt.
// A zero attempt ID means the limit block was durably recorded in the set.
func (t *Tx) ReservePRRemediation(setID domain.ID, expected uint64, mode domain.PRRemediationMode, policy domain.RemediationPolicy, problems []domain.PRRemediationProblemRef) (Record, error) {
	actor, err := t.prRemediationActor()
	if err != nil {
		return Record{}, err
	}
	r, set, err := t.GetPRProblemSet(setID)
	if err != nil {
		return Record{}, err
	}
	if r.Revision != expected || !mode.Valid() || policy.Validate() != nil {
		return Record{}, prRemediationConflict()
	}
	if set.Remediation == nil {
		set.Remediation = &domain.PRRemediationChain{ID: domain.NewID()}
	}
	c := set.Remediation
	if c.ActiveAttemptID != "" {
		return Record{}, prRemediationConflict()
	}
	if c.Sequence >= domain.MaxPRRemediationAttempts {
		return Record{}, domain.Fail(domain.ResourceExhausted, "The PR remediation history is full.", "Preserve all original attempts; no history or budget was reset.")
	}
	value := domain.PRRemediationAttempt{Version: 1, Type: domain.PRRemediationAttemptRecord, SetID: setID, ChainID: c.ID, Sequence: c.Sequence + 1, Mode: mode, State: domain.PRRemediationReserved, Policy: policy, Problems: problems, Reserved: actor}
	if err = value.Validate(); err != nil {
		return Record{}, err
	}
	for _, ref := range problems {
		_, p, err := t.GetPRProblem(ref.ID)
		if err != nil {
			return Record{}, err
		}
		if p.SetID != setID || p.ContentVersion != ref.ContentVersion || p.State != domain.PRProblemUnhandled || (mode == domain.PRRemediationAutomatic && !policy.AutomaticKind(p.Kind)) {
			return Record{}, prRemediationConflict()
		}
	}
	if mode == domain.PRRemediationAutomatic && !c.CanStartAutomatic(policy) {
		c.Limit = &domain.PRRemediationLimit{Limit: policy.AttemptLimit, Attempts: c.AutomaticAttempts - c.ResumeBaseline, PolicyDigest: policy.Digest(), At: t.now}
		_, err = t.publishPRProblemSet(r, set)
		return Record{}, err
	}
	id := domain.NewID()
	c.Sequence, c.ActiveAttemptID = value.Sequence, id
	// A permitted manual attempt does not reset the exhausted automatic budget.
	if mode == domain.PRRemediationAutomatic {
		c.Limit = nil
	}
	if _, err = t.publishPRProblemSet(r, set); err != nil {
		return Record{}, err
	}
	return t.putPRRemediationAttempt(id, 0, value)
}

func (t *Tx) BindPRRemediation(id domain.ID, expected uint64, sessionID, inputID domain.ID) (Record, error) {
	if _, err := t.prRemediationActor(); err != nil {
		return Record{}, err
	}
	r, v, err := t.GetPRRemediationAttempt(id)
	if err != nil {
		return r, err
	}
	if r.Revision != expected || v.State != domain.PRRemediationReserved {
		return r, prRemediationConflict()
	}
	v.SessionID, v.InputID = sessionID, inputID
	ir, err := t.Get(domain.QueueKind, inputID)
	if err != nil {
		return r, err
	}
	queued, err := Decode[domain.QueuedInput](ir)
	if err != nil {
		return r, err
	}
	v.InputDigest = domain.PRRemediationInputDigest(queued)
	if _, _, err = t.prRemediationInput(v, domain.InputQueued); err != nil {
		return r, err
	}
	v.State = domain.PRRemediationBound
	return t.putPRRemediationAttempt(id, expected, v)
}

func (t *Tx) prRemediationInput(v domain.PRRemediationAttempt, delivery domain.InputDelivery) (Record, domain.Session, error) {
	sr, err := t.Get(domain.SessionKind, v.SessionID)
	if err != nil {
		return sr, domain.Session{}, err
	}
	s, err := Decode[domain.Session](sr)
	if err != nil {
		return sr, s, err
	}
	ir, err := t.Get(domain.QueueKind, v.InputID)
	if err != nil {
		return sr, s, err
	}
	i, err := Decode[domain.QueuedInput](ir)
	if err != nil {
		return sr, s, err
	}
	if v.InputDigest == "" || domain.PRRemediationInputDigest(i) != v.InputDigest || sr.SessionID != sr.ID || s.ProjectID != sr.ProjectID || ir.SessionID != sr.ID || ir.ProjectID != sr.ProjectID || i.Delivery != delivery || i.Mode != domain.ExecuteMode || s.Archive != domain.NotArchived || s.Recovery != domain.NoRecovery || s.Dispatch == domain.DispatchPaused || s.Workspace == domain.GeneralChat {
		return sr, s, prRemediationConflict()
	}
	if delivery == domain.InputClaimed && (i.ExecutionID != v.ExecutionID || s.ActiveExecutionID != v.ExecutionID || s.Dispatch != domain.DispatchClaimed) {
		return sr, s, prRemediationConflict()
	}
	return sr, s, nil
}

// The caller first creates the ordinary execution claim/job in this same
// transaction and must roll back the entire callback if this gate fails.
// Provider reads happen outside SQLite immediately before that transaction.
func (t *Tx) StartPRRemediation(id domain.ID, expected uint64, executionID domain.ID, observations map[domain.PRProblemKind]domain.RepositoryQueryResult) (Record, error) {
	if _, err := t.prRemediationActor(); err != nil {
		return Record{}, err
	}
	r, v, err := t.GetPRRemediationAttempt(id)
	if err != nil {
		return r, err
	}
	if r.Revision != expected || v.State != domain.PRRemediationBound || executionID.Validate() != nil {
		return r, prRemediationConflict()
	}
	if v.Mode == domain.PRRemediationAutomatic && v.GitTarget != nil {
		if err := t.RequireAutomaticPRSource(v); err != nil {
			return r, err
		}
	}
	v.ExecutionID = executionID
	_, session, err := t.prRemediationInput(v, domain.InputClaimed)
	if err != nil {
		return r, err
	}
	setRow, set, err := t.GetPRProblemSet(v.SetID)
	if err != nil {
		return r, err
	}
	if v.Mode == domain.PRRemediationAutomatic && !set.Remediation.CanStartAutomatic(v.Policy) {
		return r, prRemediationConflict()
	}
	kinds := map[domain.PRProblemKind]bool{}
	var shared *domain.RepositoryQueryResult
	for _, ref := range v.Problems {
		_, p, err := t.GetPRProblem(ref.ID)
		if err != nil {
			return r, err
		}
		observed, ok := observations[p.Kind]
		if !ok || p.SetID != v.SetID || p.ContentVersion != ref.ContentVersion {
			return r, prRemediationConflict()
		}
		decision, err := domain.EvaluatePRRemediation(p, observed, v.Policy, v.Mode, t.now)
		if err != nil {
			return r, err
		}
		if decision != domain.PRRemediationEligible {
			return r, domain.Fail(domain.Conflict, "Fresh PR remediation prerequisites are not satisfied.", "Refresh this problem kind before execution; no attempt was consumed.")
		}
		if shared != nil && (shared.RepositoryID != observed.RepositoryID || shared.RepositoryRevision != observed.RepositoryRevision || shared.ProfileID != observed.ProfileID || shared.GenerationID != observed.GenerationID || shared.Items[0].BaseSHA != observed.Items[0].BaseSHA || shared.Items[0].HeadSHA != observed.Items[0].HeadSHA || shared.Items[0].BaseRef != observed.Items[0].BaseRef || shared.Items[0].HeadRef != observed.Items[0].HeadRef) {
			return r, prRemediationConflict()
		}
		if err = t.requirePRRemediationSelection(observed, v.Policy, session); err != nil {
			return r, err
		}
		kinds[p.Kind] = true
		shared = &observed
	}
	if len(kinds) != len(observations) {
		return r, prRemediationConflict()
	}
	v.State, v.StartedAt = domain.PRRemediationRunning, &t.now
	if v.Mode == domain.PRRemediationAutomatic {
		set.Remediation.AutomaticAttempts++
		if !set.Remediation.CanStartAutomatic(v.Policy) {
			set.Remediation.Limit = &domain.PRRemediationLimit{Limit: v.Policy.AttemptLimit, Attempts: set.Remediation.AutomaticAttempts - set.Remediation.ResumeBaseline, PolicyDigest: v.Policy.Digest(), At: t.now}
		}
	}
	if _, err = t.publishPRProblemSet(setRow, set); err != nil {
		return r, err
	}
	return t.putPRRemediationAttempt(id, expected, v)
}

func (t *Tx) requirePRRemediationSelection(observed domain.RepositoryQueryResult, policy domain.RemediationPolicy, session domain.Session) error {
	r, err := t.Get(domain.RepositoryKind, observed.RepositoryID)
	if err != nil {
		return err
	}
	repo, err := Decode[domain.Repository](r)
	if err != nil {
		return err
	}
	if strconv.FormatUint(r.Revision, 10) != observed.RepositoryRevision || repo.IntegrationID != observed.ProfileID {
		return prRemediationConflict()
	}
	pr, err := t.Get(domain.IntegrationKind, observed.ProfileID)
	if err != nil {
		return err
	}
	profile, err := Decode[domain.Integration](pr)
	if err != nil {
		return err
	}
	if profile.Validate() != nil || profile.Pending != nil || profile.Connection == nil || profile.Connection.GenerationID != observed.GenerationID {
		return prRemediationConflict()
	}
	settings := domain.DefaultSettings()
	rows, err := t.List(Filter{Kind: domain.SettingsKind, Limit: 2})
	if err != nil {
		return err
	}
	if len(rows) > 1 {
		return prRemediationConflict()
	}
	if len(rows) > 0 {
		settings, err = Decode[domain.Settings](rows[0])
		if err != nil {
			return err
		}
	}
	current, err := repo.EffectiveRemediation(settings.Remediation)
	if err != nil {
		return err
	}
	if current.Digest() != policy.Digest() {
		return prRemediationConflict()
	}
	projectRow, err := t.Get(domain.ProjectKind, session.ProjectID)
	if err != nil {
		return err
	}
	project, err := Decode[domain.Project](projectRow)
	if err != nil {
		return err
	}
	found := false
	for _, id := range project.Repositories {
		if id == observed.RepositoryID {
			found = true
		}
	}
	if !found {
		return prRemediationConflict()
	}
	return nil
}

// Finish follows the server-verified original execution job and cleanup report.
// A native success alone does not claim a push, resolve feedback or free the PR.
func (t *Tx) FinishPRRemediation(id domain.ID, expected uint64) (Record, error) {
	if _, err := t.prRemediationActor(); err != nil {
		return Record{}, err
	}
	r, v, err := t.GetPRRemediationAttempt(id)
	if err != nil {
		return r, err
	}
	if r.Revision != expected || (v.State != domain.PRRemediationRunning && v.State != domain.PRRemediationUncertain) {
		return r, prRemediationConflict()
	}
	sr, err := t.Get(domain.SessionKind, v.SessionID)
	if err != nil {
		return r, err
	}
	s, err := Decode[domain.Session](sr)
	if err != nil {
		return r, err
	}
	if s.StartupRejection != nil {
		jr, rejected, verified, err := t.VerifiedStartupRejection(v.SessionID)
		if err != nil {
			return r, err
		}
		if verified && rejected.Workspace.ExecutionID == v.ExecutionID && rejected.InputID == v.InputID {
			ir, err := t.Get(domain.QueueKind, v.InputID)
			if err != nil {
				return r, err
			}
			queued, err := Decode[domain.QueuedInput](ir)
			if err != nil {
				return r, err
			}
			job, err := Decode[domain.Job](jr)
			if err != nil {
				return r, err
			}
			if domain.PRRemediationInputDigest(queued) == v.InputDigest && !job.FinishedAt.Before(*v.StartedAt) {
				v.State, v.Outcome, v.FinishedAt = domain.PRRemediationFinished, domain.ExecutionNotStarted, &t.now
				v.StartupRejectionJobID = jr.ID
				return t.releasePRRemediation(r, v)
			}
		}
	}
	p := s.Execution
	verified := p != nil && p.ExecutionID == v.ExecutionID && p.InputID == v.InputID && p.CleanupVerified && s.ActiveExecutionID == "" && s.Recovery == domain.NoRecovery
	if verified {
		jr, err := t.Get(domain.JobKind, p.JobID)
		if err != nil {
			return r, err
		}
		j, err := Decode[domain.Job](jr)
		if err != nil {
			return r, err
		}
		var input domain.ExecutionJobInput
		var done domain.ExecutionCompletion
		stateMatches := j.State == domain.JobSucceeded && p.Outcome == domain.ExecutionSucceeded || j.State == domain.JobFailed && p.Outcome == domain.ExecutionFailed || j.State == domain.JobCanceled && p.Outcome == domain.ExecutionStopped
		verified = stateMatches && j.FinishedAt != nil && !j.FinishedAt.Before(*v.StartedAt) && jr.SessionID == v.SessionID && j.MachineID == s.MachineID && j.Type == domain.ExecuteSessionJob && domain.Decode(j.Input, &input) == nil && input.Validate() == nil && s.OwnsExecution(input) && domain.Decode(j.Output, &done) == nil && done.ValidateForHarness(input.Configuration.Harness) == nil && input.SessionID == v.SessionID && input.InputID == v.InputID && input.ExecutionID == v.ExecutionID && done.ExecutionID == v.ExecutionID && done.InputID == v.InputID && done.Outcome == p.Outcome && done.LastSequence == p.LastSequence && string(done.NativeThreadID) == p.NativeThreadID && string(done.NativeTurnID) == p.NativeTurnID
		if verified {
			ir, err := t.Get(domain.QueueKind, v.InputID)
			if err != nil {
				return r, err
			}
			queued, err := Decode[domain.QueuedInput](ir)
			if err != nil {
				return r, err
			}
			verified = ir.SessionID == v.SessionID && queued.ExecutionID == v.ExecutionID && domain.PRRemediationInputDigest(queued) == v.InputDigest && input.Input.Prompt == queued.Prompt && input.Input.Mode == queued.Mode && len(input.Input.Skills) == 0 && len(queued.Skills) == 0 && len(input.Input.Attachments) == 0 && len(queued.Attachments) == 0
		}
	}
	if !verified {
		v.State = domain.PRRemediationUncertain
		return t.putPRRemediationAttempt(id, expected, v)
	}
	if v.GitTarget != nil {
		jr, err := t.Get(domain.JobKind, p.JobID)
		if err != nil {
			return r, err
		}
		job, err := Decode[domain.Job](jr)
		if err != nil {
			return r, err
		}
		var input domain.ExecutionJobInput
		var done domain.ExecutionCompletion
		if domain.Decode(job.Input, &input) != nil || domain.Decode(job.Output, &done) != nil {
			return r, prRemediationConflict()
		}
		confirmed, err := t.finishPRFixPush(id, v, input, done)
		if err != nil {
			return r, err
		}
		if !confirmed {
			if v.State == domain.PRRemediationUncertain {
				return r, nil
			}
			v.State = domain.PRRemediationUncertain
			return t.putPRRemediationAttempt(id, expected, v)
		}
	}
	v.State, v.Outcome, v.FinishedAt = domain.PRRemediationFinished, p.Outcome, &t.now
	return t.releasePRRemediation(r, v)
}

func (t *Tx) CancelPRRemediation(id domain.ID, expected uint64) (Record, error) {
	if _, err := t.prRemediationActor(); err != nil {
		return Record{}, err
	}
	r, v, err := t.GetPRRemediationAttempt(id)
	if err != nil {
		return r, err
	}
	if r.Revision != expected || (v.State != domain.PRRemediationReserved && v.State != domain.PRRemediationBound) {
		return r, prRemediationConflict()
	}
	if v.InputID != "" {
		ir, err := t.Get(domain.QueueKind, v.InputID)
		if err != nil {
			return r, err
		}
		i, err := Decode[domain.QueuedInput](ir)
		if err != nil {
			return r, err
		}
		if ir.SessionID != v.SessionID || i.Delivery != domain.InputRemoved || i.ExecutionID != "" {
			return r, prRemediationConflict()
		}
	}
	v.State, v.FinishedAt = domain.PRRemediationCanceled, &t.now
	return t.releasePRRemediation(r, v)
}

func (t *Tx) releasePRRemediation(r Record, v domain.PRRemediationAttempt) (Record, error) {
	setRow, set, err := t.GetPRProblemSet(v.SetID)
	if err != nil {
		return r, err
	}
	if set.Remediation.ActiveAttemptID != r.ID {
		return r, prRemediationConflict()
	}
	set.Remediation.ActiveAttemptID = ""
	if _, err = t.publishPRProblemSet(setRow, set); err != nil {
		return r, err
	}
	return t.putPRRemediationAttempt(r.ID, r.Revision, v)
}

func (t *Tx) ResumePRRemediation(setID domain.ID, expected uint64) (Record, error) {
	actor, err := t.prRemediationActor()
	if err != nil {
		return Record{}, err
	}
	r, v, err := t.GetPRProblemSet(setID)
	if err != nil {
		return r, err
	}
	if r.Revision != expected || v.Remediation == nil || v.Remediation.ActiveAttemptID != "" {
		return r, prRemediationConflict()
	}
	c := v.Remediation
	c.ResumeBaseline, c.Limit, c.LastResume = c.AutomaticAttempts, nil, &actor
	return t.publishPRProblemSet(r, v)
}

func (t *Tx) PRRemediationForInput(input domain.ID) (Record, domain.PRRemediationAttempt, bool, error) {
	if input.Validate() != nil {
		return Record{}, domain.PRRemediationAttempt{}, false, prRemediationConflict()
	}
	if _, err := t.prProblemActor(); err != nil {
		return Record{}, domain.PRRemediationAttempt{}, false, err
	}
	var id domain.ID
	err := t.tx.QueryRowContext(t.ctx, "SELECT id FROM pr_remediation_attempts WHERE input_id=?", input).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, domain.PRRemediationAttempt{}, false, nil
	}
	if err != nil {
		return Record{}, domain.PRRemediationAttempt{}, false, storageError(err)
	}
	r, v, err := t.GetPRRemediationAttempt(id)
	return r, v, true, err
}

// All execution paths share this commit invariant, including ordinary Resume
// and FIFO dispatch. A bound remediation input cannot bypass the fresh gate by
// taking an otherwise valid ordinary execution path. It has no effect on inputs
// without an indexed remediation binding and does not inspect prompt contents.
func (t *Tx) validatePRRemediationClaims() error {
	for id := range t.queueTouched {
		var rawInput, rawAttempt []byte
		err := t.tx.QueryRowContext(t.ctx, `SELECT q.body,a.body FROM entities q JOIN pr_remediation_attempts r ON r.input_id=q.id JOIN entities a ON a.id=r.id WHERE q.id=? AND q.kind='queue'`, id).Scan(&rawInput, &rawAttempt)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return storageError(err)
		}
		var input domain.QueuedInput
		var attempt domain.PRRemediationAttempt
		if domain.Decode(rawInput, &input) != nil || domain.Decode(rawAttempt, &attempt) != nil || attempt.Validate() != nil {
			return prRemediationConflict()
		}
		if input.Delivery == domain.InputQueued || input.Delivery == domain.InputRemoved {
			continue
		}
		if attempt.ExecutionID == "" || attempt.ExecutionID != input.ExecutionID || domain.PRRemediationInputDigest(input) != attempt.InputDigest || (attempt.State != domain.PRRemediationRunning && attempt.State != domain.PRRemediationUncertain && attempt.State != domain.PRRemediationFinished) {
			return prRemediationConflict()
		}
	}
	return nil
}
