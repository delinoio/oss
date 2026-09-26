package opencode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type observerFixture struct {
	t     *testing.T
	o     *inputObserver
	seq   int
	user  map[string]any
	input map[string]any
	a     map[string]any
}

func newObserverFixture(t *testing.T) *observerFixture {
	t.Helper()
	creation := &sessionCreation{request: domain.NewID(), settings: fixtureSettings(), identity: sessionIdentity{id: fixtureSessionID, project: "global", slug: "private-fixture", created: 1234}}
	input := &sessionInput{receipt: InputReceipt{RequestID: domain.NewID(), SessionID: fixtureSessionID, MessageID: fixtureMessageID, PartID: fixturePartID}, digest: sha256.Sum256([]byte("private input"))}
	api := &sessionAPI{creation: creation, input: input, events: &eventStream{ctx: context.Background()}, gate: make(chan struct{}, 1), cwd: "/private/workspace"}
	o, err := api.observeInput(context.Background(), "/private/root")
	if err != nil {
		t.Fatal(err)
	}
	stored := fixtureInput(input.receipt, creation.settings, "private input")
	return &observerFixture{t: t, o: o, user: stored["info"].(map[string]any), input: stored["parts"].([]any)[0].(map[string]any), a: fixtureAssistant()}
}

func (f *observerFixture) event(kind EventKind, properties any) NativeEvent {
	f.seq++
	raw, _ := json.Marshal(properties)
	return NativeEvent{ID: fmt.Sprintf("evt_%012xabcdefghijklmn", f.seq), Kind: kind, Properties: raw}
}

func (f *observerFixture) observe(kind EventKind, properties any) inputObservation {
	f.t.Helper()
	result, err := f.o.observe(context.Background(), f.event(kind, properties))
	if err != nil {
		f.t.Fatalf("observe %s: %v", kind, err)
	}
	frozen, err := result.Freeze()
	if err != nil || frozen.Bytes() < len(result.EventID) {
		f.t.Fatal("original observation could not retain deferred publication", err)
	}
	copy, err := frozen.Thaw()
	if err != nil || !reflect.DeepEqual(result, copy) {
		f.t.Fatalf("deferred %s lost original private typed fields: %v", kind, err)
	}
	return result
}

func (f *observerFixture) message(value map[string]any) inputObservation {
	f.t.Helper()
	return f.observe(MessageUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "info": value})
}

func (f *observerFixture) part(value map[string]any) inputObservation {
	f.t.Helper()
	return f.observe(MessagePartUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "part": value, "time": 1240})
}

func (f *observerFixture) status(status NativeSessionStatus) {
	f.t.Helper()
	f.observe(SessionStatusEvent, map[string]any{"sessionID": fixtureSessionID, "status": map[string]any{"type": status}})
}

func (f *observerFixture) start() {
	f.t.Helper()
	f.message(f.user)
	f.part(f.input)
	f.status(NativeStatusBusy)
	f.message(f.a)
}

func (f *observerFixture) assistantPart(kind PartKind, n int, fields map[string]any) map[string]any {
	part := fixturePart(kind, fields)
	part["id"] = fmt.Sprintf("prt_%012xABCDEFGHIJKLMN", n)
	part["messageID"] = f.a["id"]
	return part
}

func (f *observerFixture) finish(reason FinishReason) {
	f.t.Helper()
	f.part(f.assistantPart(StepStartPartKind, 100, nil))
	f.part(f.assistantPart(StepFinishPartKind, 101, map[string]any{"reason": reason, "tokens": f.a["tokens"], "cost": 0}))
	f.a["finish"] = reason
	f.message(f.a)
	f.a["time"].(map[string]any)["completed"] = 1250
	f.message(f.a)
}

func (f *observerFixture) reject(kind EventKind, properties any) {
	f.t.Helper()
	_, err := f.o.observe(context.Background(), f.event(kind, properties))
	if err == nil || !f.o.snapshot().NeedsRecovery {
		f.t.Fatal("unowned lifecycle did not latch recovery")
	}
	if _, again := f.o.observe(context.Background(), f.event(ServerHeartbeatEvent, map[string]any{})); again == nil {
		f.t.Fatal("matching later event erased uncertainty")
	}
}

func TestInputObserverCompletionIdleAndStorageAreIndependent(t *testing.T) {
	f := newObserverFixture(t)
	f.start()
	f.status(NativeStatusIdle)
	f.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
	if p := f.o.snapshot(); p.TerminalObserved || p.SettledObserved || !p.UserSeen || !p.InputPartSeen || f.o.input.receipt.Recorded {
		t.Fatal("idle or event observation fabricated terminal/storage evidence")
	}
	f.finish(FinishStop)
	if p := f.o.snapshot(); !p.TerminalObserved || !p.SettledObserved || p.NeedsRecovery {
		t.Fatal("original terminal and idle did not remain independent")
	}
	if !f.message(f.a).Repeated || len(f.o.messages) != 2 || len(f.o.parts) != 3 {
		t.Fatal("repeated native terminal duplicated ownership")
	}
	if err := f.o.interruption(context.Background()); err == nil {
		t.Fatal("transport interruption became success")
	}
	if p := f.o.snapshot(); !p.TerminalObserved || !p.SettledObserved || !p.NeedsRecovery {
		t.Fatal("interruption erased earlier original observations")
	}
}

func TestInputObserverEarlyFinalSnapshotWaitsForOriginalPartBoundary(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			f := newObserverFixture(t)
			f.message(f.user)
			f.part(f.input)
			f.status(NativeStatusBusy)
			f.a["time"].(map[string]any)["completed"] = 1250
			if failed {
				f.a["error"] = map[string]any{"name": APIErrorKind, "data": map[string]any{"message": "private", "isRetryable": false}}
			} else {
				f.a["finish"] = FinishStop
			}
			first := f.message(f.a)
			if first.MessageFinalized || f.o.snapshot().TerminalObserved {
				t.Fatal("early native final metadata skipped owned part observations")
			}
			f.part(f.assistantPart(StepStartPartKind, 100, nil))
			if !failed {
				part := f.assistantPart(TextPartKind, 101, map[string]any{"text": "", "time": map[string]any{"start": 1236}})
				f.part(part)
				f.observe(MessagePartDeltaEvent, map[string]any{"sessionID": fixtureSessionID, "messageID": f.a["id"], "partID": part["id"], "field": "text", "delta": "private"})
				part["text"] = "private"
				part["time"].(map[string]any)["end"] = 1240
				f.part(part)
				f.part(f.assistantPart(StepFinishPartKind, 102, map[string]any{"reason": FinishStop, "tokens": f.a["tokens"], "cost": 0}))
			}
			f.status(NativeStatusIdle)
			f.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
			if f.o.snapshot().SettledObserved {
				t.Fatal("idle finalized an early message snapshot")
			}
			final := f.message(f.a)
			if !final.Repeated || !final.MessageFinalized || !f.o.snapshot().SettledObserved {
				t.Fatal("identical final payload lost its later original arrival boundary")
			}
		})
	}
}

func TestInputObserverContentFilterRefinesCompletedMessage(t *testing.T) {
	f := newObserverFixture(t)
	f.start()
	f.finish(FinishContentFilter)
	f.a["error"] = map[string]any{"name": ContentErrorKind, "data": map[string]any{"message": "private diagnostic"}}
	result := f.message(f.a)
	if result.Repeated || result.Message.Assistant.Error == nil || !result.MessageFinalized {
		t.Fatal("native post-cleanup content filter error was lost")
	}
	f.status(NativeStatusIdle)
	f.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
	f.reject(SessionStatusEvent, map[string]any{"sessionID": fixtureSessionID, "status": map[string]any{"type": NativeStatusBusy}})
}

func TestInputObserverToolCallsAndUnknownRequireSuccessor(t *testing.T) {
	for _, reason := range []FinishReason{FinishToolCalls, FinishUnknown} {
		t.Run(string(reason), func(t *testing.T) {
			f := newObserverFixture(t)
			f.start()
			f.finish(reason)
			f.status(NativeStatusIdle)
			f.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
			if p := f.o.snapshot(); p.TerminalObserved || p.SettledObserved {
				t.Fatal("native continuation finish became root completion")
			}
			f.status(NativeStatusBusy)
			f.a = fixtureAssistant()
			f.a["id"] = "msg_01960dcbe1fcABCDEFGHIJKLMN"
			f.message(f.a)
			if f.o.snapshot().IdleNotification {
				t.Fatal("prior idle satisfied successor boundary")
			}
			f.part(f.assistantPart(StepStartPartKind, 200, nil))
			f.part(f.assistantPart(StepFinishPartKind, 201, map[string]any{"reason": FinishStop, "tokens": f.a["tokens"], "cost": 0}))
			f.a["finish"] = FinishStop
			f.a["time"].(map[string]any)["completed"] = 1250
			f.message(f.a)
			if !f.o.snapshot().TerminalObserved || f.o.snapshot().SettledObserved {
				t.Fatal("successor terminal borrowed earlier idle")
			}
		})
	}
}

func TestInputObserverErrorAfterIdleKeepsOriginalFailure(t *testing.T) {
	f := newObserverFixture(t)
	f.start()
	f.part(f.assistantPart(StepStartPartKind, 100, nil))
	errorValue := map[string]any{"name": APIErrorKind, "data": map[string]any{"message": "private diagnostic", "isRetryable": false, "statusCode": 401}}
	observation := f.observe(SessionErrorEvent, map[string]any{"sessionID": fixtureSessionID, "error": errorValue})
	if observation.Error == nil || f.o.snapshot().TerminalObserved {
		t.Fatal("session error fabricated completed assistant")
	}
	f.status(NativeStatusIdle)
	f.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
	f.a["error"] = errorValue
	f.a["time"].(map[string]any)["completed"] = 1250
	f.message(f.a)
	if !f.o.snapshot().SettledObserved || f.o.messages[f.a["id"].(string)].openStep == "" {
		t.Fatal("failed completion erased the missing step-finish observation")
	}
}

func TestInputObserverRejectsUnownedMessages(t *testing.T) {
	for _, name := range []string{"session", "parent", "model", "root", "agent", "created", "variant", "summary", "concurrent-assistant", "finish-regression", "terminal-rewrite", "step-usage"} {
		t.Run(name, func(t *testing.T) {
			f := newObserverFixture(t)
			f.start()
			switch name {
			case "session":
				f.a["sessionID"] = "ses_01960dcbe1fbABCDEFGHIJKLMN"
			case "parent":
				f.a["parentID"] = "msg_01960dcbe1fdABCDEFGHIJKLMN"
			case "model":
				f.a["modelID"] = "foreign"
			case "root":
				f.a["path"].(map[string]any)["root"] = "/unowned"
			case "agent":
				f.a["agent"] = "foreign"
			case "created":
				f.a["time"].(map[string]any)["created"] = 1236
			case "variant":
				f.a["variant"] = "foreign"
			case "summary":
				f.a["summary"] = false
			case "concurrent-assistant":
				f.a["id"] = "msg_01960dcbe1fdABCDEFGHIJKLMN"
			case "finish-regression":
				f.finish(FinishStop)
				delete(f.a, "finish")
			case "terminal-rewrite":
				f.finish(FinishStop)
				f.a["cost"] = 1
			case "step-usage":
				f.part(f.assistantPart(StepStartPartKind, 100, nil))
				f.part(f.assistantPart(StepFinishPartKind, 101, map[string]any{"reason": FinishStop, "tokens": f.a["tokens"], "cost": 0}))
				f.a["finish"] = FinishStop
				f.a["tokens"].(map[string]any)["input"] = 999
			}
			f.reject(MessageUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "info": f.a})
		})
	}
}

func TestInputObserverOriginalUserCannotChangeContext(t *testing.T) {
	for _, name := range []string{"id", "system", "tools", "format", "text", "synthetic", "extra-part"} {
		t.Run(name, func(t *testing.T) {
			f := newObserverFixture(t)
			f.start()
			switch name {
			case "id":
				f.user["id"] = f.a["id"]
			case "system":
				f.user["system"] = "changed"
			case "tools":
				f.user["tools"] = map[string]any{}
			case "format":
				f.user["format"] = map[string]any{"type": "text"}
			case "text":
				f.input["text"] = "changed"
			case "synthetic":
				f.input["synthetic"] = false
			case "extra-part":
				f.input["id"] = "prt_01960dcbe1fbABCDEFGHIJKLMN"
			}
			if name == "text" || name == "synthetic" || name == "extra-part" {
				f.reject(MessagePartUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "part": f.input, "time": 1240})
			} else {
				f.reject(MessageUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "info": f.user})
			}
		})
	}
}

func TestInputObserverTextDeltaOwnershipAndFinalSnapshot(t *testing.T) {
	for _, kind := range []PartKind{TextPartKind, ReasoningPartKind} {
		for _, invalid := range []string{"", "unseen", "foreign-message", "field", "regression", "old-snapshot", "after-end", "completion-open"} {
			t.Run(string(kind)+"/"+invalid, func(t *testing.T) {
				f := newObserverFixture(t)
				f.start()
				f.part(f.assistantPart(StepStartPartKind, 99, nil))
				part := f.assistantPart(kind, 100, map[string]any{"text": "", "time": map[string]any{"start": 1236}})
				f.part(part)
				delta := map[string]any{"sessionID": fixtureSessionID, "messageID": f.a["id"], "partID": part["id"], "field": "text", "delta": "private delta"}
				switch invalid {
				case "unseen":
					delta["partID"] = "prt_01960dcbe1fbABCDEFGHIJKLMN"
				case "foreign-message":
					delta["messageID"] = fixtureMessageID
				case "field":
					delta["field"] = "output"
				}
				if invalid == "unseen" || invalid == "foreign-message" || invalid == "field" {
					f.reject(MessagePartDeltaEvent, delta)
					return
				}
				f.observe(MessagePartDeltaEvent, delta)
				if invalid == "old-snapshot" || invalid == "regression" {
					if invalid == "regression" {
						part["text"] = "private"
					}
					f.reject(MessagePartUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "part": part, "time": 1240})
					return
				}
				if invalid == "completion-open" {
					f.a["error"] = map[string]any{"name": AbortedErrorKind, "data": map[string]any{"message": "private"}}
					f.a["time"].(map[string]any)["completed"] = 1250
					f.reject(MessageUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "info": f.a})
					return
				}
				part["text"] = "private delta"
				part["time"].(map[string]any)["end"] = 1240
				result := f.part(part)
				result.Part.Text.Text = "caller mutation"
				if f.o.parts[part["id"].(string)].value.Text.Text != "private delta" || !f.part(part).Repeated {
					t.Fatal("caller changed retained text or exact repeat duplicated it")
				}
				if invalid == "after-end" {
					f.reject(MessagePartDeltaEvent, delta)
				}
			})
		}
	}
}

func TestInputObserverToolOwnershipTransitionsAndPendingAbort(t *testing.T) {
	for _, invalid := range []string{"", "first-running", "first-completed", "first-error", "call-reuse", "changed-call", "changed-input", "regression", "changed-start", "terminal-rewrite", "open-completion", "attachment-reuse", "pending-abort"} {
		t.Run(invalid, func(t *testing.T) {
			f := newObserverFixture(t)
			f.start()
			f.part(f.assistantPart(StepStartPartKind, 99, nil))
			part := f.assistantPart(ToolPartKind, 100, map[string]any{"callID": "private-call", "tool": "read", "state": map[string]any{"status": ToolPending, "input": map[string]any{}, "raw": ""}})
			fail := func() {
				f.reject(MessagePartUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "part": part, "time": 1240})
			}
			running := map[string]any{"status": ToolRunning, "input": map[string]any{"filePath": "private"}, "time": map[string]any{"start": 1236}}
			completed := map[string]any{"status": ToolCompleted, "input": map[string]any{"filePath": "private"}, "time": map[string]any{"start": 1236, "end": 1240}, "output": "private", "title": "private", "metadata": map[string]any{}}
			failed := map[string]any{"status": ToolError, "input": map[string]any{}, "time": map[string]any{"start": 1236, "end": 1240}, "error": "private"}
			switch invalid {
			case "first-running":
				part["state"] = running
				fail()
				return
			case "first-completed":
				part["state"] = completed
				fail()
				return
			case "first-error":
				part["state"] = failed
				fail()
				return
			}
			f.part(part)
			if invalid == "pending-abort" {
				part["state"] = failed
				f.part(part)
				return
			}
			if invalid == "call-reuse" {
				part["id"] = "prt_01960dcbe1fbABCDEFGHIJKLMN"
				fail()
				return
			}
			part["state"] = running
			f.part(part)
			switch invalid {
			case "changed-call":
				part["callID"] = "foreign"
				fail()
				return
			case "changed-input":
				running["input"] = map[string]any{"filePath": "foreign"}
				fail()
				return
			case "regression":
				part["state"] = map[string]any{"status": ToolPending, "input": map[string]any{}, "raw": ""}
				fail()
				return
			case "changed-start":
				running["time"].(map[string]any)["start"] = 1237
				fail()
				return
			case "open-completion":
				f.a["error"] = map[string]any{"name": AbortedErrorKind, "data": map[string]any{"message": "private"}}
				f.a["time"].(map[string]any)["completed"] = 1250
				f.reject(MessageUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "info": f.a})
				return
			case "attachment-reuse":
				completed["attachments"] = []any{f.assistantPart(FilePartKind, 200, map[string]any{"id": fixturePartID, "mime": "text/plain", "url": "private"})}
				// assistantPart chooses its ID independently of payload fields.
				completed["attachments"].([]any)[0].(map[string]any)["id"] = fixturePartID
				part["state"] = completed
				fail()
				return
			}
			part["state"] = completed
			result := f.part(part)
			*result.Part.Tool.Output = "caller mutation"
			result.Part.Tool.Input[0] = '!'
			if !f.part(part).Repeated {
				t.Fatal("returned tool payload mutated retained observation")
			}
			if invalid == "terminal-rewrite" {
				completed["output"] = "foreign"
				fail()
			}
		})
	}
}

func TestInputObserverRejectsPartBeforeOwnedStep(t *testing.T) {
	f := newObserverFixture(t)
	f.start()
	part := f.assistantPart(TextPartKind, 100, map[string]any{"text": "", "time": map[string]any{"start": 1236}})
	f.reject(MessagePartUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "part": part, "time": 1240})
}

func TestInputObserverBoundsDuplicatesAndPrivateDiagnostics(t *testing.T) {
	for _, cause := range []string{"duplicate", "bytes", "identities", "messages", "parts", "unknown"} {
		t.Run(cause, func(t *testing.T) {
			f := newObserverFixture(t)
			var log bytes.Buffer
			f.o.logger = slog.New(slog.NewJSONHandler(&log, nil))
			f.start()
			event := f.event(PluginAddedEvent, map[string]any{"id": "private-sentinel"})
			switch cause {
			case "duplicate":
				f.o.seen[event.ID] = true
			case "bytes":
				f.o.bytes = maxObservedBytes
			case "identities":
				for i := 0; i < maxEventIdentities; i++ {
					f.o.seen[fmt.Sprint(i)] = true
				}
			case "messages":
				f.finish(FinishToolCalls)
				for i := 0; i < maxObservedMessages; i++ {
					f.o.messages[fmt.Sprint(i)] = nil
				}
				f.a = fixtureAssistant()
				f.a["id"] = "msg_01960dcbe1fcABCDEFGHIJKLMN"
				event = f.event(MessageUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "info": f.a})
			case "parts":
				for i := 0; i < maxObservedParts; i++ {
					f.o.attachments[fmt.Sprint(i)] = "private"
				}
				event = f.event(MessagePartUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "part": f.assistantPart(StepStartPartKind, 100, nil), "time": 1240})
			case "unknown":
				event.Kind = "private-sentinel"
			}
			if _, err := f.o.observe(context.Background(), event); err == nil {
				t.Fatal("invalid event bypassed bounded ownership")
			}
			if !strings.Contains(log.String(), "request_id") || strings.Contains(log.String(), "private-sentinel") || strings.Contains(log.String(), "/private/") {
				t.Fatal("diagnostic missing ownership or exposing private payload")
			}
		})
	}
}

func TestInputObserverPreservesArrivalOrderAndCopiedSelection(t *testing.T) {
	f := newObserverFixture(t)
	first := f.event(PluginAddedEvent, map[string]any{"id": "private-plugin"})
	second := f.event(CatalogUpdatedEvent, map[string]any{})
	if _, err := f.o.observe(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	observation, err := f.o.observe(context.Background(), first)
	if err != nil {
		t.Fatal("native IDs incorrectly used as event ordering")
	}
	observation.Ancillary[0] = '!'
	first.Properties[0] = '!'
	f.start()
	result := f.message(f.a)
	result.Message.Assistant.Model = "foreign"
	if !f.message(f.a).Repeated {
		t.Fatal("returned assistant mutated original selection")
	}
	f.user["summary"] = map[string]any{"title": "Private changed summary", "diffs": []any{}}
	f.message(f.user)
}

func TestProjectDirectoryNotificationKeepsOriginalProjectAuthority(t *testing.T) {
	f := newObserverFixture(t)
	f.o.creation.identity.project = "original-project"
	observation := f.observe(ProjectDirectoriesUpdatedEvent, map[string]any{"projectID": "original-project"})
	if observation.Message != nil || observation.Part != nil || observation.WorkspaceEvent != nil || !bytes.Equal(observation.Ancillary, []byte(`{"projectID":"original-project"}`)) {
		t.Fatal("directory inventory acquired content or workspace authority")
	}
	for _, properties := range []map[string]any{{}, {"projectID": "foreign"}, {"projectID": nil}, {"projectID": "global", "directory": "/foreign"}} {
		other := newObserverFixture(t)
		if _, err := other.o.observe(context.Background(), other.event(ProjectDirectoriesUpdatedEvent, properties)); err == nil {
			t.Fatal("foreign directory inventory accepted")
		}
	}
}
