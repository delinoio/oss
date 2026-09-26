package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type stopFixture struct {
	r          *replyFixture
	pending    []byte
	status     []byte
	closeCalls int
	closeError bool
}

func newStopFixture(t *testing.T, kind InteractionKind) *stopFixture {
	t.Helper()
	r := newReplyFixture(t, kind)
	f := &stopFixture{r: r, pending: []byte("[]"), status: []byte("{}")}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		user, password, valid := request.BasicAuth()
		if !valid || user != "delidev" || password != "private-credential" || request.URL.RawQuery != "" || request.Header.Get("x-opencode-directory") != r.f.o.cwd {
			t.Error("Stop escaped original native authority")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet {
			switch request.URL.Path {
			case "/session/" + fixtureSessionID:
				_ = json.NewEncoder(w).Encode(fixtureSession(r.f.o.cwd, r.f.o.creation.request, r.f.o.creation.settings))
			case "/session/" + fixtureSessionID + "/message/" + fixtureMessageID:
				_ = json.NewEncoder(w).Encode(fixtureInput(r.f.o.input.receipt, r.f.o.creation.settings, "private input"))
			case "/permission", "/question":
				if request.URL.Path == "/"+string(kind) {
					_, _ = w.Write(f.pending)
				} else {
					_, _ = io.WriteString(w, "[]")
				}
			case "/session/status":
				_, _ = w.Write(f.status)
			default:
				t.Error("unexpected Stop inspection route")
				w.WriteHeader(400)
			}
			return
		}
		body, _ := io.ReadAll(request.Body)
		r.mu.Lock()
		r.posts++
		claims := append([]SessionClaim(nil), r.claims...)
		r.mu.Unlock()
		if request.Method != http.MethodPost || request.URL.Path != "/session/"+fixtureSessionID+"/abort" || len(body) != 0 || len(claims) != 1 || claims[0].Kind != StopInputMutation || claims[0].BodyDigest != mutationDigest(nil) || claims[0].InputRequestID != r.f.o.input.receipt.RequestID || claims[0].MessageID != fixtureMessageID || claims[0].PartID != fixturePartID {
			t.Error("abort escaped synchronized original Stop claim")
			w.WriteHeader(400)
			return
		}
		if r.lost {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
			return
		}
		_, _ = io.WriteString(w, "true")
	}))
	t.Cleanup(server.Close)
	client, transport := probeHTTPClient(strings.TrimPrefix(server.URL, "http://"))
	t.Cleanup(transport.CloseIdleConnections)
	r.api.client, r.api.origin, r.api.owner = client, server.URL, domain.NewID()
	done := make(chan struct{})
	close(done)
	r.api.events.done = done
	r.api.events.cancel = func() {}
	r.api.events.body = io.NopCloser(strings.NewReader(""))
	r.api.closeOwned = func(context.Context) error {
		f.closeCalls++
		if f.closeError {
			return errors.New("private cleanup failure")
		}
		return nil
	}
	return f
}

func observeInterruptedStop(r *replyFixture) {
	r.f.t.Helper()
	r.f.status(NativeStatusIdle)
	r.f.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
	name := "read"
	if r.f.o.interactions[r.id].value.Kind == QuestionInteraction {
		name = "question"
	}
	r.f.part(r.f.assistantPart(ToolPartKind, 100, map[string]any{"callID": "call_private", "tool": name, "state": map[string]any{"status": ToolError, "input": map[string]any{}, "error": "private interruption", "metadata": map[string]any{"interrupted": true}, "time": map[string]any{"start": 1236, "end": 1240}}}))
	r.f.a["error"] = map[string]any{"name": AbortedErrorKind, "data": map[string]any{"message": "private interruption"}}
	r.f.a["time"].(map[string]any)["completed"] = 1250
	r.f.message(r.f.a)
}

func TestStopKeepsNativeDeliveryInterruptionInventoriesAndCleanupIndependent(t *testing.T) {
	for _, kind := range []InteractionKind{PermissionInteraction, QuestionInteraction} {
		for _, lost := range []bool{false, true} {
			t.Run(string(kind)+"/lost="+fmtBool(lost), func(t *testing.T) {
				f := newStopFixture(t, kind)
				r := f.r
				r.lost = lost
				receipt, err := r.api.stopInput(context.Background(), r.f.o, domain.NewID())
				if (err != nil) != lost || receipt.HTTPAccepted == lost || receipt.TerminalObserved || receipt.PendingCleared || receipt.CleanupVerified {
					t.Fatal("HTTP result fabricated Stop completion")
				}
				if r.api.abortAttempt {
					t.Fatal("Stop retained mutation route authority")
				}
				if _, err := r.api.verifyStop(context.Background(), r.f.o); err == nil {
					t.Fatal("HTTP alone cleared native pending state")
				}
				if _, _, err := r.f.o.prepareInteraction(domain.NewID(), r.id, r.response); err == nil {
					t.Fatal("Stop permitted a racing answer")
				}
				observeInterruptedStop(r)
				if r.f.o.interactions[r.id].closed {
					t.Fatal("interrupted tool invented pending-request closure")
				}
				receipt, err = r.api.verifyStop(context.Background(), r.f.o)
				if err != nil || !receipt.InterruptedObserved || !receipt.IdleVerified || !receipt.PendingCleared || receipt.CleanupVerified || receipt.RepliesUncertain || receipt.HTTPAccepted == lost {
					t.Fatalf("native Stop facts: %+v %v", receipt, err)
				}
				if state := r.f.o.interactions[r.id]; !state.canceled || state.rejected || state.attempt != nil {
					t.Fatal("Stop cancellation fabricated an answer or rejection")
				}
				for range 2 {
					receipt, err = r.api.closeStoppedRuntime(context.Background(), r.f.o)
					if err != nil || !receipt.CleanupVerified || !receipt.PendingCleared {
						t.Fatal("original cleanup not retained idempotently")
					}
				}
				if f.closeCalls != 1 || r.posts != 1 || len(r.claims) != 1 {
					t.Fatal("Stop replayed native actions")
				}
			})
		}
	}
}

func TestStopNativePendingEffectsRequireOriginalOwnedCleanup(t *testing.T) {
	for _, mode := range []string{"pending", "busy", "malformed", "cleanup-failed", "unacknowledged-answer"} {
		t.Run(mode, func(t *testing.T) {
			f := newStopFixture(t, PermissionInteraction)
			r := f.r
			if _, err := r.api.stopInput(context.Background(), r.f.o, domain.NewID()); err != nil {
				t.Fatal(err)
			}
			observeInterruptedStop(r)
			f.pending, _ = json.Marshal([]json.RawMessage{r.f.o.interactions[r.id].raw})
			if mode == "busy" {
				f.pending = []byte("[]")
				f.status = []byte(`{"ses_01960dcbe1fbABCDEFGHIJKLMN":{"type":"busy"}}`)
			}
			if mode == "malformed" {
				f.pending = []byte("null")
			}
			if mode == "cleanup-failed" {
				f.closeError = true
			}
			if mode == "unacknowledged-answer" {
				// A prior sent answer can lose both its HTTP and native closure;
				// stopping the process cannot recover that missing acceptance.
				r.f.o.interactions[r.id].attempt = &interactionAttempt{sent: true, receipt: InteractionReceipt{RequestID: domain.NewID()}}
			}
			if receipt, err := r.api.verifyStop(context.Background(), r.f.o); err == nil || receipt.PendingCleared || receipt.CleanupVerified || r.f.o.interactions[r.id].closed {
				t.Fatal("Stop fabricated cleared native pending work")
			}
			receipt, err := r.api.closeStoppedRuntime(context.Background(), r.f.o)
			wantError := mode == "cleanup-failed" || mode == "unacknowledged-answer"
			if (err != nil) != wantError || receipt.CleanupVerified != (mode != "cleanup-failed") || receipt.RepliesUncertain != (mode == "unacknowledged-answer") {
				t.Fatalf("cleanup uncertainty: %+v %v", receipt, err)
			}
			_, _ = r.api.closeStoppedRuntime(context.Background(), r.f.o)
			if f.closeCalls != 1 || r.posts != 1 {
				t.Fatal("uncertain cleanup or answer authorized automatic retry")
			}
		})
	}
}

func TestStopClaimFailureAndCancellationNeverResend(t *testing.T) {
	for _, mode := range []string{"claim-error", "cancel", "observer-failed"} {
		t.Run(mode, func(t *testing.T) {
			f := newStopFixture(t, PermissionInteraction)
			r := f.r
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "claim-error":
				r.claimError = true
			case "cancel":
				r.beforeClaimReturn = cancel
			case "observer-failed":
				r.beforeClaimReturn = func() { _ = r.f.o.interruption(context.Background()) }
			}
			receipt, err := r.api.stopInput(ctx, r.f.o, domain.NewID())
			if err == nil || receipt.RequestID == "" || receipt.HTTPAccepted || r.posts != 0 {
				t.Fatal("unconfirmed claim allowed native abort")
			}
			if _, err := r.api.stopInput(context.Background(), r.f.o, domain.NewID()); err == nil || len(r.claims) != 1 || r.posts != 0 {
				t.Fatal("uncertain original Stop claim was replayed")
			}
			receipt, err = r.api.closeStoppedRuntime(context.Background(), r.f.o)
			if err != nil || !receipt.CleanupVerified || receipt.InterruptedObserved || receipt.HTTPAccepted {
				t.Fatal("explicit owner cleanup fabricated unobserved native abort")
			}
		})
	}
}

func TestUnclaimedStopCannotCloseAnOutstandingNativeInteraction(t *testing.T) {
	r := newReplyFixture(t, PermissionInteraction)
	part := r.f.assistantPart(ToolPartKind, 100, map[string]any{"callID": "call_private", "tool": "read", "state": map[string]any{"status": ToolError, "input": map[string]any{}, "error": "private interruption", "metadata": map[string]any{"interrupted": true}, "time": map[string]any{"start": 1236, "end": 1240}}})
	r.f.reject(MessagePartUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "part": part, "time": 1240})
}

func TestStopRacingNormalCompletionCannotInventNativeInterruption(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run("lost="+fmtBool(lost), func(t *testing.T) {
			f := newStopFixture(t, PermissionInteraction)
			r := f.r
			r.lost = lost
			attempt, _, err := r.f.o.prepareInteraction(domain.NewID(), r.id, r.response)
			if err != nil {
				t.Fatal(err)
			}
			attempt.sent = true
			if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
				t.Fatal(err)
			}
			if _, err := r.api.stopInput(context.Background(), r.f.o, domain.NewID()); (err != nil) != lost {
				t.Fatal(err)
			}
			r.f.part(r.f.assistantPart(ToolPartKind, 100, map[string]any{"callID": "call_private", "tool": "read", "state": map[string]any{"status": ToolRunning, "input": map[string]any{}, "time": map[string]any{"start": 1236}}}))
			r.f.part(r.f.assistantPart(ToolPartKind, 100, map[string]any{"callID": "call_private", "tool": "read", "state": map[string]any{"status": ToolCompleted, "input": map[string]any{}, "time": map[string]any{"start": 1236, "end": 1240}, "output": "private", "title": "private", "metadata": map[string]any{}}}))
			finishInteractionMessage(r)
			r.f.a = fixtureAssistant()
			r.f.a["id"] = "msg_01960dcbe200ABCDEFGHIJKLMN"
			r.f.message(r.f.a)
			r.f.part(r.f.assistantPart(StepStartPartKind, 200, nil))
			r.f.part(r.f.assistantPart(StepFinishPartKind, 201, map[string]any{"reason": FinishStop, "cost": 0, "tokens": r.f.a["tokens"]}))
			r.f.a["finish"] = FinishStop
			r.f.a["time"].(map[string]any)["completed"] = 1300
			r.f.message(r.f.a)
			r.f.status(NativeStatusIdle)
			r.f.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
			receipt, err := r.api.verifyStop(context.Background(), r.f.o)
			if (err != nil) != lost || receipt.InterruptedObserved || !receipt.TerminalObserved || receipt.HTTPAccepted == lost || receipt.IdleVerified == lost {
				t.Fatalf("racing natural completion: %+v %v", receipt, err)
			}
			receipt, err = r.api.closeStoppedRuntime(context.Background(), r.f.o)
			if err != nil || !receipt.CleanupVerified || receipt.InterruptedObserved || r.f.o.interactions[r.id].canceled {
				t.Fatal("cleanup rewrote native success or accepted original approval")
			}
		})
	}
}

func TestStopCleanupRetainsLateOriginalAnswerAcceptanceWithoutReplay(t *testing.T) {
	f := newStopFixture(t, QuestionInteraction)
	r := f.r
	attempt, _, err := r.f.o.prepareInteraction(domain.NewID(), r.id, r.response)
	if err != nil {
		t.Fatal(err)
	}
	attempt.sent = true
	if _, err := r.api.stopInput(context.Background(), r.f.o, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	receipt, err := r.api.closeStoppedRuntime(context.Background(), r.f.o)
	if err == nil || !receipt.CleanupVerified || !receipt.RepliesUncertain || !r.f.o.interactions[r.id].canceled {
		t.Fatal("shutdown fabricated missing original answer acceptance")
	}
	if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
		t.Fatal("queued original closure was erased by cleanup", err)
	}
	receipt, err = r.api.closeStoppedRuntime(context.Background(), r.f.o)
	answer, _ := r.f.o.interactionReceipt(r.id)
	if err != nil || !receipt.CleanupVerified || receipt.RepliesUncertain || !answer.NativeAccepted || answer.HTTPAccepted || r.f.o.interactions[r.id].canceled || f.closeCalls != 1 || r.posts != 1 {
		t.Fatal("late native acceptance repeated work or fabricated lost HTTP acknowledgment")
	}
}
