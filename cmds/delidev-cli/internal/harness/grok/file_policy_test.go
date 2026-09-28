package grok

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func TestManualNativeGrokOriginalRememberedEdits(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native executable required")
	}
	for _, remember := range []bool{false, true} {
		t.Run(fmt.Sprint(remember), func(t *testing.T) {
			config, logs := fixtureAPIConfig(t, "native-file-policy")
			config.Probe.Process.Executable = binary
			config.Model = turnFixtureModel
			path := filepath.Join(config.Workspace, "original.txt")
			if err := os.WriteFile(path, []byte("Original private file.\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var requests atomic.Uint32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/api-proxy/v1/chat/completions" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer "+apiFixtureToken || requests.Add(1) > 12 {
					t.Error("foreign original policy authority")
					w.WriteHeader(400)
					return
				}
				raw, e := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
				var body struct {
					Model    string            `json:"model"`
					Tools    []json.RawMessage `json:"tools"`
					Messages []struct {
						Role string `json:"role"`
						Tool string `json:"tool_call_id"`
					} `json:"messages"`
				}
				if e != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &body) != nil || body.Model != turnFixtureModel {
					t.Error("invalid original policy request")
					w.WriteHeader(400)
					return
				}
				completed := map[string]bool{}
				for _, m := range body.Messages {
					if m.Role == "tool" {
						if m.Tool != "policy-write-1" && m.Tool != "policy-write-2" {
							t.Error("foreign remembered tool result")
							w.WriteHeader(400)
							return
						}
						completed[m.Tool] = true
					}
				}
				delta := map[string]any{"role": "assistant", "content": "Original edits complete."}
				finish := "stop"
				if len(body.Tools) > 0 && len(completed) < 2 {
					n := len(completed) + 1
					args, _ := json.Marshal(map[string]any{"file_path": path, "content": fmt.Sprintf("Original edit %d.\n", n)})
					delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("policy-write-%d", n), "type": "function", "function": map[string]any{"name": "write", "arguments": string(args)}}}}
					finish = "tool_calls"
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, chunk := range []any{
					map[string]any{"id": "chat-policy", "object": "chat.completion.chunk", "created": 1, "model": turnFixtureModel, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}},
					map[string]any{"id": "chat-policy", "object": "chat.completion.chunk", "created": 1, "model": turnFixtureModel, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16}},
				} {
					encoded, _ := json.Marshal(chunk)
					_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
				}
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer provider.Close()
			config.ServerOrigin = provider.URL
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			api, e := openAPI(ctx, config)
			if e != nil {
				t.Fatal(e)
			}
			defer api.Close()
			if _, e := api.Create(ctx, domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); e != nil {
				t.Fatal(e)
			}
			replies, tools, inherited := 0, 0, 0
			var original domain.ID
			result, e := api.RunFileTools(ctx, domain.NewID(), "Verify original remembered edits.", func(context.Context, InputClaim) error { return nil }, func(callback context.Context, v InputObservation) error {
				if v.Permission != nil {
					replies++
					decision := AllowFileOnce
					if remember {
						decision = AllowFileSession
					}
					delivery, e := api.ReplyFilePermission(callback, domain.NewID(), v.Permission.ArrivalID, decision, func(_ context.Context, c FilePermissionClaim) error { return c.Validate() })
					if e != nil || !delivery.Delivered || delivery.Resolved {
						return sessionUncertain()
					}
					if original == "" {
						original = v.Permission.ArrivalID
					}
				}
				if v.FileTool != nil && v.FileTool.Observation != nil && v.FileTool.Observation.Phase == fileToolCompleted {
					tools++
					policy := v.FileTool.InheritedPermission
					if policy != "" {
						if !remember || tools != 2 || policy != original {
							t.Error("remembered edit lost original approval")
							return incompatible()
						}
						inherited++
					}
				}
				return nil
			})
			expectedReplies := 2
			if remember {
				expectedReplies = 1
			}
			if e != nil || tools != 2 || replies != expectedReplies || remember && inherited != 1 || !remember && inherited != 0 || result.Meta.Usage.Input != 33 || result.Meta.Input != 11 {
				t.Fatal("original session edits lost response scope/accounting", e, replies, tools, inherited)
			}
			actual, e := os.ReadFile(path)
			if e != nil || string(actual) != "Original edit 2.\n" {
				t.Fatal("remembered native edits did not apply")
			}
			if e := api.profile.checkInitialized(); e != nil {
				t.Fatal("remembered edits changed initialized configuration", e)
			}
			if e := api.Close(); e != nil {
				t.Fatal(e)
			}
			if e := process.ReconcileOwner(config.Probe.Process.Directory, config.Probe.Process.OwnerID); e != nil {
				t.Fatal(e)
			}
			if e := filepath.WalkDir(filepath.Dir(config.Probe.Home), func(path string, entry os.DirEntry, e error) error {
				if e != nil || entry.IsDir() {
					return e
				}
				raw, e := os.ReadFile(path)
				if bytes.Contains(raw, []byte(config.Token)) {
					t.Error("remembered policy retained execution credentials")
				}
				return e
			}); e != nil {
				t.Fatal(e)
			}
			for _, private := range []string{config.Token, path, "Original edit", config.Workspace} {
				if strings.Contains(logs.String(), private) {
					t.Fatal("policy diagnostics exposed private data")
				}
			}
		})
	}
}

func TestRememberedEditsRequireOriginalCompletedApproval(t *testing.T) {
	events := fileToolEvents(t, true)
	for _, remember := range []bool{false, true} {
		o, _ := newFileToolObserver(turnFixtureSession, turnFixturePrompt)
		for _, e := range events {
			if _, err := o.observe(e); err != nil {
				t.Fatal(err)
			}
		}
		arrival := domain.NewID()
		if remember {
			if err := o.observeEditPolicy(arrival); err != nil {
				t.Fatal(err)
			}
		}
		var last error
		for _, e := range events {
			if e.Kind == nativewire.ServerRequest {
				continue
			}
			raw := strings.ReplaceAll(string(e.Params), "call_delidev_read", "second-write")
			v := fixtureObject([]byte(raw))
			if meta, ok := v["_meta"].(map[string]any); ok {
				if original, ok := meta["eventId"].(string); ok {
					index, _ := eventIndex(original, turnFixtureSession)
					meta["eventId"] = fmt.Sprintf("%s-%d", turnFixtureSession, index+10)
				}
			}
			e.Params, _ = json.Marshal(v)
			var fact fileToolFact
			fact, last = o.observe(e)
			if last != nil {
				break
			}
			if fact.Observation != nil && fact.InheritedPermission != arrival && remember {
				t.Fatal("remembered native tool lost provenance")
			}
		}
		if remember && last != nil || !remember && last == nil {
			t.Fatal("unrequested Write did not require original policy", remember, last)
		}
		if remember && o.observeEditPolicy(domain.NewID()) == nil {
			t.Fatal("original remembered scope replaced")
		}
	}
}

func TestRememberedPolicyCannotPrecedeOriginalResolutionAndCompletion(t *testing.T) {
	for _, scenario := range []string{"original", "once", "undelivered", "unresolved", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			arrival := domain.NewID()
			decision := AllowFileSession
			if scenario == "once" {
				decision = AllowFileOnce
			}
			settled := make(chan struct{})
			close(settled)
			reply := &fileReply{offer: FilePermissionOffer{ArrivalID: arrival, ToolID: "original-write"}, done: settled, observation: FilePermissionDelivery{Claim: FilePermissionClaim{ArrivalID: arrival, Decision: decision}, Claimed: true, Attempted: true, Delivered: scenario != "undelivered"}}
			c := &textControl{profile: fileWriteInput, permissions: map[domain.ID]*fileReply{arrival: reply}}
			if c.originalEditPolicy() != "" {
				t.Fatal("delivery granted remembered edits")
			}
			if scenario != "unresolved" {
				err := c.observeFileReply(context.Background(), fileToolFact{Interaction: &fileInteraction{Kind: fileInteractionResolved, ID: "original-write"}})
				if (err != nil) != (scenario == "undelivered") {
					t.Fatal("resolution lost original delivery ownership", err)
				}
			}
			if c.originalEditPolicy() != "" {
				t.Fatal("resolution alone granted remembered edits")
			}
			phase := fileToolCompleted
			if scenario == "failed" {
				phase = fileToolFailed
			}
			err := c.observeFileReply(context.Background(), fileToolFact{Observation: &fileToolObservation{ID: "original-write", Phase: phase}})
			valid := scenario == "original" || scenario == "once"
			if (err == nil) != valid {
				t.Fatal("invalid original policy completion", err)
			}
			if scenario == "original" {
				if c.originalEditPolicy() != arrival {
					t.Fatal("original policy provenance missing")
				}
			} else if c.originalEditPolicy() != "" {
				t.Fatal("non-policy outcome granted remembered edits")
			}
			if err := c.observeFileReply(context.Background(), fileToolFact{Interaction: &fileInteraction{Kind: fileInteractionResolved, ID: "later-write"}, InheritedPermission: domain.NewID()}); err == nil {
				t.Fatal("foreign remembered permission adopted")
			}
		})
	}
}

func TestRememberedEditPolicyCannotBeReplacedBeforeReply(t *testing.T) {
	arrival, original := domain.NewID(), domain.NewID()
	pending := &fileReply{offer: FilePermissionOffer{ArrivalID: arrival, ToolID: "later-explicit-write"}}
	control := &textControl{profile: fileWriteInput, running: true, editPolicy: original, permissions: map[domain.ID]*fileReply{arrival: pending}}
	api := &apiConnection{control: control}
	if _, err := api.ReplyFilePermission(context.Background(), domain.NewID(), arrival, AllowFileSession, func(context.Context, FilePermissionClaim) error {
		t.Error("replacement policy consumed native response authority")
		return nil
	}); err == nil {
		t.Fatal("original remembered edit scope replaced")
	}
	if pending.done != nil || control.originalEditPolicy() != original {
		t.Fatal("unsupported policy replacement consumed the pending proposal")
	}
}
