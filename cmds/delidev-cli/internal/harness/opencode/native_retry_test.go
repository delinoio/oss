package opencode

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type nativeRetryMode string

const (
	nativeRetryContinue nativeRetryMode = "continue"
	nativeRetryStop     nativeRetryMode = "stop"
	nativeRetryLostStop nativeRetryMode = "lost-stop"
)

func TestManualNativeOpenCodeOriginalRetry(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private pinned native OpenCode retry fixture")
	}
	for _, mode := range []nativeRetryMode{nativeRetryContinue, nativeRetryStop, nativeRetryLostStop} {
		t.Run(string(mode), func(t *testing.T) { nativeRetryFixture(t, mode) })
	}
}

func nativeRetryFixture(t *testing.T, mode nativeRetryMode) {
	t.Helper()
	key := string(domain.NewID())
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBody+1))
		var body map[string]json.RawMessage
		if err != nil || domain.Decode(raw, &body) != nil || r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key || !scalar(body["model"], fixtureSettings().Model) || string(body["stream"]) != "true" {
			t.Error("native retry escaped the original provider/model scope")
			w.WriteHeader(400)
			return
		}
		call := calls.Add(1)
		if call == 1 {
			w.Header().Set("Content-Type", "application/json")
			wait := "0.05"
			if mode != nativeRetryContinue {
				wait = "60"
			}
			w.Header().Set("Retry-After", wait)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"Private temporary provider failure","type":"server_error"}}`)
			return
		}
		if mode != nativeRetryContinue || call != 2 {
			t.Error("native Stop or retry repeated the original provider request")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Original retry completed."},"finish_reason":null}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	api, ctx := nativeSessionFixture(t, provider.URL, key)
	if _, err := api.create(ctx, domain.NewID(), fixtureSettings()); err != nil {
		t.Fatal(err)
	}
	stream, err := api.openEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := api.submit(ctx, domain.NewID(), fixtureMessageID, fixturePartID, "Exercise the original native retry."); err != nil {
		t.Fatal(err)
	}
	observer, err := api.observeInput(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	owned := &OwnedAPI{session: api, reading: make(chan struct{}, 1)}
	var retries []domain.OpenCodeStopRetryObservation
	for count := 0; count < 512; count++ {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		observation, err := observer.observe(ctx, event)
		if err != nil {
			t.Fatalf("original retry observation %s failed: %v", event.Kind, err)
		}
		if observation.Retry != nil {
			r := observation.Retry
			if r.Attempt != 1 || r.Next == 0 || len(retries) != 0 {
				t.Fatal("original retry lost its independent scheduling facts")
			}
			retries = append(retries, domain.OpenCodeStopRetryObservation{NativeEventID: observation.EventID, Attempt: r.Attempt, Next: r.Next})
			if mode != nativeRetryContinue {
				if mode == nativeRetryLostStop {
					api.client.Transport = lostStopResponse{api.client.Transport}
				}
				stop, err := owned.Interrupt(ctx, domain.NewID())
				if (err != nil) != (mode == nativeRetryLostStop) || !stop.NativeAttempted || stop.HTTPAccepted != (mode == nativeRetryStop) {
					t.Fatal("already observed backoff lost its original Stop claim", err)
				}
			}
		}
		p := observer.snapshot()
		if p.SettledObserved || mode == nativeRetryLostStop && p.Status == NativeStatusIdle && p.IdleNotification && observer.messages[p.AssistantID] != nil && observer.messages[p.AssistantID].finalized {
			break
		}
	}
	if len(retries) != 1 {
		t.Fatal("native retry was not observed")
	}
	if mode == nativeRetryContinue {
		proof, err := owned.CloseCompleted(ctx)
		if err != nil || proof.AssistantID == "" || calls.Load() != 2 {
			t.Fatal("native retry did not preserve complete original history and cleanup", err)
		}
		return
	}
	proof, err := owned.CloseAfterStop(ctx)
	if mode == nativeRetryLostStop {
		if err == nil || proof.History.Digest != "" {
			t.Fatal("lost abort HTTP fabricated canceled-backoff completion")
		}
		stop, err := owned.FinishStopCleanup(ctx)
		if err != nil || !stop.CleanupVerified || stop.HTTPAccepted || stop.RetryCanceledObserved || stop.InterruptedObserved {
			t.Fatal("independent owner cleanup changed original unknown cancellation", err)
		}
		return
	}
	if err != nil || !proof.Stop.RetryCanceledObserved || proof.Stop.InterruptedObserved || len(proof.Retries) != 1 || proof.Retries[0] != retries[0] || calls.Load() != 1 {
		t.Fatal("original backoff Stop lost history or invented a native error", err)
	}
}
