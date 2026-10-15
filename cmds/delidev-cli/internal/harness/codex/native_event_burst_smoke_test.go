package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// burstReasoningBytes is the reasoning text length fragmented into deltas.
const burstReasoningBytes = 2600

// burstReasoningText returns deterministic text whose bytes are position
// dependent, so reordered or dropped deltas change the reconstruction.
func burstReasoningText() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	text := make([]byte, burstReasoningBytes)
	for i := range text {
		text[i] = alphabet[(i*7+i/len(alphabet))%len(alphabet)]
	}
	return string(text)
}

// burstReasoningFrames builds the scripted loopback stream: one reasoning item
// fragmented into deltaCount deltas followed by a completed assistant message.
func burstReasoningFrames(text string, deltaCount int) []map[string]any {
	frames := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_fixture", "status": "in_progress"}},
		{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "reasoning", "id": "rs_fixture", "summary": []any{}, "content": []any{}}},
	}
	for i := 0; i < deltaCount; i++ {
		frame := map[string]any{"type": "response.reasoning_text.delta", "item_id": "rs_fixture", "output_index": 0, "content_index": 0, "delta": text[i*len(text)/deltaCount : (i+1)*len(text)/deltaCount]}
		frames = append(frames, frame)
	}
	return append(frames,
		map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "reasoning", "id": "rs_fixture", "summary": []any{}, "content": []any{map[string]any{"type": "reasoning_text", "text": text}}}},
		map[string]any{"type": "response.output_item.done", "output_index": 1, "item": map[string]any{"type": "message", "id": "msg_fixture", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Fixture complete."}}}},
		map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_fixture", "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
	)
}

// TestManualNativeEventBurstSmoke drives the installed native executable through
// a private loopback Responses endpoint. A reasoning text fragmented into many
// small deltas must survive a consumer that waits after every nonterminal event.
func TestManualNativeEventBurstSmoke(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native event burst smoke with a local scripted provider only")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("the native fixture executable must be absolute")
	}
	text := burstReasoningText()
	for _, test := range []struct {
		name          string
		deltas        int
		providerPause time.Duration
		consumerPause time.Duration
	}{
		{name: "80 immediate deltas with a slow consumer", deltas: 80, consumerPause: 35 * time.Millisecond},
		{name: "200 immediate deltas with a slow consumer", deltas: 200, consumerPause: 35 * time.Millisecond},
		{name: "200 paced deltas with a slow consumer", deltas: 200, providerPause: 40 * time.Millisecond, consumerPause: 35 * time.Millisecond},
		{name: "200 immediate deltas with a fast consumer", deltas: 200},
		{name: "2600 one-byte immediate deltas with a slow consumer", deltas: burstReasoningBytes, consumerPause: 35 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int64
			frames := burstReasoningFrames(text, test.deltas)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/responses" {
					t.Errorf("unexpected fixture operation %s %s", r.Method, r.URL.Path)
					http.Error(w, "unsupported", 404)
					return
				}
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				flusher, _ := w.(http.Flusher)
				for _, frame := range frames {
					if frame["type"] == "response.reasoning_text.delta" && test.providerPause > 0 {
						time.Sleep(test.providerPause)
					}
					raw, _ := json.Marshal(frame)
					fmt.Fprintf(w, "data: %s\n\n", raw)
					if flusher != nil {
						flusher.Flush()
					}
				}
			}))
			defer provider.Close()
			// An isolated deadline covers the intentional consumer delay of the largest case.
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			cfg := nativeFixtureConfig(t, binary, provider.URL)
			root := filepath.Dir(cfg.Home)
			client, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Close() })
			settings := ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Effort: "high", Cwd: filepath.Join(root, "workspace"), Instructions: "Temporary non-inference thread validation.", Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
			started, err := client.StartThread(ctx, domain.NewID(), settings)
			if err != nil || started.Thread == nil {
				t.Fatal("thread did not start", err)
			}
			if _, err := client.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Use the private scripted loopback response."}); err != nil {
				t.Fatal(err)
			}
			var reconstructed strings.Builder
			consumed := 0
			for {
				event, err := client.NextEvent(ctx)
				if err != nil {
					t.Fatalf("event %d failed: %v", consumed, err)
				}
				consumed++
				if event.Kind == NativeExtensionEvent {
					t.Fatalf("native execution requires an additional typed adapter: %s", event.ExtensionStage)
				}
				if event.Kind == ArtifactDeltaEvent && event.ArtifactDelta != nil && event.ArtifactDelta.Kind == ReasoningContentDelta {
					reconstructed.WriteString(event.ArtifactDelta.Text)
				}
				if event.Kind == TurnCompletedEvent {
					if event.Turn == nil || event.Turn.Status != TurnCompleted || !event.Correlated {
						t.Fatal("fixture turn did not complete")
					}
					break
				}
				time.Sleep(test.consumerPause)
			}
			t.Logf("consumed %d native events for %d deltas", consumed, test.deltas)
			if reconstructed.String() != text || reconstructed.Len() != burstReasoningBytes {
				t.Fatalf("reasoning text was dropped or reordered: %d bytes", reconstructed.Len())
			}
			if requests.Load() != 1 {
				t.Fatalf("expected one provider request, got %d", requests.Load())
			}
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			if err := process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
