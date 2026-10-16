// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// This installed-engine test uses only scripted loopback responses and a local
// synthetic PNG. It proves native tool dispatch, not real OpenRouter access.
func TestManualNativeOpenRouterToolProfileDispatchesImageAndSearch(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed Codex opt-in required; local scripted provider only")
	}
	version, err := exec.Command(binary, "--version").Output()
	if err != nil {
		t.Fatal("installed Codex version unavailable")
	}
	observed := strings.TrimPrefix(strings.TrimSpace(string(version)), "codex-cli ")
	if !domain.CodexVersionAllowed(observed) {
		t.Fatal("installed native version is not admitted")
	}
	var pixels bytes.Buffer
	original := image.NewNRGBA(image.Rect(0, 0, 768, 768))
	random := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < len(original.Pix); i += 4 {
		value := random.Uint32()
		original.Pix[i] = byte(value)
		original.Pix[i+1] = byte(value >> 8)
		original.Pix[i+2] = byte(value >> 16)
		original.Pix[i+3] = 255
	}
	if err := png.Encode(&pixels, original); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(pixels.Bytes())
	var inference, images, searches atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/api-proxy/v1/images/generations":
			images.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"created": uint64(123), "data": []any{map[string]string{"b64_json": encoded}}})
		case "/api-proxy/v1/alpha/search":
			searches.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"output":"Original scripted search result.","results":[],"encrypted_output":null}`)
		case "/api-proxy/v1/responses":
			call := inference.Add(1)
			if call > 1 {
				var walk func(any)
				walk = func(value any) {
					switch v := value.(type) {
					case map[string]any:
						for _, item := range v {
							walk(item)
						}
					case []any:
						for _, item := range v {
							walk(item)
						}
					case string:
						if _, after, ok := strings.Cut(v, "DELIDEV_TOOL_INVENTORY:"); ok {
							var names []string
							if json.NewDecoder(strings.NewReader(after)).Decode(&names) == nil {
								t.Logf("installed native tool inventory: %v", names)
							}
						}
					}
				}
				var body any
				if json.Unmarshal(requestBody, &body) == nil {
					walk(body)
				}
			}
			var item map[string]any
			if call == 1 {
				item = map[string]any{"type": "custom_tool_call", "id": "ct_fixture", "call_id": "call_fixture", "name": "exec", "namespace": "functions", "input": "text('DELIDEV_TOOL_INVENTORY:'+JSON.stringify(ALL_TOOLS.map(t=>t.name))); const result = await tools.delidev_image__imagegen({prompt:'A local fixture image',transparent_background:false}); image(result); text(await tools.web__run({search_query:[{q:'local fixture'}]}));"}
			} else {
				item = map[string]any{"type": "message", "id": "msg_fixture", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": "Native tool fixture complete."}}}
			}
			w.Header().Set("Content-Type", "text/event-stream")
			for _, frame := range []any{map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_fixture", "status": "in_progress"}}, map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}, map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_fixture", "status": "completed", "output": []any{}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}} {
				raw, _ := json.Marshal(frame)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			}
		default:
			t.Errorf("native tool escaped selected fixed routes: %s", r.URL.Path)
			http.Error(w, "unsupported", 404)
		}
	}))
	defer provider.Close()
	cfg := nativeFixtureConfig(t, binary, provider.URL)
	cfg.Version = observed
	cfg.DynamicTools, cfg.EnableImageGeneration = true, true
	cfg.ImageRoot, cfg.ImageMachineID = t.TempDir(), domain.NewID()
	cfg.API = &APIConfig{OpenRouterTools: true, ServerOrigin: provider.URL, Token: apiFixtureToken()}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	client, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	settings := ThreadSettings{Model: "openai/gpt-6-luna", Provider: APIProvider, Effort: "high", Cwd: cfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionFullAccess, ApprovalPolicy: "on-request"}}
	if _, err := client.StartThread(ctx, domain.NewID(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "너가 쓸 수 있는 툴들 다 한번씩 써봐"}); err != nil {
		t.Fatal(err)
	}
	sawImage, sawSearch := false, false
	for {
		event, err := client.NextEvent(ctx)
		if err != nil {
			t.Fatalf("native event wait failed: %v; image=%v search=%v requests=%d/%d/%d", err, sawImage, sawSearch, images.Load(), searches.Load(), inference.Load())
		}
		if event.Kind == DynamicToolEvent && event.DynamicTool.Stage == domain.DynamicRequested {
			_, err := client.ReplyDynamicImage(ctx, event.DynamicTool.Request.Token, event.TurnID, domain.NewID(), func() error { return nil }, func(ctx context.Context, raw json.RawMessage) (string, error) {
				request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.URL+"/api-proxy/v1/images/generations", bytes.NewReader(raw))
				if err != nil {
					return "", err
				}
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					return "", err
				}
				defer response.Body.Close()
				var result struct {
					Data []struct {
						Base64 string `json:"b64_json"`
					} `json:"data"`
				}
				if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
					return "", err
				}
				return "data:image/png;base64," + result.Data[0].Base64, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		if event.Kind == DynamicToolEvent && event.DynamicTool.Stage == domain.DynamicCompleted && event.DynamicTool.Snapshot.Identity.Tool == "imagegen" {
			sawImage = event.DynamicTool.Snapshot.Success != nil && *event.DynamicTool.Snapshot.Success
		}
		if event.Kind == DynamicToolEvent && event.DynamicTool.Stage == domain.DynamicCompleted && event.DynamicTool.Snapshot.Identity.Tool == "web_search" {
			sawSearch = true
		}
		if event.Kind == TurnCompletedEvent {
			if event.Turn == nil || event.Turn.Status != TurnCompleted {
				t.Fatal("native tools did not complete the original turn")
			}
			break
		}
	}
	if !sawImage || !sawSearch || images.Load() != 1 || searches.Load() != 1 || inference.Load() != 2 {
		t.Fatalf("native tool profile did not execute both tools exactly once: image=%v search=%v imageCalls=%d searchCalls=%d inference=%d", sawImage, sawSearch, images.Load(), searches.Load(), inference.Load())
	}
}
