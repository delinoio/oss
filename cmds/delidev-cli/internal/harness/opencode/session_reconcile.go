package opencode

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (s *sessionAPI) creationReceipt() SessionReceipt {
	if s.creation == nil {
		return SessionReceipt{}
	}
	c := s.creation
	return SessionReceipt{RequestID: c.request, SessionID: c.identity.id, HTTPAccepted: c.acknowledged, Recorded: c.recorded}
}

// Only the original private fresh runtime can reconcile its once-attempted
// creation. A second candidate is a contradiction even if only one marker
// matches: this scope has no authority to create or adopt other sessions.
// Read retries retain missing evidence; they never retry the native mutation.
func (s *sessionAPI) reconcileCreation(ctx context.Context) (receipt SessionReceipt, returned error) {
	if err := s.enter(ctx); err != nil {
		return SessionReceipt{}, err
	}
	defer s.leave()
	phase := "claim"
	defer func() {
		receipt = s.creationReceipt()
		if returned != nil {
			s.diagnostic(ctx, CreateSessionMutation, returned, "phase", phase)
		}
	}()
	if s.creation == nil || !s.creation.attempted || s.input != nil || s.creationLookup || s.creationCandidate != "" {
		return receipt, sessionUncertain()
	}
	if s.problem != nil {
		return receipt, s.problem
	}
	if s.apiProfile != nil && (!s.apiVerified || !equalSessionSettings(s.creation.settings, s.apiProfile.Settings)) {
		return receipt, sessionProblem()
	}
	if err := s.verifyInstructions(ctx, "creation-reconciliation"); err != nil {
		return receipt, err
	}
	if s.creation.identity.id != "" {
		phase = "original-session"
		if _, err := s.readSession(ctx); err != nil {
			return receipt, err
		}
		s.creation.recorded = true
		return receipt, nil
	}
	s.creationLookup = true
	defer func() { s.creationLookup, s.creationCandidate = false, "" }()
	contradiction := func() error {
		s.problem = sessionProblem()
		return s.problem
	}
	phase = "creation-inventory"
	raw, _, err := s.request(ctx, http.MethodGet, "/session?limit=2", nil, http.StatusOK)
	if err != nil {
		return receipt, sessionUncertain()
	}
	var sessions []json.RawMessage
	if domain.Decode(raw, &sessions) != nil || sessions == nil || len(sessions) > 1 {
		return receipt, contradiction()
	}
	if len(sessions) == 0 {
		return receipt, sessionUncertain()
	}
	identity, err := validateSession(sessions[0], s.cwd, s.creation, true)
	if err != nil || !s.validRootProject(identity) || !unusedCreation(sessions[0]) {
		return receipt, contradiction()
	}
	s.creationCandidate = identity.id
	phase = "creation-history"
	raw, _, err = s.request(ctx, http.MethodGet, "/session/"+identity.id+"/message?limit=1", nil, http.StatusOK)
	if err != nil {
		return receipt, sessionUncertain()
	}
	var messages []json.RawMessage
	if domain.Decode(raw, &messages) != nil || messages == nil || len(messages) != 0 {
		return receipt, contradiction()
	}
	phase = "creation-status"
	raw, _, err = s.request(ctx, http.MethodGet, "/session/status", nil, http.StatusOK)
	if err != nil {
		return receipt, sessionUncertain()
	}
	statuses, err := object(raw)
	if err != nil || len(statuses) != 0 {
		return receipt, contradiction()
	}
	phase = "creation-identity"
	raw, _, err = s.request(ctx, http.MethodGet, "/session/"+identity.id, nil, http.StatusOK)
	if err != nil {
		return receipt, sessionUncertain()
	}
	confirmed, err := validateSession(raw, s.cwd, s.creation, true)
	if err != nil || confirmed != identity || !unusedCreation(raw) {
		return receipt, contradiction()
	}
	// Publish identity only after all original fresh-session checks. Missing
	// acknowledgment remains missing even when storage proves creation.
	s.creation.identity, s.creation.recorded = confirmed, true
	if s.logger != nil {
		s.logger.InfoContext(ctx, "OpenCode original session creation reconciled", "owner_id", s.owner, "request_id", s.creation.request, "http_accepted", s.creation.acknowledged)
	}
	return receipt, nil
}

func unusedCreation(raw []byte) bool {
	fields, err := object(raw)
	if err != nil || fields["summary"] != nil || string(fields["cost"]) != "0" {
		return false
	}
	return exactPrivateJSON(fields["tokens"], map[string]any{"input": 0, "output": 0, "reasoning": 0, "cache": map[string]any{"read": 0, "write": 0}}) == nil
}
