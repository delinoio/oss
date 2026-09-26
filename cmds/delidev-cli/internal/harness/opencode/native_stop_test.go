package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type nativeStopIntent string

const (
	liveStopIntent             nativeStopIntent = "live"
	unobservedStopIntent       nativeStopIntent = "unobserved"
	lostStreamStopIntent       nativeStopIntent = "lost-stream"
	recoverCompletedStopIntent nativeStopIntent = "recover-completed-cleanup"
	recoverUnstartedStopIntent nativeStopIntent = "recover-unstarted-cleanup"
	verifiedHistoryStopIntent  nativeStopIntent = "verified-stopped-history"
)

type lostStopResponse struct{ http.RoundTripper }

func (l lostStopResponse) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := l.RoundTripper.RoundTrip(request)
	if err == nil && request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/abort") {
		_ = response.Body.Close()
		return nil, errors.New("private lost stop response")
	}
	return response, err
}

func TestManualNativeOpenCodeStop(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private native OpenCode Stop")
	}
	for _, mode := range []string{"text", "permission", "question"} {
		for _, lost := range []bool{false, true} {
			t.Run(mode+"/lost="+fmtBool(lost), func(t *testing.T) { nativeStopFixture(t, mode, lost, liveStopIntent) })
		}
	}
}

func TestManualNativeOpenCodeOwnedStop(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private native OpenCode original owned Stop")
	}
	for _, intent := range []nativeStopIntent{unobservedStopIntent, lostStreamStopIntent} {
		t.Run(string(intent), func(t *testing.T) { nativeStopFixture(t, "text", false, intent) })
	}
}

func TestManualNativeOpenCodeStopRecovery(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private native OpenCode original Stop recovery")
	}
	for _, intent := range []nativeStopIntent{recoverCompletedStopIntent, recoverUnstartedStopIntent} {
		t.Run(string(intent), func(t *testing.T) { nativeStopFixture(t, "text", false, intent) })
	}
}

func TestManualNativeOpenCodeStoppedHistory(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private native OpenCode stopped history")
	}
	for _, mode := range []string{"text", "permission", "question", "completed-race"} {
		for _, lost := range []bool{false, true} {
			t.Run(mode+"/lost="+fmtBool(lost), func(t *testing.T) { nativeStopFixture(t, mode, lost, verifiedHistoryStopIntent) })
		}
	}
}

func nativeStopFixture(t *testing.T, mode string, lost bool, intent nativeStopIntent) {
	natural := mode == "completed-race"
	text := mode == "text" || natural
	key := string(domain.NewID())
	var args atomic.Value
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBody+1))
		var body map[string]json.RawMessage
		if err != nil || domain.Decode(raw, &body) != nil || r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key || !scalar(body["model"], fixtureSettings().Model) || string(body["stream"]) != "true" || calls.Add(1) != 1 {
			t.Error("native Stop escaped original provider scope or continued")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if text {
			_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Private stream awaiting Stop."},"finish_reason":null}]}`+"\n\n")
			if natural {
				_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
				return
			}
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		tool := "read"
		if mode == "question" {
			tool = "question"
		}
		for _, chunk := range []map[string]any{
			{"id": "chatcmpl-private", "object": "chat.completion.chunk", "created": 1, "model": "private-model", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_private_stop", "type": "function", "function": map[string]any{"name": tool, "arguments": args.Load().(string)}}}}, "finish_reason": nil}}},
			{"id": "chatcmpl-private", "object": "chat.completion.chunk", "created": 1, "model": "private-model", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}},
		} {
			raw, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(provider.Close)
	api, ctx := nativeSessionFixture(t, provider.URL, key)
	path := filepath.Join(api.cwd, "private-stop.txt")
	if err := os.WriteFile(path, []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	argument := map[string]any{"filePath": path}
	if mode == "question" {
		argument = map[string]any{"questions": []any{map[string]any{"question": "Private question", "header": "Choice", "options": []any{map[string]any{"label": "First", "description": "Private choice"}}}}}
	}
	raw, _ := json.Marshal(argument)
	args.Store(string(raw))
	if _, err := api.create(ctx, domain.NewID(), fixtureSettings()); err != nil {
		t.Fatal(err)
	}
	stream, err := api.openEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := api.submit(ctx, domain.NewID(), fixtureMessageID, fixturePartID, "Exercise private native Stop."); err != nil {
		t.Fatal(err)
	}
	observer, err := api.observeInput(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if intent == unobservedStopIntent || intent == recoverCompletedStopIntent || intent == recoverUnstartedStopIntent {
		nativeOwnedStopCleanup(t, api, observer, ctx, intent, &calls)
		return
	}
	if lost {
		api.client.Transport = lostStopResponse{api.client.Transport}
	}
	var requested bool
	for count := 0; count < 512; count++ {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		observation, err := observer.observe(ctx, event)
		if err != nil {
			t.Fatalf("native Stop event %s: %v", event.Kind, err)
		}
		if !requested && (observation.Interaction != nil || text && observation.Part != nil && observation.Part.Kind == TextPartKind && observation.Part.MessageID != fixtureMessageID) {
			if intent == lostStreamStopIntent {
				stream.Close()
				_ = observer.interruption(ctx)
				nativeOwnedStopCleanup(t, api, observer, ctx, intent, &calls)
				return
			}
			if natural {
				// Let native completion win without consuming the queued original
				// final events. The original live observer still owns this Stop;
				// an accepted abort cannot rewrite the completed native result.
				for {
					raw, _, err := api.request(ctx, http.MethodGet, "/session/status", nil, http.StatusOK)
					status, decodeErr := object(raw)
					if err != nil || decodeErr != nil {
						t.Fatal("native completion race status unavailable", err, decodeErr)
					}
					if len(status) == 0 {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("native completion race did not reach idle")
					case <-time.After(10 * time.Millisecond):
					}
				}
			}
			receipt, err := api.stopInput(ctx, observer, domain.NewID())
			if (err != nil) != lost || receipt.RequestID.Validate() != nil || receipt.HTTPAccepted == lost || receipt.TerminalObserved || receipt.IdleVerified || receipt.PendingCleared {
				t.Fatalf("native stop claim/delivery/observation: %+v %v", receipt, err)
			}
			requested = true
		}
		if requested && observer.snapshot().SettledObserved {
			receipt, err := api.verifyStop(ctx, observer)
			pending := !text
			unproven := natural && lost
			if (err != nil) != (pending || unproven) || receipt.HTTPAccepted == lost || receipt.InterruptedObserved == natural || !receipt.TerminalObserved || !receipt.IdleObserved || receipt.IdleVerified == (pending || unproven) || receipt.PendingCleared == (pending || unproven) || receipt.RepliesUncertain || receipt.CleanupVerified || calls.Load() != 1 {
				t.Fatalf("native verified Stop: %+v %v", receipt, err)
			}
			// In this pinned version /abort ends the assistant but leaves native
			// permission/question effects pending. Retain that evidence until
			// real joined process cleanup, never synthesize reject/answer calls.
			if intent == verifiedHistoryStopIntent {
				owned := &OwnedAPI{session: api, reading: make(chan struct{}, 1)}
				proof, e := owned.CloseAfterStop(ctx)
				if unproven {
					if e == nil || proof.History.Digest != "" || owned.completionAttempted {
						t.Fatal("normal completion fabricated missing Stop acknowledgment")
					}
					receipt, err = owned.FinishStopCleanup(ctx)
					if err != nil || !receipt.CleanupVerified || receipt.HTTPAccepted || receipt.InterruptedObserved {
						t.Fatal("owner cleanup rewrote an unproven native Stop")
					}
					if proof, err := owned.CloseAfterStop(ctx); err == nil || proof.History.Digest != "" {
						t.Fatal("later owner cleanup fabricated stopped history")
					}
					return
				}
				if e != nil || proof.History.RequestID != observer.input.receipt.RequestID || proof.History.SessionID != observer.input.receipt.SessionID || proof.History.InputID != observer.input.receipt.MessageID || proof.History.AssistantID != observer.progress.AssistantID || len(proof.History.Messages) != len(observer.messages) || len(proof.History.Digest) != 64 {
					t.Fatalf("original stopped history/cleanup: %v", e)
				}
				receipt, err = proof.Stop, e
				again, e := owned.CloseAfterStop(ctx)
				if e != nil || again.Stop != receipt || again.History.Digest != proof.History.Digest || calls.Load() != 1 {
					t.Fatal("stopped history repeated native work or changed original proof")
				}
			} else {
				receipt, err = api.closeStoppedRuntime(ctx, observer)
			}
			if err != nil || !receipt.CleanupVerified || !receipt.PendingCleared || receipt.HTTPAccepted == lost || receipt.InterruptedObserved == natural || receipt.IdleVerified == pending || calls.Load() != 1 {
				t.Fatalf("native Stop ownership cleanup: %+v %v", receipt, err)
			}
			for _, interaction := range observer.interactions {
				if !interaction.canceled || !interaction.closed || interaction.rejected || interaction.attempt != nil {
					t.Fatal("native cancellation fabricated answer/rejection acceptance")
				}
			}
			if _, err := api.stopInput(ctx, observer, domain.NewID()); err == nil {
				t.Fatal("verified stop granted another abort")
			}
			return
		}
	}
	t.Fatal("native Stop did not settle original input")
}

type refuseOwnedStopHTTP struct{ calls atomic.Int32 }

func (r *refuseOwnedStopHTTP) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls.Add(1)
	return nil, errors.New("owned cleanup must not use native HTTP")
}

func nativeOwnedStopCleanup(t *testing.T, api *sessionAPI, observer *inputObserver, ctx context.Context, intent nativeStopIntent, calls *atomic.Int32) {
	t.Helper()
	refuse := &refuseOwnedStopHTTP{}
	api.client.Transport = refuse
	recovering := intent == recoverCompletedStopIntent || intent == recoverUnstartedStopIntent
	if recovering {
		original := api.closeOwned
		api.closeOwned = func(ctx context.Context) error {
			if intent == recoverCompletedStopIntent {
				if err := original(ctx); err != nil {
					return err
				}
			}
			return errors.New("private injected cleanup result loss")
		}
	}
	receipt, err := api.claimOwnedStop(ctx, observer, domain.NewID())
	if err != nil || receipt.RequestID.Validate() != nil || receipt.NativeAttempted || receipt.HTTPAccepted || receipt.CleanupVerified {
		t.Fatalf("native owned Stop claim: %+v %v", receipt, err)
	}
	receipt, err = api.closeStoppedRuntime(ctx, observer)
	if recovering {
		if err == nil || receipt.CleanupVerified || receipt.PendingCleared {
			t.Fatal("injected cleanup uncertainty disappeared")
		}
		receipt, err = api.recoverStoppedRuntime(ctx, observer, domain.NewID())
	}
	if err != nil || !receipt.CleanupVerified || !receipt.PendingCleared || receipt.NativeAttempted || receipt.HTTPAccepted || receipt.InterruptedObserved || receipt.TerminalObserved || receipt.IdleVerified || refuse.calls.Load() != 0 || calls.Load() > 1 {
		t.Fatalf("native owner-only cleanup: %+v %v", receipt, err)
	}
	if intent != lostStreamStopIntent && (observer.snapshot().AssistantID != "" || observer.snapshot().UserSeen) || intent == lostStreamStopIntent && !observer.snapshot().NeedsRecovery {
		t.Fatal("owned cleanup invented missed native observations or repaired an event gap")
	}
	if _, err := api.claimOwnedStop(ctx, observer, domain.NewID()); err == nil {
		t.Fatal("owned Stop consumed another mutation claim")
	}
}
