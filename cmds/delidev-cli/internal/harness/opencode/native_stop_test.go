package opencode

import (
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

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
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
			t.Run(mode+"/lost="+fmtBool(lost), func(t *testing.T) { nativeStopFixture(t, mode, lost) })
		}
	}
}

func nativeStopFixture(t *testing.T, mode string, lost bool) {
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
		if mode == "text" {
			_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Private stream awaiting Stop."},"finish_reason":null}]}`+"\n\n")
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
		if !requested && (observation.Interaction != nil || mode == "text" && observation.Part != nil && observation.Part.Kind == TextPartKind && observation.Part.MessageID != fixtureMessageID) {
			receipt, err := api.stopInput(ctx, observer, domain.NewID())
			if (err != nil) != lost || receipt.RequestID.Validate() != nil || receipt.HTTPAccepted == lost || receipt.TerminalObserved || receipt.IdleVerified || receipt.PendingCleared {
				t.Fatalf("native stop claim/delivery/observation: %+v %v", receipt, err)
			}
			requested = true
		}
		if requested && observer.snapshot().SettledObserved {
			receipt, err := api.verifyStop(ctx, observer)
			pending := mode != "text"
			if (err != nil) != pending || receipt.HTTPAccepted == lost || !receipt.InterruptedObserved || !receipt.TerminalObserved || !receipt.IdleObserved || receipt.IdleVerified == pending || receipt.PendingCleared == pending || receipt.RepliesUncertain || receipt.CleanupVerified || calls.Load() != 1 {
				t.Fatalf("native verified Stop: %+v %v", receipt, err)
			}
			// In this pinned version /abort ends the assistant but leaves native
			// permission/question effects pending. Retain that evidence until
			// real joined process cleanup, never synthesize reject/answer calls.
			receipt, err = api.closeStoppedRuntime(ctx, observer)
			if err != nil || !receipt.CleanupVerified || !receipt.PendingCleared || receipt.HTTPAccepted == lost || !receipt.InterruptedObserved || receipt.IdleVerified == pending || calls.Load() != 1 {
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
