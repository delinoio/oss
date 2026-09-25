package server

import (
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// The owner API authenticates/revalidates its principal and retains a
// reference-only receipt around this transaction, independently of native send.
func (s *Service) acceptApprovalResponse(tx *store.Tx, responseID, interactionID domain.ID, revision uint64, input domain.ApprovalResponseInput) (store.Record, error) {
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
	if r.Revision != revision || value.Closure != domain.InteractionOpen || (value.ApprovalResponse != nil || value.Response != nil) || value.Type != domain.NativeApprovalInteraction {
		return store.Record{}, domain.Fail(domain.Conflict, "The approval changed, closed or already has a response.", "Reload its original request and current response state before responding; do not replay native input.")
	}
	if err := input.Validate(value.Approval); err != nil {
		return store.Record{}, err
	}
	if _, err := s.approvalResponseScope(tx, r, value); err != nil {
		return store.Record{}, err
	}
	// The current live scope accepts the pinned Codex profile only. Account for
	// its per-approval native response wrapper before acceptance, not after the
	// Worker has claimed a response that cannot fit on the native wire.
	if _, err := codex.PrepareApprovalResponse(value.Approval, input); err != nil {
		return store.Record{}, err
	}
	value.ApprovalResponse = &domain.ApprovalResponse{ID: responseID, State: domain.ApprovalResponseQueued, Input: input, AcceptedAt: time.Now().UTC()}
	return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value)
}

func (s *Service) approvalResponseScope(tx *store.Tx, r store.Record, value domain.ExecutionInteraction) (store.ExecutionGrant, error) {
	if value.Type != domain.NativeApprovalInteraction || value.Approval == nil || value.Approval.Validate() != nil {
		return store.ExecutionGrant{}, executionEventConflict()
	}
	grant, err := s.interactionResponseScope(tx, r, value)
	if err != nil {
		return grant, err
	}
	return grant, nil
}
