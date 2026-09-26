package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type checkpointReadPhase string

const (
	checkpointOwnershipPhase checkpointReadPhase = "ownership"
	checkpointSettingsPhase  checkpointReadPhase = "settings"
	checkpointSessionsPhase  checkpointReadPhase = "sessions"
	checkpointIdlePhase      checkpointReadPhase = "idle"
	checkpointMessagesPhase  checkpointReadPhase = "messages"
	checkpointRecheckPhase   checkpointReadPhase = "recheck"
	checkpointTodoPhase      checkpointReadPhase = "todo"
)

// This read compares all original message/part bytes through the pinned native
// API after independent runtime/configuration ownership. It cannot discover a
// session, reconstruct observations, acknowledge creation or authorize input.
func (s *sessionAPI) inspectCheckpointHistory(ctx context.Context, checkpoint nativeCheckpoint, previousAgent PrimaryAgent) (returned error) {
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.leave()
	phase := checkpointOwnershipPhase
	defer func() {
		if returned != nil && s.logger != nil {
			s.logger.WarnContext(ctx, "opencode_retained_history_comparison_failed", "owner_id", s.owner, "phase", phase, "code", domain.SafeError(returned).Code)
		}
	}()
	if s.problem != nil || s.creation == nil || s.input != nil || s.events != nil || s.observer != nil || s.historyRead != nil || s.checkpointRead || !s.apiVerified || s.apiProfile == nil || s.creation.request != checkpoint.Reference.CreationRequestID || s.creation.identity != (sessionIdentity{checkpoint.Reference.SessionID, checkpoint.Project, checkpoint.Slug, checkpoint.Created}) || s.cwd != checkpoint.Workspace || s.runtimeRoot != checkpoint.NativeRoot || !validCheckpointHistory(checkpoint.History) {
		return sessionUncertain()
	}
	phase = checkpointSettingsPhase
	settings, err := checkpointSettingsForAgent(s, previousAgent)
	if err != nil || settings != checkpoint.SettingsSHA256 {
		return sessionUncertain()
	}
	s.checkpointRead = true
	defer func() {
		s.checkpointRead, s.historyRead = false, nil
		if returned != nil {
			s.problem = sessionProblem()
		}
	}()
	// A fresh isolated original runtime cannot acquire unowned child/root
	// sessions through restoration. The bounded list must contain only its
	// independently pinned original session, even if another marker matches.
	phase = checkpointSessionsPhase
	raw, _, err := s.request(ctx, http.MethodGet, "/session?limit=2", nil, http.StatusOK)
	if err != nil {
		return err
	}
	var sessions []json.RawMessage
	if domain.Decode(raw, &sessions) != nil || len(sessions) != 1 {
		return sessionUncertain()
	}
	creation := s.sessionMetadataCreation()
	identity, err := validateSession(sessions[0], s.cwd, &creation, false)
	if err != nil || identity != s.creation.identity {
		return sessionUncertain()
	}
	if _, err := s.readSession(ctx); err != nil {
		return err
	}
	phase = checkpointIdlePhase
	if err := s.historyIdle(ctx); err != nil {
		return err
	}
	phase = checkpointMessagesPhase
	if err := s.readCheckpointMessages(ctx, checkpoint, "", 0, nil); err != nil {
		return err
	}
	phase = checkpointRecheckPhase
	if err := s.historyIdle(ctx); err != nil {
		return err
	}
	if _, err := s.readSession(ctx); err != nil {
		return err
	}
	phase = checkpointTodoPhase
	if err := s.compareTodoHistory(ctx, latestCheckpointTodo(checkpoint)); err != nil {
		return err
	}
	if s.logger != nil {
		s.logger.InfoContext(ctx, "opencode_retained_history_compared", "owner_id", s.owner, "original_request_id", checkpoint.Reference.InputRequestID, "inputs", len(checkpoint.Previous)+1)
	}
	return nil
}

// Read the complete original ordered history without treating IDs as cursors
// or following native Link URLs. No historical payload leaves this comparison.
func (s *sessionAPI) readCheckpointMessages(ctx context.Context, checkpoint nativeCheckpoint, cursor string, size int, seen map[string]bool) error {
	if s.historyRead != nil || !validCheckpointLineage(checkpoint) || s.creation == nil || checkpoint.Reference.SessionID != s.creation.identity.id {
		return sessionUncertain()
	}
	defer func() { s.historyRead = nil }()
	base := "/session/" + checkpoint.Reference.SessionID + "/message?limit=1"
	path := base
	if cursor != "" {
		if !validHistoryCursor(cursor) {
			return sessionUncertain()
		}
		path += "&before=" + url.QueryEscape(cursor)
	}
	if seen == nil {
		seen = map[string]bool{}
	}
	var messages []HistoryMessage
	for _, history := range checkpointHistories(checkpoint) {
		messages = append(messages, history.Messages...)
	}
	for index := len(messages) - 1; index >= 0; index-- {
		s.historyRead = &historyPageRead{path: path}
		raw, _, err := s.request(ctx, http.MethodGet, path, nil, http.StatusOK)
		if err != nil {
			return err
		}
		size += len(raw)
		if size > maxObservedBytes || ctx.Err() != nil {
			return sessionUncertain()
		}
		var page []json.RawMessage
		if domain.Decode(raw, &page) != nil || len(page) != 1 || !checkpointMessageMatches(page[0], messages[index]) {
			return sessionUncertain()
		}
		cursor := s.historyRead.cursor
		if index == 0 {
			if cursor != "" {
				return sessionUncertain()
			}
		} else {
			if cursor == "" || seen[cursor] {
				return sessionUncertain()
			}
			seen[cursor] = true
			path = base + "&before=" + url.QueryEscape(cursor)
		}
	}
	return nil
}

func checkpointMessageMatches(raw []byte, expected HistoryMessage) bool {
	fields, err := shape(raw, []string{"info", "parts"}, nil)
	if err != nil || mutationDigest(canonicalNative(fields["info"])) != expected.Digest {
		return false
	}
	var parts []json.RawMessage
	if domain.Decode(fields["parts"], &parts) != nil || parts == nil || len(parts) != len(expected.Parts) {
		return false
	}
	for index, part := range parts {
		if mutationDigest(canonicalNative(part)) != expected.Parts[index].Digest {
			return false
		}
	}
	return true
}
