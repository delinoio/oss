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
	if s.apiProfile != nil && s.apiProfile.WorkspaceRoot != nil && !checkpointMatchesRoot(checkpoint, *s.apiProfile.WorkspaceRoot) {
		return sessionUncertain()
	}
	if s.problem != nil || s.creation == nil || s.input != nil || s.events != nil || s.observer != nil || s.historyRead != nil || s.checkpointRead || !s.apiVerified || s.apiProfile == nil || s.creation.request != checkpoint.Reference.CreationRequestID || s.creation.identity != s.checkpointIdentity(checkpoint) || s.cwd != checkpoint.Workspace || s.runtimeRoot != checkpoint.NativeRoot || !validCheckpointHistory(checkpoint.History) {
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
	identity, err := s.validateOriginalSession(sessions[0], &creation)
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
		if s.logger != nil {
			s.logger.WarnContext(ctx, "opencode_predecessor_lineage_rejected", "owner_id", s.owner, "lineage_valid", validCheckpointLineage(checkpoint), "message_count", len(checkpointInventory(checkpoint)))
		}
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
	messages := checkpointInventory(checkpoint)
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
		if domain.Decode(raw, &page) != nil || len(page) != 1 || !s.checkpointNativeMessageMatches(page[0], messages[index]) {
			if s.logger != nil {
				s.logger.WarnContext(ctx, "opencode_predecessor_history_rejected", "owner_id", s.owner, "position", index, "page_count", len(page))
			}
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

// Native pruning may finish after the idle arrival. This independent read may
// retain only a completed tool's original timestamp-only pruning change. It
// grants no input, event, settlement or output authority. The caller already
// holds the original observer lock during final history comparison.
func (s *sessionAPI) checkpointNativeMessageMatches(raw []byte, expected HistoryMessage) bool {
	if checkpointMessageMatches(raw, expected) {
		return true
	}
	if s.observer == nil || s.input == nil || !s.observer.progress.SettledObserved || s.observer.problem != nil {
		return false
	}
	fields, err := shape(raw, []string{"info", "parts"}, nil)
	if err != nil || mutationDigest(canonicalNative(fields["info"])) != expected.Digest {
		return false
	}
	var parts []json.RawMessage
	if domain.Decode(fields["parts"], &parts) != nil || parts == nil || len(parts) != len(expected.Parts) {
		return false
	}
	candidate := s.observer.reconciliationCopy()
	for index, rawPart := range parts {
		original := expected.Parts[index]
		if mutationDigest(canonicalNative(rawPart)) == original.Digest {
			continue
		}
		part, err := decodeNativePart(rawPart)
		if err != nil || original.Kind != ToolPartKind || part.ID != original.ID || part.MessageID != expected.ID || part.SessionID != s.input.receipt.SessionID || part.Tool == nil || part.Tool.Timing == nil || part.Tool.Timing.Compacted == nil || mutationDigest(unprunedPart(rawPart)) != original.Digest {
			return false
		}
		if _, _, err := candidate.compactedToolPart(part, rawPart); err != nil {
			return false
		}
	}
	s.observer.contextPruned = candidate.contextPruned
	s.observer.parts = candidate.parts
	return true
}
