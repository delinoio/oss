// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type CompactionReceipt struct {
	ActionID           domain.ID
	InputRequestID     domain.ID
	SessionID          string
	SourceInputID      string
	NativeAttempted    bool
	HTTPAccepted       bool
	LifecycleCompleted bool
	CleanupVerified    bool
	Context            NativeContextRecord
	HistorySHA256      string
}

type nativeCompactionAttempt struct {
	mu           sync.Mutex
	action       domain.ID
	path, digest string
	attempted    bool
	accepted     bool
	problem      error
	cancel       context.CancelFunc
	done         chan struct{}
}

// StartCompaction is available only on a freshly and independently restored
// successful source. The durable claim precedes exactly one native summarize
// request with original provider/model and auto=false. Its HTTP operation runs
// concurrently with the owned event reader; cleanup cancels and joins it.
func (a *OwnedAPI) StartCompaction(ctx context.Context, action domain.ID) (CompactionReceipt, error) {
	if !a.valid() || action.Validate() != nil {
		return CompactionReceipt{}, sessionInvalid()
	}
	select {
	case a.reading <- struct{}{}:
		defer func() { <-a.reading }()
	case <-ctx.Done():
		return CompactionReceipt{}, unavailable()
	}
	s := a.session
	if err := s.enter(ctx); err != nil {
		return CompactionReceipt{}, err
	}
	source := s.predecessor
	eligible := s.compactionAttempt == nil && source != nil && !source.Reference.RequiresResume && s.resumeRequest.Validate() == nil && action != s.resumeRequest && action != source.Reference.OwnerID && action != source.Reference.CreationRequestID && action != source.Reference.InputRequestID && s.problem == nil && s.creation != nil && s.apiVerified && s.input == nil && s.events == nil && s.observer == nil && !s.eventAttempt && len(s.children) == 0 && s.apiProfile != nil && s.apiProfile.Settings.Agent == s.sessionAgent
	if !eligible {
		s.leave()
		return CompactionReceipt{}, sessionConflict()
	}
	body, err := json.Marshal(struct {
		Provider string `json:"providerID"`
		Model    string `json:"modelID"`
		Auto     bool   `json:"auto"`
	}{s.apiProfile.Settings.Provider, s.apiProfile.Settings.Model, false})
	if err != nil {
		s.leave()
		return CompactionReceipt{}, sessionInvalid()
	}
	lifetime, cancel := context.WithTimeout(ctx, 15*time.Minute)
	attempt := &nativeCompactionAttempt{cancel: cancel, action: action, path: "/session/" + source.Reference.SessionID + "/summarize", digest: mutationDigest(body), done: make(chan struct{})}
	s.compactionAttempt = attempt
	s.leave()
	if _, err = s.openEvents(lifetime); err != nil {
		close(attempt.done)
		attempt.mu.Lock()
		attempt.problem = err
		attempt.mu.Unlock()
		cancel()
		return a.CompactionReceipt(ctx)
	}
	if err = s.enter(lifetime); err != nil {
		close(attempt.done)
		attempt.mu.Lock()
		attempt.problem = err
		attempt.mu.Unlock()
		cancel()
		return CompactionReceipt{}, err
	}
	// Original input identity is retained for private comparison only. No new
	// DeliDev/native prompt, input acceptance or HTTP input acknowledgment occurs.
	digest, err := hex.DecodeString(source.Reference.InputSHA256)
	if err != nil || len(digest) != 32 {
		s.leave()
		close(attempt.done)
		return CompactionReceipt{}, sessionUncertain()
	}
	s.input = &sessionInput{receipt: InputReceipt{RequestID: source.Reference.InputRequestID, SessionID: source.Reference.SessionID, MessageID: source.Reference.InputID, PartID: source.Reference.PartID, Recorded: true}}
	copy(s.input.digest[:], digest)
	s.leave()
	observer, err := s.observeInput(lifetime, s.runtimeRoot)
	if err != nil {
		close(attempt.done)
		return CompactionReceipt{}, err
	}
	observer.mu.Lock()
	observer.contextManual = action
	observer.progress.UserSeen = true
	observer.progress.InputPartSeen = true
	observer.mu.Unlock()
	claim := SessionClaim{RequestID: action, Kind: CompactSessionMutation, SessionID: source.Reference.SessionID, MessageID: source.Reference.InputID, PartID: source.Reference.PartID, InputRequestID: source.Reference.InputRequestID, BodyDigest: attempt.digest}
	if claim.Validate() != nil || s.claim(lifetime, claim) != nil {
		close(attempt.done)
		attempt.mu.Lock()
		attempt.problem = sessionUncertain()
		attempt.mu.Unlock()
		cancel()
		return CompactionReceipt{}, sessionUncertain()
	}
	attempt.mu.Lock()
	attempt.attempted = true
	attempt.mu.Unlock()
	go func() {
		defer close(attempt.done)
		raw, _, err := s.request(lifetime, http.MethodPost, attempt.path, body, http.StatusOK)
		attempt.mu.Lock()
		defer attempt.mu.Unlock()
		attempt.accepted = err == nil && string(raw) == "true"
		if !attempt.accepted {
			if err == nil {
				err = sessionUncertain()
			}
			attempt.problem = err
		}
	}()
	return a.CompactionReceipt(ctx)
}

func (a *OwnedAPI) CompactionReceipt(ctx context.Context) (CompactionReceipt, error) {
	if !a.valid() {
		return CompactionReceipt{}, sessionInvalid()
	}
	s := a.session
	if err := s.enter(ctx); err != nil {
		return CompactionReceipt{}, err
	}
	defer s.leave()
	attempt := s.compactionAttempt
	if attempt == nil || s.predecessor == nil {
		return CompactionReceipt{}, sessionInvalid()
	}
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	ref := s.predecessor.Reference
	receipt := CompactionReceipt{ActionID: attempt.action, InputRequestID: ref.InputRequestID, SessionID: ref.SessionID, SourceInputID: ref.InputID, NativeAttempted: attempt.attempted, HTTPAccepted: attempt.accepted, CleanupVerified: a.completed != nil && a.completionAttempted}
	if s.observer != nil {
		s.observer.mu.Lock()
		defer s.observer.mu.Unlock()
		if record := s.observer.activeContext(); record != nil {
			receipt.Context = *record
			receipt.LifecycleCompleted = s.observer.contextClosed() && record.ActionID == attempt.action && record.CompletedEventID != ""
		}
	}
	if a.completed != nil {
		receipt.HistorySHA256 = a.completed.Digest
	}
	return receipt, attempt.problem
}

func (s *sessionAPI) joinCompactionHTTP(ctx context.Context, cancel bool) error {
	attempt := s.compactionAttempt
	if attempt == nil {
		return nil
	}
	attempt.mu.Lock()
	cancelHTTP := attempt.cancel
	attempt.mu.Unlock()
	if cancel && cancelHTTP != nil {
		cancelHTTP()
	}
	select {
	case <-attempt.done:
	case <-ctx.Done():
		return unavailable()
	}
	if cancel {
		return nil
	}
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	if !attempt.attempted || !attempt.accepted || attempt.problem != nil {
		return sessionUncertain()
	}
	return nil
}
