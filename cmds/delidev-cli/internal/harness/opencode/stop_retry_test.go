package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type earlyStopEventsTransport struct {
	http.RoundTripper
	observe func()
}

func (r earlyStopEventsTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPost {
		r.observe()
	}
	return r.RoundTripper.RoundTrip(request)
}

func TestOriginalStopRetryCompletionBeforeHTTPAcknowledgment(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run("lost="+fmtBool(lost), func(t *testing.T) {
			f := newStopFixture(t, PermissionInteraction)
			r := f.r
			r.lost = lost
			r.api.client.Transport = earlyStopEventsTransport{RoundTripper: r.api.client.Transport, observe: func() {
				r.f.observe(SessionStatusEvent, map[string]any{"sessionID": fixtureSessionID, "status": map[string]any{"type": "retry", "attempt": 1, "next": 1300, "message": "private provider diagnostic"}})
				r.f.part(r.f.assistantPart(ToolPartKind, 100, map[string]any{"callID": "call_private", "tool": "read", "state": map[string]any{"status": ToolError, "input": map[string]any{}, "error": "private interruption", "metadata": map[string]any{"interrupted": true}, "time": map[string]any{"start": 1236, "end": 1240}}}))
				r.f.a["time"].(map[string]any)["completed"] = 1240
				r.f.message(r.f.a)
				r.f.status(NativeStatusIdle)
				r.f.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
				before, err := r.f.o.stopReceipt()
				if err != nil || before.HTTPAccepted || before.TerminalObserved || before.RetryCanceledObserved || before.InterruptedObserved || !before.IdleObserved || !r.f.o.messages[r.f.o.progress.AssistantID].finalized {
					t.Fatal("provisional metadata invented Stop confirmation", err)
				}
			}}
			after, err := r.api.stopInput(context.Background(), r.f.o, domain.NewID())
			progress := r.f.o.snapshot()
			if (err != nil) != lost || after.HTTPAccepted == lost || after.TerminalObserved == lost || after.RetryCanceledObserved == lost || progress.SettledObserved == lost || after.InterruptedObserved || after.PendingCleared || after.CleanupVerified {
				t.Fatal("original HTTP result did not independently confirm early completion", err)
			}
			if _, err := r.api.stopInput(context.Background(), r.f.o, domain.NewID()); err == nil {
				t.Fatal("early completion permitted another abort")
			}
		})
	}
}

func TestOriginalStopDuringRetryPreservesMissingNativeError(t *testing.T) {
	f := newStopFixture(t, PermissionInteraction)
	r := f.r
	if _, err := r.api.stopInput(context.Background(), r.f.o, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	retry := r.f.observe(SessionStatusEvent, map[string]any{"sessionID": fixtureSessionID, "status": map[string]any{"type": "retry", "attempt": 1, "next": 1300, "message": "private provider diagnostic"}})
	r.f.part(r.f.assistantPart(ToolPartKind, 100, map[string]any{"callID": "call_private", "tool": "read", "state": map[string]any{"status": ToolError, "input": map[string]any{}, "error": "private interruption", "metadata": map[string]any{"interrupted": true}, "time": map[string]any{"start": 1236, "end": 1240}}}))
	r.f.a["time"].(map[string]any)["completed"] = 1240
	r.f.message(r.f.a)
	r.f.status(NativeStatusIdle)
	r.f.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
	history := &historyFixture{t: t, o: r.f.o, api: &OwnedAPI{session: r.api, reading: make(chan struct{}, 1)}}
	fixture := &stoppedHistoryFixture{stop: f, history: history, kind: PermissionInteraction, pending: []json.RawMessage{r.f.o.interactions[r.id].raw}}
	r.api.client = &http.Client{Transport: fixture}
	proof, err := history.api.CloseAfterStop(context.Background())
	if err != nil || !proof.Stop.RetryCanceledObserved || proof.Stop.InterruptedObserved || !proof.Stop.HTTPAccepted || len(proof.Retries) != 1 || proof.Retries[0].NativeEventID != retry.EventID || proof.Retries[0].Attempt != 1 || proof.Retries[0].Next != 1300 || r.f.o.messages[r.f.o.progress.AssistantID].value.Assistant.Error != nil {
		t.Fatal("retry cancellation invented native error or lost original schedule", err)
	}
	proof.Retries[0].Attempt = 999
	again, err := history.api.CloseAfterStop(context.Background())
	if err != nil || again.Retries[0].Attempt != 1 {
		t.Fatal("retry evidence shared caller memory")
	}
}

func TestOriginalStopRetryCannotAuthorizeOrdinaryRetryOrUnknownActions(t *testing.T) {
	for _, name := range []string{"unclaimed", "unsent", "action", "fraction", "missing-message", "settled", "lost-http"} {
		t.Run(name, func(t *testing.T) {
			f := newStopFixture(t, PermissionInteraction)
			r := f.r
			if name != "unclaimed" {
				r.lost = name == "lost-http"
				_, _ = r.api.stopInput(context.Background(), r.f.o, domain.NewID())
			}
			status := map[string]any{"type": "retry", "attempt": 1, "next": 1300, "message": "private diagnostic"}
			switch name {
			case "unsent":
				r.f.o.stop.sent = false
			case "action":
				status["action"] = map[string]any{"link": "private://provider"}
			case "fraction":
				status["attempt"] = 1.5
			case "missing-message":
				delete(status, "message")
			case "settled":
				r.f.o.progress.SettledObserved = true
			}
			_, err := r.f.o.observe(context.Background(), r.f.event(SessionStatusEvent, map[string]any{"sessionID": fixtureSessionID, "status": status}))
			if name != "lost-http" {
				if err == nil {
					t.Fatal("unsupported retry/action entered original Stop")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			value := *r.f.o.messages[r.f.o.progress.AssistantID].value.Assistant
			end := int64(1240)
			value.Completed = &end
			if r.f.o.stoppedBackoffMessage(NativeMessage{ID: r.f.o.progress.AssistantID, Assistant: &value}) {
				t.Fatal("missing HTTP acknowledgment fabricated backoff cancellation")
			}
		})
	}
}
