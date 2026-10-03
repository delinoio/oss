// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestManualNativeOpenCodePruningAndPreservedTailRestoration(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit isolated pinned OpenCode pruning fixture")
	}
	requireNoManagedOpenCodeConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	config := fixtureOwnedAPIConfig(t)
	config.Probe.Process.Executable = binary
	config.ContextLimit, config.OutputLimit, config.Prune = 1_000_000, 1000, true
	config.Settings.Agent, config.Settings.Permission = BuildAgent, []PermissionRule{{Permission: "read", Pattern: "*", Action: PermissionAllow}}
	var logs bytes.Buffer
	config.Probe.Process.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	paths := make([]string, 12)
	text := strings.Repeat(strings.Repeat("a", 160)+"\n", 200)
	for i := range paths {
		paths[i] = filepath.Join(config.Workspace, fmt.Sprintf("original-large-plain-text-%d.txt", i))
		if err := os.WriteFile(paths[i], []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if r.Method != http.MethodPost || r.URL.Path != apiproxy.Prefix+"/chat/completions" || json.NewDecoder(io.LimitReader(r.Body, maxHTTPBody)).Decode(&body) != nil || !scalar(body["model"], config.Settings.Model) || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+apiproxy.TokenPrefix) {
			t.Error("pruning escaped original private route/model")
			w.WriteHeader(403)
			return
		}
		n := calls.Add(1)
		if n > 6 {
			t.Error("pruning replayed native inference")
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		id := fmt.Sprintf("chat_pruning_%d", n)
		chunk := func(delta any, finish any) {
			v := map[string]any{"id": id, "object": "chat.completion.chunk", "model": config.Settings.Model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 4, "total_tokens": 24}}
			raw, _ := json.Marshal(v)
			fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		if n == 1 {
			var tools []any
			for i := 0; i < 12; i++ {
				arguments, _ := json.Marshal(map[string]any{"filePath": paths[i]})
				tools = append(tools, map[string]any{"index": i, "id": fmt.Sprintf("call_pruning_%d", i), "type": "function", "function": map[string]any{"name": "read", "arguments": string(arguments)}})
			}
			chunk(map[string]any{"role": "assistant", "tool_calls": tools}, nil)
			chunk(map[string]any{}, "tool_calls")
		} else {
			answer := "Original ordinary retained answer"
			if n == 2 {
				answer = strings.Repeat("Original retained ordinary answer. ", 2400)
			}
			if n == 5 {
				answer = "Original independently compacted summary"
			}
			chunk(map[string]any{"role": "assistant", "content": answer}, nil)
			chunk(map[string]any{}, "stop")
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()
	config.ServerOrigin = provider.URL
	config.Claim = func(_ context.Context, c SessionClaim) error { return c.Validate() }
	api, err := OpenOwnedAPI(ctx, config)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	defer api.Close(context.Background())
	if _, err := api.CreateSession(ctx, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	settle := func(api *OwnedAPI) {
		t.Helper()
		for n := 0; n < 4096; n++ {
			if _, err := api.Next(ctx); err != nil {
				t.Fatal(err, logs.String())
			}
			p, err := api.Progress(ctx)
			if err != nil || p.NeedsRecovery {
				t.Fatal("native pruning observation uncertain", err, logs.String())
			}
			if p.SettledObserved {
				return
			}
		}
		t.Fatal("native pruning did not settle")
	}
	if _, err := api.StartText(ctx, domain.NewID(), "Read the original large file using the assigned native tools."); err != nil {
		t.Fatal(err)
	}
	settle(api)
	if _, err := api.CloseCompleted(ctx); err != nil {
		t.Fatal(err, logs.String())
	}
	raw, ref, err := api.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	home := filepath.Dir(config.Probe.Home)
	cp, err := decodeCheckpoint(raw, ref, home)
	if err != nil || cp.Tools == nil || len(cp.Tools.Parts) != 12 {
		t.Fatal("original complete tool proof unavailable", err, logs.String())
	}
	makeReplacement := func() APIExecutionConfig {
		fresh := fixtureOwnedAPIConfig(t)
		nonce, err := security.RandomToken()
		if err != nil {
			t.Fatal(err)
		}
		fresh.Token = apiproxy.TokenPrefix + nonce
		fresh.Probe.Process.Executable, fresh.Probe.Process.Logger = binary, config.Probe.Process.Logger
		fresh.Workspace, fresh.NativeRoot, fresh.Settings, fresh.ServerOrigin = config.Workspace, config.NativeRoot, config.Settings, config.ServerOrigin
		fresh.ContextLimit, fresh.OutputLimit, fresh.Prune = config.ContextLimit, config.OutputLimit, true
		fresh.Claim = func(_ context.Context, c SessionClaim) error { return c.Validate() }
		return fresh
	}
	secondConfig := makeReplacement()
	second, err := OpenResumedAPI(ctx, secondConfig, home, raw, ref, domain.NewID(), BuildAgent, false)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	defer second.Close(context.Background())
	if _, err := second.StartText(ctx, domain.NewID(), "Keep this recent small plain-text turn."); err != nil {
		t.Fatal(err)
	}
	settle(second)
	if _, err := second.CloseCompleted(ctx); err != nil {
		t.Fatal("original native pruning/history failed", err, logs.String())
	}
	secondRaw, secondRef, err := second.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	secondHome := filepath.Dir(secondConfig.Probe.Home)
	// Native pruning considers tool outputs only after two newer user turns.
	// Restore a fresh ordinary owner for the third turn rather than inventing
	// native history or sending an auxiliary preparation inference.
	thirdConfig := makeReplacement()
	third, err := OpenResumedAPI(ctx, thirdConfig, secondHome, secondRaw, secondRef, domain.NewID(), BuildAgent, false)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	defer third.Close(context.Background())
	if _, err := third.StartText(ctx, domain.NewID(), "Keep the second recent small plain-text turn."); err != nil {
		t.Fatal(err)
	}
	settle(third)
	if _, err := third.CloseCompleted(ctx); err != nil {
		t.Fatal(err, logs.String())
	}
	prunedRaw, prunedRef, err := third.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	prunedHome := filepath.Dir(thirdConfig.Probe.Home)
	secondCP, err := decodeCheckpoint(prunedRaw, prunedRef, prunedHome)
	if err != nil || secondCP.Context == nil || len(secondCP.Context.Pruned) == 0 || len(secondCP.Context.Records) != 0 || len(secondCP.Previous) != 2 || InspectCheckpoint(ctx, home, raw, ref) != nil {
		t.Fatal("native pruning was fabricated or lost original conversation/source", err, logs.String())
	}
	manualConfig := makeReplacement()
	manual, err := OpenResumedAPI(ctx, manualConfig, prunedHome, prunedRaw, prunedRef, domain.NewID(), BuildAgent, false)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	defer manual.Close(context.Background())
	action := domain.NewID()
	if _, err := manual.StartCompaction(ctx, action); err != nil {
		t.Fatal(err)
	}
	settle(manual)
	if _, err := manual.CloseCompleted(ctx); err != nil {
		t.Fatal(err, logs.String())
	}
	manualRaw, manualRef, err := manual.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	manualHome := filepath.Dir(manualConfig.Probe.Home)
	record, _, err := InspectCompactionCheckpoint(ctx, manualHome, manualRaw, manualRef, prunedHome, prunedRaw, prunedRef, action)
	if err != nil || record.TailStartID == "" || record.TailStartID != secondRef.InputID {
		t.Fatal("native preserved recent tail lacks original ownership", err, logs.String())
	}
	lastConfig := makeReplacement()
	last, err := OpenResumedAPI(ctx, lastConfig, manualHome, manualRaw, manualRef, domain.NewID(), BuildAgent, false)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	defer last.Close(context.Background())
	if _, err := last.StartText(ctx, domain.NewID(), "Continue through preserved native tail."); err != nil {
		t.Fatal(err)
	}
	settle(last)
	if _, err := last.CloseCompleted(ctx); err != nil {
		t.Fatal(err, logs.String())
	}
	if _, _, err := last.RetainCheckpoint(ctx); err != nil || calls.Load() != 6 {
		t.Fatal("native pruning/tail lineage failed fresh continuation", err, calls.Load(), logs.String())
	}
}
