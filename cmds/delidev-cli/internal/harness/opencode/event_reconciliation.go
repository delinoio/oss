package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// ReconciliationState is local observation state, never new execution authority.
type ReconciliationState string

const (
	ReconciliationOriginal   ReconciliationState = ""
	ReconciliationReading    ReconciliationState = "reading"
	ReconciliationPublishing ReconciliationState = "publishing"
	ReconciliationComplete   ReconciliationState = "complete"
	ReconciliationFailed     ReconciliationState = "failed"
)

type reconciliationMessage struct {
	info  json.RawMessage
	parts []json.RawMessage
}
type reconciliationSnapshot struct {
	messages []reconciliationMessage
	pending  [][]json.RawMessage
	idle     bool
}

func (s *sessionAPI) nextRecovered(ctx context.Context) (inputObservation, bool, error) {
	if err := s.enter(ctx); err != nil {
		return inputObservation{}, false, err
	}
	defer s.leave()
	if len(s.recovered) == 0 {
		return inputObservation{}, false, nil
	}
	s.reconciliationMu.Lock()
	denied := s.reconciliationDenied
	s.reconciliationMu.Unlock()
	if denied || s.observer.ctx.Err() != nil || ctx.Err() != nil || s.events.status() != nil || s.observer.stop != nil {
		s.recovered, s.recoveredObserver = nil, nil
		s.reconciliation = ReconciliationFailed
		return inputObservation{}, true, s.observer.interruption(ctx)
	}
	value := s.recovered[0]
	s.recovered[0] = inputObservation{}
	s.recovered = s.recovered[1:]
	if len(s.recovered) == 0 {
		s.observer, s.recoveredObserver = s.recoveredObserver, nil
		s.reconciliation = ReconciliationComplete
		s.reconciliationLog(ctx, "completed", nil)
	}
	return value, true, nil
}

func (s *sessionAPI) cancelReconciliation() {
	s.reconciliationMu.Lock()
	defer s.reconciliationMu.Unlock()
	s.reconciliationDenied = true
	if s.reconciliationCancel != nil {
		s.reconciliationCancel()
	}
}

// Called under the original read/session gates around independent history
// verification. Late arrivals must still be covered by the recovered facts;
// terminal progress cannot hide a contradictory or unsupported queued event.
func (s *sessionAPI) verifyRecoveredTail(ctx context.Context, o *inputObserver) error {
	if s.reconciliation != ReconciliationComplete {
		return nil
	}
	s.events.mu.Lock()
	var events []NativeEvent
	for len(s.events.queue) > 0 {
		event := <-s.events.queue
		s.events.pending -= len(event.Properties)
		events = append(events, event)
	}
	problem := s.events.problem
	s.events.mu.Unlock()
	if problem != nil || ctx.Err() != nil {
		return o.interruption(ctx)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, event := range events {
		if len(event.Properties) > maxObservedBytes-o.bytes {
			return o.fail(ctx, "reconciliation-tail", eventBound())
		}
		if err := o.coveredReconciliationEvent(event); err != nil {
			return o.fail(ctx, "reconciliation-tail", err)
		}
		o.bytes += len(event.Properties)
	}
	return nil
}

// Next owns the read gate, and this cycle owns the session gate. Replies cannot
// race its reads or consume another claim. Stop cancels the cycle before waiting
// for that gate; original process cleanup remains a separate joined operation.
func (s *sessionAPI) reconcileEvents(ctx context.Context, old *eventStream, o *inputObserver) (returned error) {
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.leave()
	old.mu.Lock()
	eligible := old.problem != nil && old.failure == streamTransport && !old.closing && old.parent != nil && old.parent.Err() == nil
	deadline, seen := old.failedAt.Add(eventIdleLimit), maps.Clone(old.seen)
	old.mu.Unlock()
	if !eligible || s.reconciliation != ReconciliationOriginal || s.problem != nil || s.observer != o || s.events != old || s.verifyStreamOwner == nil || !s.apiVerified || s.apiProfile == nil || len(s.children) != 0 || len(s.earlyChildren) != 0 {
		return o.interruption(ctx)
	}
	o.mu.Lock()
	valid := o.problem == nil && o.stop == nil && o.currentRetry == nil && o.ctx.Err() == nil
	if valid {
		o.progress.Reconciliation = ReconciliationReading
		o.progress.SettledObserved = false
	}
	o.mu.Unlock()
	if !valid {
		return o.interruption(ctx)
	}
	s.reconciliation = ReconciliationReading
	s.reconciliationReadBytes = 0
	bounded, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	stopLifetime := context.AfterFunc(old.parent, cancel)
	defer stopLifetime()
	s.reconciliationMu.Lock()
	if s.reconciliationDenied {
		s.reconciliationMu.Unlock()
		s.reconciliation = ReconciliationFailed
		return o.interruption(ctx)
	}
	s.reconciliationCancel = cancel
	s.reconciliationMu.Unlock()
	defer func() {
		s.reconciliationMu.Lock()
		s.reconciliationCancel = nil
		s.reconciliationMu.Unlock()
		if returned != nil {
			s.reconciliationLog(ctx, "failed", returned)
			s.reconciliation = ReconciliationFailed
			o.mu.Lock()
			o.progress.Reconciliation = ReconciliationFailed
			_ = o.fail(ctx, "reconciliation", sessionUncertain())
			o.mu.Unlock()
			if s.events != old {
				s.events.Close()
			}
		}
	}()
	s.reconciliationLog(ctx, "started", nil)
	old.Close()
	if err := s.verifyReconciliationOwner(bounded); err != nil {
		return err
	}
	stream, err := s.connectEvents(old.parent, bounded, seen)
	if err != nil {
		return err
	}
	s.events = stream
	var previous []byte
	var events []NativeEvent
	eventBytes := 0
	for bounded.Err() == nil {
		snapshot, raw, err := s.readReconciliationSnapshot(bounded)
		if err != nil {
			return err
		}
		stream.mu.Lock()
		for len(stream.queue) > 0 {
			event := <-stream.queue
			stream.pending -= len(event.Properties)
			eventBytes += len(event.Properties)
			events = append(events, event)
		}
		problem := stream.problem
		stream.mu.Unlock()
		if problem != nil {
			return problem
		}
		if len(events) > maxQueuedEvents || eventBytes > maxQueuedEventBytes {
			return eventBound()
		}
		if snapshot.idle && bytes.Equal(previous, raw) {
			candidate, observations, err := o.joinReconciliation(snapshot, events)
			if err != nil {
				return err
			}
			if err := s.verifyReconciliationOwner(bounded); err != nil {
				return err
			}
			if err := stream.status(); err != nil {
				return err
			}
			stream.mu.Lock()
			// Ownership revalidation may itself overlap late original events.
			// Validate that last bounded batch before making any facts visible.
			for len(stream.queue) > 0 {
				event := <-stream.queue
				stream.pending -= len(event.Properties)
				eventBytes += len(event.Properties)
				events = append(events, event)
				if len(events) > maxQueuedEvents || eventBytes > maxQueuedEventBytes {
					stream.mu.Unlock()
					return eventBound()
				}
				if err := candidate.coveredReconciliationEvent(event); err != nil {
					stream.mu.Unlock()
					return err
				}
			}
			problem = stream.problem
			stream.mu.Unlock()
			if problem != nil {
				return problem
			}
			if bounded.Err() != nil {
				return sessionUncertain()
			}
			// No candidate facts become public until every snapshot and buffered event
			// has been validated. Progress stays blocked while publication drains.
			s.recovered, s.recoveredObserver = observations, candidate
			s.reconciliation = ReconciliationPublishing
			o.mu.Lock()
			o.progress.Reconciliation = ReconciliationPublishing
			o.mu.Unlock()
			s.reconciliationLog(ctx, "verified", nil)
			return nil
		}
		previous = raw
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-bounded.Done():
			timer.Stop()
			return sessionUncertain()
		case <-timer.C:
		}
	}
	return sessionUncertain()
}

func (s *sessionAPI) reconciliationLog(ctx context.Context, phase string, err error) {
	if s.logger != nil {
		code := domain.Code("")
		if err != nil {
			code = domain.SafeError(err).Code
		}
		s.logger.InfoContext(ctx, "opencode_stream_reconciliation", "owner_id", s.owner, "phase", phase, "code", code)
	}
}

// Recheck original process birth/authenticated authority and complete effective
// configuration, including account relay selection and private instruction and
// reference bytes. The constructor's closure cannot adopt a replacement PID.
func (s *sessionAPI) verifyReconciliationOwner(ctx context.Context) error {
	if ctx.Err() != nil {
		return sessionUncertain()
	}
	if err := s.verifyStreamOwner(ctx); err != nil {
		return err
	}
	if err := s.verifyInstructions(ctx, "event-reconciliation"); err != nil {
		return err
	}
	s.reconciliationRead = true
	defer func() { s.reconciliationRead = false }()
	paths := map[string]any{"home": s.runtimeHome, "state": filepath.Join(s.runtimeHome, "state", "opencode"), "config": filepath.Join(s.runtimeHome, "config", "opencode"), "worktree": s.runtimeRoot, "directory": s.cwd}
	for _, step := range []struct {
		path     string
		validate func([]byte) error
	}{
		{"/config", s.apiProfile.validateConfig}, {"/provider", s.apiProfile.validateProvider},
		{"/path", func(raw []byte) error { return exactPrivateJSON(raw, paths) }},
		{"/agent", func(raw []byte) error {
			return validatePrimaryAgent(raw, s.creation.settings.Agent, s.runtimeHome, s.runtimeRoot, s.apiProfile.References...)
		}},
		{"/config", s.apiProfile.validateConfig},
	} {
		raw, _, err := s.request(ctx, http.MethodGet, step.path, nil, http.StatusOK)
		if err != nil {
			return err
		}
		if err := step.validate(raw); err != nil {
			return err
		}
	}
	_, err := s.readSession(ctx)
	return err
}

// Read only the original input's history, newest-first through fixed one-row
// pages. Older checkpoint lineage is compared without importing its events.
func (s *sessionAPI) readReconciliationSnapshot(ctx context.Context) (reconciliationSnapshot, []byte, error) {
	var result reconciliationSnapshot
	if _, err := s.readSession(ctx); err != nil {
		return result, nil, err
	}
	receipt, err := s.readStoredInput(ctx)
	if err != nil {
		return result, nil, err
	}
	if !receipt.Recorded {
		return result, nil, sessionUncertain()
	}
	total, parts := 0, 0
	read := func(path string) ([]byte, error) {
		raw, _, err := s.request(ctx, http.MethodGet, path, nil, http.StatusOK)
		total += len(raw)
		if total > maxObservedBytes {
			return nil, eventBound()
		}
		return raw, err
	}
	status, err := read("/session/status")
	if err != nil {
		return result, nil, err
	}
	statuses, err := object(status)
	if err != nil || len(statuses) > 1 {
		return result, nil, observerProblem()
	}
	result.idle = len(statuses) == 0
	if !result.idle {
		state, ok := statuses[receipt.SessionID]
		fields, err := shape(state, []string{"type"}, nil)
		if !ok || err != nil || !scalar(fields["type"], "busy") {
			return result, nil, observerProblem()
		}
	}
	for _, path := range []string{"/permission", "/question"} {
		raw, err := read(path)
		if err != nil {
			return result, nil, err
		}
		var entries []json.RawMessage
		if domain.Decode(raw, &entries) != nil || entries == nil || len(entries) > maxObservedInteractions {
			return result, nil, observerProblem()
		}
		result.pending = append(result.pending, entries)
	}
	base := "/session/" + receipt.SessionID + "/message?limit=1"
	path, seen := base, map[string]bool{}
	defer func() { s.historyRead = nil }()
	for len(result.messages) < maxObservedMessages {
		s.historyRead = &historyPageRead{path: path}
		raw, err := read(path)
		if err != nil {
			return result, nil, err
		}
		var page []json.RawMessage
		if domain.Decode(raw, &page) != nil || len(page) != 1 {
			return result, nil, observerProblem()
		}
		fields, err := shape(page[0], []string{"info", "parts"}, nil)
		if err != nil {
			return result, nil, err
		}
		message, err := decodeNativeMessage(fields["info"])
		if err != nil || message.SessionID != receipt.SessionID || seen[message.ID] {
			return result, nil, observerProblem()
		}
		seen[message.ID] = true
		var nativeParts []json.RawMessage
		if domain.Decode(fields["parts"], &nativeParts) != nil || nativeParts == nil {
			return result, nil, observerProblem()
		}
		parts += len(nativeParts)
		if parts > maxObservedParts {
			return result, nil, eventBound()
		}
		result.messages = append(result.messages, reconciliationMessage{canonicalNative(fields["info"]), nativeParts})
		cursor := s.historyRead.cursor
		if message.ID == receipt.MessageID {
			if (s.predecessor == nil) != (cursor == "") {
				return result, nil, observerProblem()
			}
			if s.predecessor != nil {
				s.historyRead = nil
				if err := s.readCheckpointMessages(ctx, *s.predecessor, cursor, total, map[string]bool{cursor: true}); err != nil {
					return result, nil, err
				}
			}
			slices.Reverse(result.messages)
			// Canonical encoding closes the whole repeated observation, not only IDs.
			canonical := []any{canonicalNative(status), result.pending}
			for _, message := range result.messages {
				canonical = append(canonical, []any{message.info, message.parts})
			}
			raw, _ = json.Marshal(canonical)
			return result, canonicalNative(raw), nil
		}
		if cursor == "" || seen[cursor] {
			return result, nil, observerProblem()
		}
		seen[cursor] = true
		path = base + "&before=" + url.QueryEscape(cursor)
	}
	return result, nil, eventBound()
}

func (o *inputObserver) reconciliationCopy() *inputObserver {
	// Called with o.mu held. Copy every mutable comparison owner; failed joining
	// must not change a published prefix, response receipt, or usage observation.
	c := &inputObserver{creation: o.creation, input: o.input, cwd: o.cwd, root: o.root, logger: o.logger, owner: o.owner,
		seen: maps.Clone(o.seen), bytes: o.bytes, messages: map[string]*observedMessage{}, messageOrder: slices.Clone(o.messageOrder),
		parts: map[string]*observedPart{}, attachments: maps.Clone(o.attachments), calls: maps.Clone(o.calls), progress: o.progress,
		ctx: o.ctx, cancel: o.cancel, interactions: map[string]*observedInteraction{}, responseIDs: maps.Clone(o.responseIDs),
		alwaysOrder: slices.Clone(o.alwaysOrder), sessionPermissions: slices.Clone(o.sessionPermissions), rejectionPolicy: o.rejectionPolicy, retries: slices.Clone(o.retries)}
	for id, m := range o.messages {
		v := *m
		v.raw = slices.Clone(m.raw)
		v.base = slices.Clone(m.base)
		v.parts = slices.Clone(m.parts)
		v.value, _ = decodeNativeMessage(v.raw)
		c.messages[id] = &v
	}
	for id, p := range o.parts {
		v := *p
		v.raw = slices.Clone(p.raw)
		v.value, _ = decodeNativePart(v.raw)
		c.parts[id] = &v
	}
	for id, i := range o.interactions {
		v := *i
		v.raw = slices.Clone(i.raw)
		kind := PermissionAskedEvent
		if i.value.Kind == QuestionInteraction {
			kind = QuestionAskedEvent
		}
		v.value, _ = decodeNativeInteraction(kind, v.raw)
		v.rejectionSources = slices.Clone(i.rejectionSources)
		v.alwaysObservations = slices.Clone(i.alwaysObservations)
		if i.attempt != nil {
			a := *i.attempt
			a.body = slices.Clone(a.body)
			v.attempt = &a
		}
		c.interactions[id] = &v
	}
	if o.todo != nil {
		v := *o.todo
		c.todo = &v
	}
	return c
}

func snapshotObservation(kind EventKind, properties any, finalized bool) inputObservation {
	raw, _ := json.Marshal(properties)
	event := NativeEvent{Kind: kind, Properties: canonicalNative(raw)}
	frozen := freezeObservation(event, inputObservation{Kind: kind, MessageFinalized: finalized})
	frozen.snapshot = true
	value, _ := frozen.Thaw()
	return value
}

func (o *inputObserver) joinReconciliation(snapshot reconciliationSnapshot, events []NativeEvent) (*inputObserver, []inputObservation, error) {
	o.mu.Lock()
	c := o.reconciliationCopy()
	o.mu.Unlock()
	c.snapshotJoining = true
	var observations []inputObservation
	// Only actual original reply events can resolve a pending delivery. Neither
	// empty inventories nor completed tools fabricate an acceptance event/receipt.
	for _, event := range events {
		if event.Kind == PermissionRepliedEvent || event.Kind == QuestionRepliedEvent || event.Kind == QuestionRejectedEvent {
			value, err := c.observe(context.Background(), event)
			if err != nil {
				return nil, nil, err
			}
			observations = append(observations, value)
		}
	}
	for index, message := range snapshot.messages {
		native, err := decodeNativeMessage(message.info)
		if err != nil {
			return nil, nil, err
		}
		prior := c.messages[native.ID]
		if index < len(c.messageOrder) && c.messageOrder[index] != native.ID {
			return nil, nil, observerProblem()
		}
		if prior != nil && prior.value.User != nil && !bytes.Equal(prior.raw, message.info) {
			return nil, nil, observerProblem()
		}
		// Existing final metadata is applied after parts. A new row may already be
		// complete, but remains provisional until its complete part list is checked.
		initial := message.info
		if prior != nil {
			initial = prior.raw
		}
		if _, _, err := c.message(initial); err != nil {
			return nil, nil, err
		}
		observations = append(observations, snapshotObservation(MessageUpdatedEvent, map[string]any{"info": json.RawMessage(initial)}, prior != nil && prior.finalized))
		owner := c.messages[native.ID]
		originalParts := slices.Clone(owner.parts)
		seenParts := map[string]bool{}
		for partIndex, raw := range message.parts {
			part, err := decodeNativePart(raw)
			if err != nil || part.MessageID != native.ID || seenParts[part.ID] {
				return nil, nil, observerProblem()
			}
			seenParts[part.ID] = true
			if partIndex < len(originalParts) && originalParts[partIndex] != part.ID {
				return nil, nil, observerProblem()
			}
			if _, _, err := c.part(raw); err != nil {
				return nil, nil, err
			}
			observations = append(observations, snapshotObservation(MessagePartUpdatedEvent, map[string]any{"part": json.RawMessage(raw)}, false))
		}
		if len(message.parts) < len(originalParts) {
			return nil, nil, observerProblem()
		}
		if native.Assistant != nil {
			if _, _, err := c.message(message.info); err != nil {
				return nil, nil, err
			}
			if native.Assistant.Completed == nil || !owner.finalized {
				return nil, nil, sessionUncertain()
			}
			observations = append(observations, snapshotObservation(MessageUpdatedEvent, map[string]any{"info": message.info}, true))
		}
	}
	if len(snapshot.messages) != len(c.messageOrder) {
		return nil, nil, observerProblem()
	}
	for index, entries := range snapshot.pending {
		kind := PermissionAskedEvent
		if index == 1 {
			kind = QuestionAskedEvent
		}
		seen := map[string]bool{}
		for _, raw := range entries {
			value, err := decodeNativeInteraction(kind, raw)
			if err != nil || value.SessionID != c.input.receipt.SessionID || seen[value.ID] {
				return nil, nil, observerProblem()
			}
			seen[value.ID] = true
			original := c.interactions[value.ID]
			if original == nil || original.closed || !bytes.Equal(original.raw, canonicalNative(raw)) {
				return nil, nil, sessionUncertain()
			}
		}
	}
	for _, interaction := range c.interactions {
		if !interaction.closed {
			return nil, nil, sessionUncertain()
		}
	}
	for _, event := range events {
		if event.Kind == PermissionRepliedEvent || event.Kind == QuestionRepliedEvent || event.Kind == QuestionRejectedEvent {
			continue
		}
		if err := c.coveredReconciliationEvent(event); err != nil {
			return nil, nil, err
		}
	}
	c.progress.Status = NativeStatusIdle
	c.snapshotJoining = false
	c.progress.IdleReconciled = true
	c.progress.NeedsRecovery = false
	c.progress.Reconciliation = ReconciliationComplete
	c.refresh()
	if !c.progress.SettledObserved {
		return nil, nil, sessionUncertain()
	}
	size := 0
	for _, value := range observations {
		frozen, err := value.Freeze()
		if err != nil {
			return nil, nil, err
		}
		size += frozen.Bytes()
	}
	if size > maxObservedBytes-c.bytes {
		return nil, nil, eventBound()
	}
	c.bytes += size
	return c, observations, nil
}

// Buffered stream updates may be earlier than the repeated final snapshots.
// Validate their original identity and monotonic relation, retain their true IDs,
// and publish the current snapshot once. No missed text delta is reconstructed.
func (o *inputObserver) coveredReconciliationEvent(event NativeEvent) error {
	if o.seen[event.ID] || len(o.seen) >= maxEventIdentities {
		return observerProblem()
	}
	fields, err := object(event.Properties)
	if err != nil {
		return observerProblem()
	}
	switch event.Kind {
	case MessageUpdatedEvent:
		if _, err := shape(event.Properties, []string{"sessionID", "info"}, nil); err != nil || !scalar(fields["sessionID"], o.input.receipt.SessionID) {
			return observerProblem()
		}
		value, err := decodeNativeMessage(fields["info"])
		if err != nil {
			return err
		}
		current := o.messages[value.ID]
		if current == nil || !bytes.Equal(messageBase(fields["info"], value.Role), current.base) {
			return observerProblem()
		}
		if value.Assistant != nil && value.Assistant.Completed != nil && !bytes.Equal(canonicalNative(fields["info"]), current.raw) && !contentFilterRefinement(canonicalNative(fields["info"]), current.raw) {
			return observerProblem()
		}
	case MessagePartUpdatedEvent:
		if _, err := shape(event.Properties, []string{"sessionID", "part", "time"}, nil); err != nil || !scalar(fields["sessionID"], o.input.receipt.SessionID) {
			return observerProblem()
		}
		if _, valid := nativeCount(fields["time"]); !valid {
			return observerProblem()
		}
		value, err := decodeNativePart(fields["part"])
		if err != nil {
			return err
		}
		current := o.parts[value.ID]
		if current == nil || value.SessionID != o.input.receipt.SessionID || value.MessageID != current.value.MessageID || value.Kind != current.value.Kind {
			return observerProblem()
		}
		if bytes.Equal(canonicalNative(fields["part"]), current.raw) {
			break
		}
		if value.Text != nil && current.value.Text != nil {
			if value.Text.Timing == nil || value.Text.Timing.End != nil || value.Text.Timing.Start != current.value.Text.Timing.Start || !strings.HasPrefix(current.text, value.Text.Text) {
				return observerProblem()
			}
		} else if value.Tool != nil && current.value.Tool != nil {
			old := *current
			old.value = value
			if err := o.tool(current.value, &old); err != nil {
				return err
			}
		} else {
			return observerProblem()
		}
	case MessagePartDeltaEvent:
		if _, err := shape(event.Properties, []string{"sessionID", "messageID", "partID", "field", "delta"}, nil); err != nil || !scalar(fields["sessionID"], o.input.receipt.SessionID) || !scalar(fields["field"], "text") {
			return observerProblem()
		}
		id, _ := boundedString(fields["partID"], 30, true)
		text, valid := boundedString(fields["delta"], maxHTTPBody, false)
		part := o.parts[id]
		if !valid || part == nil || part.value.Text == nil || !scalar(fields["messageID"], part.value.MessageID) || !strings.Contains(part.text, text) {
			return observerProblem()
		}
	case SessionStatusEvent, SessionIdleEvent, SessionUpdatedEvent, ServerHeartbeatEvent, LspUpdatedEvent, ModelsDevRefreshedEvent, CatalogUpdatedEvent, ReferenceUpdatedEvent, IntegrationUpdatedEvent, ProjectDirectoriesUpdatedEvent, PluginAddedEvent, IntegrationConnectionUpdatedEvent:
		// Validate ancillary shapes using a throwaway live observer without changing
		// the independently read idle boundary or accepting contradictory activity.
		copy := o.reconciliationCopy()
		copy.progress.SettledObserved = false
		if _, err := copy.observe(context.Background(), event); err != nil {
			return err
		}
	default:
		return observerProblem()
	}
	o.seen[event.ID] = true
	return nil
}
