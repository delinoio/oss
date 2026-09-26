package opencode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type HistoryPart struct {
	ID     string
	Kind   PartKind
	Digest string
}

type HistoryMessage struct {
	ID     string
	Role   MessageRole
	Digest string
	Parts  []HistoryPart
}

// HistoryObservation is content-free comparison evidence from the original
// live process. It proves neither process closure, durable Worker checkpoint
// publication, interaction acceptance nor permission to resume or replay input.
type HistoryObservation struct {
	RequestID   domain.ID
	SessionID   string
	InputID     string
	AssistantID string
	Messages    []HistoryMessage
	Digest      string
}

type historyPageRead struct {
	path   string
	cursor string
}

func validHistoryCursor(cursor string) bool {
	if len(cursor) == 0 || len(cursor) > 2048 {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != cursor {
		return false
	}
	fields, err := shape(raw, []string{"id", "time"}, nil)
	if err != nil {
		return false
	}
	id, ok := boundedString(fields["id"], 30, true)
	_, valid := nativeCount(fields["time"])
	return ok && nativeID(id, "msg") && valid
}

type historyBoundary uint8

const (
	completedHistoryBoundary historyBoundary = iota
	stoppedHistoryBoundary
)

func (s *sessionAPI) readHistory(ctx context.Context, o *inputObserver) (HistoryObservation, error) {
	return s.readHistoryAt(ctx, o, completedHistoryBoundary)
}

// Called under the session gate and original observer lock. History uses the
// native newest-first one-message pages, comparing them to reverse original
// observation order; native ID timestamps are never treated as replay cursors.
func (s *sessionAPI) readHistoryAt(ctx context.Context, o *inputObserver, boundary historyBoundary) (HistoryObservation, error) {
	var result HistoryObservation
	if boundary != completedHistoryBoundary && boundary != stoppedHistoryBoundary || o == nil || o != s.observer || s.input == nil || s.creation == nil || s.historyRead != nil || s.events == nil || s.problem != nil {
		return result, sessionUncertain()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.problem != nil || boundary == completedHistoryBoundary && o.stop != nil || boundary == stoppedHistoryBoundary && !o.stoppedHistoryReady() || !o.progress.SettledObserved || o.progress.NeedsRecovery || o.creation.request != s.creation.request || o.creation.identity != s.creation.identity || !equalSessionSettings(o.creation.settings, s.creation.settings) || !slices.Equal(o.sessionPermissions, s.sessionPermissions) || o.cwd != s.cwd || o.input.digest != s.input.digest || o.progress.RequestID != s.input.receipt.RequestID || o.progress.SessionID != s.input.receipt.SessionID || len(o.messageOrder) < 2 || len(o.messageOrder) != len(o.messages) || o.messageOrder[0] != s.input.receipt.MessageID || o.messageOrder[len(o.messageOrder)-1] != o.progress.AssistantID {
		return result, sessionUncertain()
	}
	idle := func() error {
		if boundary == completedHistoryBoundary {
			return s.historyIdle(ctx)
		}
		if boundary == stoppedHistoryBoundary {
			return s.stoppedHistoryIdle(ctx, o)
		}
		return sessionUncertain()
	}
	phase := "original-state"
	fail := func(err error) (HistoryObservation, error) {
		if domain.SafeError(err).Code == domain.Unsupported {
			_ = o.fail(ctx, "history-"+phase, err)
		}
		s.diagnostic(ctx, SubmitInputMutation, err, "phase", "history-"+phase)
		return HistoryObservation{}, err
	}
	if err := s.events.status(); err != nil {
		return fail(err)
	}
	if _, err := s.readSession(ctx); err != nil {
		return fail(err)
	}
	receipt, err := s.readStoredInput(ctx)
	if err != nil {
		return fail(err)
	}
	if !receipt.Recorded {
		return fail(sessionUncertain())
	}
	phase = "pending"
	if err := idle(); err != nil {
		return fail(err)
	}
	result = HistoryObservation{RequestID: receipt.RequestID, SessionID: receipt.SessionID, InputID: receipt.MessageID, AssistantID: o.progress.AssistantID, Messages: make([]HistoryMessage, len(o.messageOrder))}
	base := "/session/" + receipt.SessionID + "/message?limit=1"
	path, bytesRead, partsRead := base, 0, 0
	seen := map[string]bool{}
	defer func() { s.historyRead = nil }()
	phase = "messages"
	for index := len(o.messageOrder) - 1; index >= 0; index-- {
		s.historyRead = &historyPageRead{path: path}
		raw, _, err := s.request(ctx, http.MethodGet, path, nil, http.StatusOK)
		if err != nil {
			return fail(err)
		}
		bytesRead += len(raw)
		if bytesRead > maxObservedBytes {
			return fail(eventBound())
		}
		var page []json.RawMessage
		if domain.Decode(raw, &page) != nil || len(page) != 1 {
			return fail(observerProblem())
		}
		fields, err := shape(page[0], []string{"info", "parts"}, nil)
		original := o.messages[o.messageOrder[index]]
		if err != nil || original == nil || original.value.Assistant != nil && !original.finalized || !bytes.Equal(canonicalNative(fields["info"]), original.raw) {
			return fail(observerProblem())
		}
		var parts []json.RawMessage
		if domain.Decode(fields["parts"], &parts) != nil || parts == nil || len(parts) != len(original.parts) {
			return fail(observerProblem())
		}
		message := HistoryMessage{ID: original.value.ID, Role: original.value.Role, Digest: mutationDigest(original.raw), Parts: make([]HistoryPart, len(parts))}
		for partIndex, raw := range parts {
			part := o.parts[original.parts[partIndex]]
			if part == nil || part.value.MessageID != message.ID || !bytes.Equal(canonicalNative(raw), part.raw) || part.value.Text != nil && part.text != part.value.Text.Text {
				return fail(observerProblem())
			}
			message.Parts[partIndex] = HistoryPart{ID: part.value.ID, Kind: part.value.Kind, Digest: mutationDigest(part.raw)}
		}
		partsRead += len(parts)
		result.Messages[index] = message
		cursor := s.historyRead.cursor
		if index == 0 {
			if partsRead != len(o.parts) || (s.predecessor == nil) != (cursor == "") {
				return fail(observerProblem())
			}
			if s.predecessor != nil {
				if seen[cursor] {
					return fail(observerProblem())
				}
				seen[cursor] = true
				s.historyRead = nil
				if err := s.readCheckpointMessages(ctx, *s.predecessor, cursor, bytesRead, seen); err != nil {
					return fail(err)
				}
			}
		} else {
			if cursor == "" || seen[cursor] {
				return fail(observerProblem())
			}
			// Treat the validated native cursor as an opaque page position. The
			// next request still uses the original fixed local session authority;
			// never follow a native Link URL or reuse a cursor on another session.
			seen[cursor] = true
			path = base + "&before=" + url.QueryEscape(cursor)
		}
	}
	s.historyRead = nil
	phase = "settled-after-history"
	if err := idle(); err != nil {
		return fail(err)
	}
	if _, err := s.readSession(ctx); err != nil {
		return fail(err)
	}
	if err := s.events.status(); err != nil {
		return fail(err)
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > maxObservedBytes {
		return fail(eventBound())
	}
	result.Digest = mutationDigest(raw)
	if s.logger != nil {
		s.logger.InfoContext(ctx, "OpenCode original stored history verified", "owner_id", s.owner, "request_id", receipt.RequestID, "messages", len(result.Messages), "parts", partsRead)
	}
	return result, nil
}

func (s *sessionAPI) historyIdle(ctx context.Context) error {
	for _, path := range []string{"/permission", "/question", "/session/status"} {
		raw, _, err := s.request(ctx, http.MethodGet, path, nil, http.StatusOK)
		if err != nil {
			return err
		}
		if path == "/session/status" {
			status, err := object(raw)
			if err != nil || len(status) != 0 {
				return observerProblem()
			}
		} else {
			var pending []json.RawMessage
			if domain.Decode(raw, &pending) != nil || pending == nil || len(pending) != 0 {
				return observerProblem()
			}
		}
	}
	return nil
}
