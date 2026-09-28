package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// pendingInteraction is called only while the session API's mutation gate is
// held. The native list can prove a still-pending original proposal, never
// acceptance, rejection or permission to replay an earlier response.
func (s *sessionAPI) pendingInteraction(ctx context.Context, observer *inputObserver, id string) (*NativeInteraction, error) {
	if observer == nil || observer != s.observer {
		return nil, sessionInvalid()
	}
	observer.mu.Lock()
	interaction := observer.interactions[id]
	if interaction == nil || observer.problem != nil {
		observer.mu.Unlock()
		return nil, sessionUncertain()
	}
	if interaction.closed {
		observer.mu.Unlock()
		return nil, sessionConflict()
	}
	original := slices.Clone(interaction.raw)
	kind := interaction.value.Kind
	observer.mu.Unlock()
	path, eventKind := "/permission", PermissionAskedEvent
	if kind == QuestionInteraction {
		path, eventKind = "/question", QuestionAskedEvent
	}
	raw, _, err := s.request(ctx, http.MethodGet, path, nil, http.StatusOK)
	if err != nil {
		if domain.SafeError(err).Code == domain.Unsupported {
			return nil, observer.pendingFailure(ctx)
		}
		return nil, err
	}
	var entries []json.RawMessage
	if domain.Decode(raw, &entries) != nil || entries == nil || len(entries) > maxObservedInteractions {
		return nil, observer.pendingFailure(ctx)
	}
	seen := map[string]bool{}
	var matched *NativeInteraction
	for _, entry := range entries {
		value, err := decodeNativeInteraction(eventKind, entry)
		if err != nil || seen[value.ID] {
			return nil, observer.pendingFailure(ctx)
		}
		seen[value.ID] = true
		if value.ID != id {
			continue
		}
		if !bytes.Equal(original, canonicalNative(entry)) {
			return nil, observer.pendingFailure(ctx)
		}
		matched = &value
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.problem != nil {
		return nil, observer.problem
	}
	if interaction.closed {
		// An actual native closure can race this read's older pending view.
		// The newer owned closure wins without manufacturing a contradiction.
		return nil, sessionConflict()
	}
	if matched == nil {
		// Preserve disappearance as uncertainty without blocking a later
		// original live closure. Reappearance cannot silently restore send
		// authority for a request already observed missing.
		interaction.pendingAbsent = true
		return nil, sessionUncertain()
	}
	if interaction.pendingAbsent {
		return nil, sessionUncertain()
	}
	return matched, nil
}

func (o *inputObserver) pendingFailure(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.fail(ctx, "pending-interaction", observerProblem())
}
