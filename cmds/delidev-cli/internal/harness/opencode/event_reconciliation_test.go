package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type reconciliationFixture struct {
	*historyFixture
	opened  atomic.Int32
	checks  atomic.Int32
	reads   atomic.Int32
	change  string
	stream  *io.PipeWriter
	overlap []NativeEvent
}

func newReconciliationFixture(t *testing.T) *reconciliationFixture {
	t.Helper()
	stored := newObserverFixture(t)
	stored.start()
	stored.part(stored.assistantPart(StepStartPartKind, 100, nil))
	text := stored.assistantPart(TextPartKind, 102, map[string]any{"text": "prefix", "time": map[string]any{"start": 1236}})
	stored.part(text)
	live := stored.o.reconciliationCopy()
	text["text"] = "prefix and final suffix"
	text["time"].(map[string]any)["end"] = 1245
	stored.part(text)
	stored.part(stored.assistantPart(StepFinishPartKind, 101, map[string]any{"reason": FinishStop, "tokens": stored.a["tokens"], "cost": 0}))
	stored.a["finish"] = FinishStop
	stored.a["time"].(map[string]any)["completed"] = 1250
	stored.message(stored.a)
	stored.status(NativeStatusIdle)
	stored.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
	f := &reconciliationFixture{historyFixture: &historyFixture{t: t, o: stored.o}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	body, writer := io.Pipe()
	stream := &eventStream{ctx: ctx, parent: ctx, body: body, cancel: func() { _ = writer.Close(); _ = body.Close() }, queue: make(chan NativeEvent, maxQueuedEvents), done: make(chan struct{}), seen: map[string]bool{}, alive: func() error { return nil }}
	stream.failAs(unavailable(), streamTransport)
	close(stream.queue)
	close(stream.done)
	creation, input := live.creation, live.input
	session := &sessionAPI{creation: &creation, input: &input, observer: live, events: stream, eventAttempt: true, gate: make(chan struct{}, 1), cwd: live.cwd, runtimeRoot: live.root, runtimeHome: filepath.Join(t.TempDir(), "runtime"), password: "private-http-secret", origin: "http://127.0.0.1:1", alive: func() error { return nil }, apiProfile: fixtureAPIProfile(), apiVerified: true}
	session.client = &http.Client{Transport: f}
	session.verifyStreamOwner = func(context.Context) error {
		f.checks.Add(1)
		if f.change == "owner" {
			return sessionUncertain()
		}
		return nil
	}
	f.api = &OwnedAPI{session: session, reading: make(chan struct{}, 1)}
	t.Cleanup(func() {
		if session.events != stream {
			session.events.Close()
		}
	})
	return f
}

func (f *reconciliationFixture) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet {
		f.t.Fatal("reconciliation granted mutation authority")
	}
	user, password, ok := r.BasicAuth()
	if !ok || user != "delidev" || password != f.api.session.password || r.Header.Get("x-opencode-directory") != f.o.cwd || r.Header.Get("Last-Event-ID") != "" {
		f.t.Fatal("reconciliation replaced authenticated event authority")
	}
	if r.URL.Path == "/event" {
		f.opened.Add(1)
		reader, writer := io.Pipe()
		f.stream = writer
		go func() {
			connected := NativeEvent{ID: "evt_ffffffffffffabcdefghijklmn", Kind: ServerConnectedEvent, Properties: json.RawMessage(`{}`)}
			events := append([]NativeEvent{connected}, f.overlap...)
			for _, e := range events {
				raw, _ := json.Marshal(map[string]any{"id": e.ID, "type": e.Kind, "properties": e.Properties})
				if _, err := fmt.Fprintf(writer, "event: message\ndata: %s\n\n", raw); err != nil {
					return
				}
			}
			<-r.Context().Done()
			_ = writer.Close()
		}()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}, nil
	}
	var value any
	s := f.api.session
	switch r.URL.Path {
	case "/config":
		value = fixtureEffectiveConfig(s.apiProfile)
		if f.change == "config" {
			value.(map[string]any)["small_model"] = "foreign/model"
		}
	case "/provider":
		value = s.apiProfile.provider()
	case "/path":
		value = map[string]any{"home": s.runtimeHome, "state": filepath.Join(s.runtimeHome, "state", "opencode"), "config": filepath.Join(s.runtimeHome, "config", "opencode"), "worktree": s.runtimeRoot, "directory": s.cwd}
	case "/agent":
		selected, err := expectedPrimaryAgent(s.creation.settings.Agent, s.runtimeHome, s.runtimeRoot)
		if err != nil {
			f.t.Fatal(err)
		}
		agents := []any{selected}
		for _, name := range []string{"plan", "general", "explore", "compaction", "title", "summary"} {
			agents = append(agents, map[string]any{"name": name, "mode": "subagent", "native": true, "permission": []any{}, "options": map[string]any{}})
		}
		value = agents
	default:
		if f.opened.Load() == 0 && strings.Contains(r.URL.Path, "/message") {
			f.t.Fatal("native reads preceded listener readiness")
		}
		if f.change == "oversized" && strings.Contains(r.URL.Path, "/message") {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxHTTPBody+1)))}, nil
		}
		response, err := f.historyFixture.RoundTrip(r)
		if err == nil && f.change == "changing" && r.URL.Path == "/session/"+fixtureSessionID+"/message" && r.URL.Query().Get("before") == "" {
			raw, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			var page []map[string]json.RawMessage
			if json.Unmarshal(raw, &page) != nil || len(page) != 1 {
				f.t.Fatal("invalid changing-history fixture")
			}
			var parts []map[string]any
			_ = json.Unmarshal(page[0]["parts"], &parts)
			for _, part := range parts {
				if part["type"] == "text" {
					part["text"] = fmt.Sprintf("prefix and final suffix %d", f.reads.Add(1))
				}
			}
			page[0]["parts"], _ = json.Marshal(parts)
			raw, _ = json.Marshal(page)
			response.Body = io.NopCloser(strings.NewReader(string(raw)))
		}
		return response, err
	}
	raw, _ := json.Marshal(value)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
}

func drainReconciliation(t *testing.T, f *reconciliationFixture) []Observation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var values []Observation
	for {
		value, err := f.api.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if value.EventID != "" {
			t.Fatal("native read fabricated an event identity")
		}
		frozen, err := value.Freeze()
		if err != nil {
			t.Fatal(err)
		}
		copy, err := frozen.Thaw()
		if err != nil || copy.EventID != "" {
			t.Fatal("reconciled proof lost its snapshot provenance")
		}
		values = append(values, value)
		progress, err := f.api.Progress(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if progress.Reconciliation == ReconciliationComplete {
			if !progress.SettledObserved || !progress.IdleReconciled || progress.IdleNotification || progress.NeedsRecovery {
				t.Fatal("read idle became a native notification or incomplete settlement")
			}
			break
		}
		if progress.SettledObserved {
			t.Fatal("public completion escaped before snapshot publication drained")
		}
	}
	return values
}

func TestEventReconciliationRecoversOriginalTextWithNoReplay(t *testing.T) {
	f := newReconciliationFixture(t)
	values := drainReconciliation(t, f)
	if len(values) == 0 || f.opened.Load() != 1 || f.checks.Load() != 2 {
		t.Fatal("not one ownership-checked reconciliation cycle")
	}
	if f.api.session.input.receipt.RequestID != f.o.input.receipt.RequestID || len(f.api.session.observer.parts) != len(f.o.parts) {
		t.Fatal("recovery replaced original input/part ownership")
	}
	proof, err := f.api.InspectHistory(context.Background())
	if err != nil || len(proof.Messages) != 2 {
		t.Fatal("recovered terminal did not independently verify stored history", err)
	}
	text := f.api.session.observer.parts["prt_000000000066ABCDEFGHIJKLMN"]
	if text == nil || text.text != "prefix and final suffix" {
		t.Fatal("recovery duplicated or lost original text prefix")
	}
	raw, _ := json.Marshal(values)
	if strings.Contains(string(raw), "prefix") || strings.Contains(string(raw), f.api.session.password) {
		t.Fatal("private recovery observations leaked through JSON")
	}
}

func TestEventReconciliationRefusesChangedOrUnboundedStateWithoutPartialFacts(t *testing.T) {
	for _, mode := range []string{"owner", "config", "oversized", "changed-message", "changed-part", "removed-part", "reordered-parts", "extra-part", "foreign-page", "extra-history", "invalid-cursor", "pending", "busy"} {
		t.Run(mode, func(t *testing.T) {
			f := newReconciliationFixture(t)
			f.change = mode
			if mode != "owner" && mode != "config" {
				f.mode = mode
			}
			ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
			defer cancel()
			before := f.api.session.observer.parts["prt_000000000066ABCDEFGHIJKLMN"].text
			value, err := f.api.Next(ctx)
			if err == nil || value.Kind != "" || len(f.api.session.recovered) != 0 || f.api.session.observer.parts["prt_000000000066ABCDEFGHIJKLMN"].text != before {
				t.Fatal("failed cycle published partial success")
			}
			if f.api.session.reconciliation != ReconciliationFailed || !f.api.session.observer.snapshot().NeedsRecovery {
				t.Fatal("failed cycle erased recovery")
			}
			if _, err := f.api.Next(context.Background()); err == nil || f.opened.Load() > 1 {
				t.Fatal("failed reconciliation gained another reconnect")
			}
		})
	}
}

func TestEventReconciliationOnlyRecoversTransportFailures(t *testing.T) {
	for _, failure := range []streamFailure{streamInvalid, streamCapacity, streamCanceled, streamOwnerLost, streamDisposed} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			f := newReconciliationFixture(t)
			f.api.session.events.failure = failure
			if _, err := f.api.Next(context.Background()); err == nil || f.opened.Load() != 0 {
				t.Fatal("nontransport failure gained reconnect authority")
			}
		})
	}
}

func TestEventReconciliationCancellationJoinsOriginalCleanup(t *testing.T) {
	f := newReconciliationFixture(t)
	unrelated := newReconciliationFixture(t)
	f.mode = "busy"
	done := make(chan error, 1)
	go func() { _, err := f.api.Next(context.Background()); done <- err }()
	deadline := time.Now().Add(time.Second)
	for f.opened.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	f.api.session.cancelReconciliation()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation became success")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation failed to join bounded reader")
	}
	if f.opened.Load() != 1 || !f.api.session.observer.snapshot().NeedsRecovery {
		t.Fatal("cancellation replaced original failure")
	}
	drainReconciliation(t, unrelated)
}

func TestEventReconciliationSnapshotKeysCannotInventNativeEvents(t *testing.T) {
	fake := Observation{Kind: MessageUpdatedEvent}
	if _, err := fake.PublicationKey(); err == nil {
		t.Fatal("caller manufactured snapshot provenance")
	}
	f := newReconciliationFixture(t)
	values := drainReconciliation(t, f)
	for _, value := range values {
		key, err := value.PublicationKey()
		if err != nil || !strings.HasPrefix(key, "snapshot:") {
			t.Fatal("snapshot source lacks private deduplication key")
		}
	}
}

func TestEventReconciliationJoinsOverlappingSnapshotAndStreamFacts(t *testing.T) {
	f := newReconciliationFixture(t)
	stored := f.o
	message := stored.messages[stored.progress.AssistantID]
	text := stored.parts["prt_000000000066ABCDEFGHIJKLMN"]
	raw, _ := json.Marshal(map[string]any{"sessionID": fixtureSessionID, "info": json.RawMessage(message.raw)})
	f.overlap = append(f.overlap, NativeEvent{ID: "evt_fffffffffff0abcdefghijklmn", Kind: MessageUpdatedEvent, Properties: raw})
	raw, _ = json.Marshal(map[string]any{"sessionID": fixtureSessionID, "part": json.RawMessage(text.raw), "time": 1250})
	f.overlap = append(f.overlap, NativeEvent{ID: "evt_fffffffffff1abcdefghijklmn", Kind: MessagePartUpdatedEvent, Properties: raw})
	drainReconciliation(t, f)
	if !f.api.session.observer.seen[f.overlap[0].ID] || len(f.api.session.observer.parts) != len(stored.parts) {
		t.Fatal("overlap created duplicate native records")
	}
}

func TestEventReconciliationPreservesReplyAcceptanceAndUncertainty(t *testing.T) {
	for _, positive := range []bool{false, true} {
		t.Run(fmt.Sprint(positive), func(t *testing.T) {
			r := newReplyFixture(t, QuestionInteraction)
			if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err != nil {
				t.Fatal(err)
			}
			if positive {
				r.f.observe(QuestionRepliedEvent, map[string]any{"sessionID": fixtureSessionID, "requestID": r.id, "answers": r.response.Answers})
			}
			original, _ := r.f.o.interactionReceipt(r.id)
			f := newReconciliationFixture(t)
			// Retain the exact original response claim/receipt even when the native
			// pending inventory disappears. No replacement answer can be inferred.
			interaction := r.f.o.reconciliationCopy().interactions[r.id]
			interaction.value.Tool.MessageID = f.api.session.observer.progress.AssistantID
			f.api.session.observer.interactions[r.id] = interaction
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if positive {
				drainReconciliation(t, f)
			} else {
				if value, err := f.api.Next(ctx); err == nil || value.Kind != "" {
					t.Fatal("pending disappearance proved delivery")
				}
			}
			retained, _ := f.api.session.observer.interactionReceipt(r.id)
			if retained != original || len(r.claims) != 1 || r.posts != 1 {
				t.Fatal("reconciliation repeated or reclassified the original reply")
			}
		})
	}
}

func snapshotOfObserver(o *inputObserver) reconciliationSnapshot {
	result := reconciliationSnapshot{idle: true, pending: [][]json.RawMessage{{}, {}}}
	for _, id := range o.messageOrder {
		owner := o.messages[id]
		message := reconciliationMessage{info: owner.raw}
		for _, part := range owner.parts {
			message.parts = append(message.parts, o.parts[part].raw)
		}
		result.messages = append(result.messages, message)
	}
	return result
}

func TestEventReconciliationPreservesOriginalRunningToolAcrossSuccessor(t *testing.T) {
	f := newObserverFixture(t)
	f.start()
	f.part(f.assistantPart(StepStartPartKind, 10, nil))
	tool := f.assistantPart(ToolPartKind, 20, map[string]any{"callID": "original-call", "tool": "read", "state": map[string]any{"status": ToolPending, "input": map[string]any{}, "raw": ""}})
	f.part(tool)
	tool["state"] = map[string]any{"status": ToolRunning, "input": map[string]any{"filePath": "private"}, "time": map[string]any{"start": 1236}}
	f.part(tool)
	live := f.o.reconciliationCopy()
	tool["state"] = map[string]any{"status": ToolCompleted, "input": map[string]any{"filePath": "private"}, "time": map[string]any{"start": 1236, "end": 1240}, "output": "original result", "title": "private", "metadata": map[string]any{}}
	f.part(tool)
	f.part(f.assistantPart(StepFinishPartKind, 21, map[string]any{"reason": FinishToolCalls, "tokens": f.a["tokens"], "cost": 0}))
	f.a["finish"] = FinishToolCalls
	f.a["time"].(map[string]any)["completed"] = 1250
	f.message(f.a)
	f.a = fixtureAssistant()
	f.a["id"] = "msg_01960dcbe3fbABCDEFGHIJKLMN"
	f.a["time"] = map[string]any{"created": 1251}
	f.message(f.a)
	f.part(f.assistantPart(StepStartPartKind, 30, nil))
	f.part(f.assistantPart(StepFinishPartKind, 31, map[string]any{"reason": FinishStop, "tokens": f.a["tokens"], "cost": 0}))
	f.a["finish"] = FinishStop
	f.a["time"].(map[string]any)["completed"] = 1255
	f.message(f.a)
	raw, _ := json.Marshal(map[string]any{"sessionID": fixtureSessionID, "part": tool, "time": 1255})
	overlap := NativeEvent{ID: "evt_fffffffffff2abcdefghijklmn", Kind: MessagePartUpdatedEvent, Properties: raw}
	candidate, values, err := live.joinReconciliation(snapshotOfObserver(f.o), []NativeEvent{overlap})
	if err != nil || !candidate.snapshot().SettledObserved || len(candidate.calls) != 1 || candidate.calls["original-call"] != tool["id"] {
		t.Fatal("tool recovery replaced the original call", err)
	}
	if !candidate.seen[overlap.ID] {
		t.Fatal("overlapping native tool update lost its original event identity")
	}
	part := candidate.parts[tool["id"].(string)]
	if part == nil || part.value.Tool.State != ToolCompleted || *part.value.Tool.Output != "original result" {
		t.Fatal("original result was not recovered")
	}
	for _, value := range values {
		if value.EventID != "" {
			t.Fatal("tool snapshots fabricated native arrivals")
		}
	}
	tool["callID"] = "foreign-call"
	raw, _ = json.Marshal(tool)
	snapshot := snapshotOfObserver(f.o)
	snapshot.messages[1].parts[1] = raw
	if _, partial, err := live.joinReconciliation(snapshot, nil); err == nil || len(partial) != 0 {
		t.Fatal("conflicting call published a partial result")
	}
}

func TestEventReconciliationKeepsOriginalThirtySecondDeadline(t *testing.T) {
	f := newReconciliationFixture(t)
	f.api.session.events.failedAt = time.Now().Add(-eventIdleLimit - time.Second)
	if _, err := f.api.Next(context.Background()); err == nil || f.opened.Load() != 0 {
		t.Fatal("reconciliation renewed an expired observation deadline")
	}
}

func TestEventReconciliationChangingHistoryExhaustsOriginalDeadline(t *testing.T) {
	f := newReconciliationFixture(t)
	f.change = "changing"
	f.api.session.events.failedAt = time.Now().Add(-eventIdleLimit + 300*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	value, err := f.api.Next(ctx)
	if err == nil || value.Kind != "" || f.reads.Load() < 2 || f.opened.Load() != 1 || f.api.session.reconciliation != ReconciliationFailed || len(f.api.session.recovered) != 0 {
		t.Fatal("changing snapshots escaped the original bounded cycle")
	}
	if f.api.session.observer.parts["prt_000000000066ABCDEFGHIJKLMN"].text != "prefix" {
		t.Fatal("unsettled snapshots changed the published prefix")
	}
}

func TestEventReconciliationStopBeforeCancellationRegistrationCannotRaceAdmission(t *testing.T) {
	f := newReconciliationFixture(t)
	f.api.session.cancelReconciliation()
	if _, err := f.api.Next(context.Background()); err == nil || f.opened.Load() != 0 || !f.api.session.observer.snapshot().NeedsRecovery {
		t.Fatal("early Stop gained a later reconciliation cycle")
	}
}

func TestEventReconciliationLateArrivalsCannotEscapeHistoryBoundary(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(fmt.Sprint(supported), func(t *testing.T) {
			f := newReconciliationFixture(t)
			drainReconciliation(t, f)
			event := NativeEvent{ID: "evt_fffffffffff3abcdefghijklmn", Kind: TodoUpdatedEvent, Properties: json.RawMessage(`{}`)}
			if supported {
				event.Kind = ServerHeartbeatEvent
			}
			stream := f.api.session.events
			stream.mu.Lock()
			stream.pending += len(event.Properties)
			stream.queue <- event
			stream.mu.Unlock()
			_, err := f.api.InspectHistory(context.Background())
			if supported {
				if err != nil || !f.api.session.observer.seen[event.ID] {
					t.Fatal("covered late heartbeat lost its original identity", err)
				}
			} else if err == nil || !f.api.session.observer.snapshot().NeedsRecovery {
				t.Fatal("late unsupported event disappeared behind recovered completion")
			}
		})
	}
}
