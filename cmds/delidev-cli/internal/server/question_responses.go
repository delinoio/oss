package server

import (
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// This private transaction primitive is not exposed by RPC until Worker
// response controls exist. The eventual owner API must authenticate/revalidate
// its principal and retain a reference-only mutation receipt around this call.
func (s *Service) acceptQuestionResponse(tx *store.Tx, responseID, interactionID domain.ID, revision uint64, input domain.QuestionResponseInput) (store.Record, error) {
	for _, id := range []domain.ID{responseID, interactionID} {
		if err := id.Validate(); err != nil {
			return store.Record{}, err
		}
	}
	if revision == 0 || s.executionAuthority == nil {
		return store.Record{}, executionEventConflict()
	}
	r, err := tx.Get(domain.InteractionKind, interactionID)
	if err != nil {
		return store.Record{}, err
	}
	value, err := store.Decode[domain.ExecutionInteraction](r)
	if err != nil {
		return store.Record{}, err
	}
	if r.Revision != revision || value.Closure != domain.InteractionOpen || value.Response != nil || value.Type != domain.UserQuestionInteraction {
		return store.Record{}, domain.Fail(domain.Conflict, "The question changed, closed or already has a response.", "Reload its original request and current response state before responding; do not replay native input.")
	}
	if err := input.Validate(value.Questions); err != nil {
		return store.Record{}, err
	}
	if _, err := s.questionResponseScope(tx, r, value); err != nil {
		return store.Record{}, err
	}
	value.Response = &domain.QuestionResponse{ID: responseID, State: domain.QuestionResponseQueued, Input: input, AcceptedAt: time.Now().UTC()}
	return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value)
}

func (s *Service) questionResponseScope(tx *store.Tx, r store.Record, value domain.ExecutionInteraction) (store.ExecutionGrant, error) {
	if s.executionAuthority == nil || r.Kind != domain.InteractionKind || value.Type != domain.UserQuestionInteraction || value.Closure != domain.InteractionOpen {
		return store.ExecutionGrant{}, executionEventConflict()
	}
	_, session, err := sessionRecord(tx, r.SessionID)
	if err != nil {
		return store.ExecutionGrant{}, err
	}
	progress := session.Execution
	if session.Outcome != domain.ExecutionRunning || progress == nil || progress.Outcome != domain.ExecutionRunning || progress.CleanupVerified || session.ActiveExecutionID != value.ExecutionID || progress.ExecutionID != value.ExecutionID || progress.NativeThreadID != value.NativeThreadID || progress.NativeTurnID != value.NativeTurnID {
		return store.ExecutionGrant{}, executionEventConflict()
	}
	job, err := tx.SessionExecutionJob(r.SessionID, value.ExecutionID)
	if err != nil {
		return store.ExecutionGrant{}, err
	}
	if job.ID != progress.JobID {
		return store.ExecutionGrant{}, executionEventConflict()
	}
	grant, err := tx.ExecutionGrantForJob(job.ID)
	if err != nil {
		return store.ExecutionGrant{}, err
	}
	// Reuse the complete live execution/account/Worker/project/epoch boundary.
	// A question cannot introduce another account, model or permission policy.
	if _, err := s.executionAuthority.scope(tx, grant); err != nil {
		return store.ExecutionGrant{}, err
	}
	return grant, nil
}
