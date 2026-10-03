// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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

func TestManualNativeOpenCodeAutomaticCompactionFreshRestoration(t *testing.T) {
	nativeOpenCodeCompactionFixture(t, false, false)
}
func TestManualNativeOpenCodeCompactionLostHTTPResponse(t *testing.T) {
	nativeOpenCodeCompactionFixture(t, true, false)
}

func TestManualNativeOpenCodeOverflowCompactionFreshRestoration(t *testing.T) {
	nativeOpenCodeCompactionFixture(t, false, true)
}

func nativeOpenCodeCompactionFixture(t *testing.T, lost, overflow bool) {
	binary := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit isolated pinned OpenCode compaction fixture")
	}
	requireNoManagedOpenCodeConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var calls atomic.Int32
	var logs bytes.Buffer
	config := fixtureOwnedAPIConfig(t)
	config.Probe.Process.Executable = binary
	config.Probe.Process.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	config.Settings.Permission = []PermissionRule{{Permission: "bash", Pattern: "*", Action: PermissionAllow}}
	config.Settings.Agent = BuildAgent
	const prompt = "Private original automatic compaction input."
	const summary = "Private original native summary."
	const answer = "Private original final answer."
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if json.NewDecoder(io.LimitReader(r.Body, maxHTTPBody)).Decode(&body) != nil || !scalar(body["model"], config.Settings.Model) || string(body["stream"]) != "true" || r.URL.Path != apiproxy.Prefix+"/chat/completions" {
			t.Error("native compaction escaped original model/route")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		call := calls.Add(1)
		if call > 6 {
			t.Error("native compaction repeated an original request")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if call >= 3 && !bytes.Contains(body["messages"], []byte(summary)) {
			t.Error("native successor lost original compacted context")
		}
		if overflow && call == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"maximum context length exceeded","code":"context_length_exceeded","type":"context_length_exceeded"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		id := fmt.Sprintf("chatcmpl-private-compaction-%d", call)
		chunk := func(delta any, finish any, usage any) {
			value := map[string]any{"id": id, "object": "chat.completion.chunk", "created": 1, "model": "private-model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
			if usage != nil {
				value["usage"] = usage
			}
			raw, _ := json.Marshal(value)
			fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		if call == 1 {
			chunk(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call-original-native-compaction", "type": "function", "function": map[string]any{"name": "bash", "arguments": "{\"command\":\"printf native-compaction-tool\",\"description\":\"Private fixture command\"}"}}}}, nil, nil)
			chunk(map[string]any{}, "tool_calls", map[string]any{"prompt_tokens": 31500, "completion_tokens": 4, "total_tokens": 31504})
		} else {
			text := answer
			if call == 2 || call == 4 || call == 5 {
				text = summary
			}
			chunk(map[string]any{"role": "assistant", "content": text}, nil, nil)
			chunk(map[string]any{}, "stop", map[string]any{"prompt_tokens": 20, "completion_tokens": 4, "total_tokens": 24})
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()
	config.ServerOrigin = provider.URL
	claimCount := 0
	config.Claim = func(_ context.Context, c SessionClaim) error {
		if c.Validate() != nil {
			return sessionInvalid()
		}
		claimCount++
		raw, _ := json.Marshal(c)
		return security.WriteAtomic(filepath.Join(filepath.Dir(filepath.Dir(config.Probe.Home)), fmt.Sprintf("claim-%d.json", claimCount)), raw)
	}
	api, err := OpenOwnedAPI(ctx, config)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	defer api.Close(context.Background())
	if _, err = api.CreateSession(ctx, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	if _, err = api.StartText(ctx, domain.NewID(), prompt); err != nil {
		t.Fatal(err)
	}
	started, completed := 0, 0
	for n := 0; n < 512; n++ {
		observed, err := api.Next(ctx)
		if err != nil {
			t.Fatal("native automatic compaction observation failed", err, logs.String())
		}
		if observed.Compaction != nil {
			if observed.Compaction.Stage == domain.NativeCompactionStarted {
				started++
			} else {
				completed++
			}
		}
		progress, err := api.Progress(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if progress.SettledObserved {
			break
		}
	}
	history, err := api.CloseCompleted(ctx)
	if err != nil {
		t.Fatal("native compacted history/cleanup failed", err, logs.String())
	}
	raw, ref, err := api.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatal("native compacted checkpoint failed", err, logs.String())
	}
	home := filepath.Dir(config.Probe.Home)
	cp, err := decodeCheckpoint(raw, ref, home)
	if err != nil || cp.Context == nil || len(cp.Context.Records) != 1 || started != 1 || completed != 1 || calls.Load() != 3 || len(history.Messages) < 6 || checkpointReplacementProfile(cp) != nil {
		t.Fatal("original automatic compaction did not retain complete replacement proof", err, started, completed, calls.Load(), len(history.Messages), logs.String())
	}
	for _, secret := range []string{prompt, summary, answer, config.Token} {
		if bytes.Contains(raw, []byte(secret)) || bytes.Contains(logs.Bytes(), []byte(secret)) {
			t.Fatal("private native content entered checkpoint/logs")
		}
	}
	for actionNumber := 0; actionNumber < 2; actionNumber++ {
		manual := fixtureOwnedAPIConfig(t)
		manual.Probe.Process.Executable = binary
		manual.Probe.Process.Logger = config.Probe.Process.Logger
		manual.Workspace, manual.NativeRoot, manual.Settings, manual.ServerOrigin = config.Workspace, config.NativeRoot, config.Settings, provider.URL
		manual.Token = apiproxy.TokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(8 + actionNumber)}, 32))
		manualClaims := []SessionClaim{}
		manual.Claim = func(_ context.Context, c SessionClaim) error {
			if c.Validate() != nil {
				return sessionInvalid()
			}
			manualClaims = append(manualClaims, c)
			raw, _ := json.Marshal(c)
			return security.WriteAtomic(filepath.Join(filepath.Dir(filepath.Dir(manual.Probe.Home)), string(c.RequestID)+".json"), raw)
		}
		restored, err := OpenResumedAPI(ctx, manual, home, raw, ref, domain.NewID(), BuildAgent, false)
		if err != nil {
			t.Fatal("manual original restore failed", actionNumber, err, logs.String())
		}
		defer restored.Close(context.Background())
		var loss *lostCompactionResponse
		if lost && actionNumber == 0 {
			loss = &lostCompactionResponse{RoundTripper: restored.session.client.Transport}
			restored.session.client.Transport = loss
		}
		action := domain.NewID()
		if receipt, err := restored.StartCompaction(ctx, action); err != nil || !receipt.NativeAttempted {
			t.Fatal("manual native claim failed", actionNumber, err, logs.String())
		}
		if _, err := restored.StartCompaction(ctx, action); err == nil || len(manualClaims) != 2 {
			t.Fatal("native manual summarize was replayed")
		}
		if _, err := restored.StartText(ctx, domain.NewID(), "Private forbidden input."); err == nil || len(manualClaims) != 2 {
			t.Fatal("manual action created a synthetic input")
		}
		for n := 0; n < 512; n++ {
			if _, err := restored.Next(ctx); err != nil {
				t.Fatal("manual original observation failed", actionNumber, err, logs.String())
			}
			progress, _ := restored.Progress(ctx)
			if progress.SettledObserved {
				break
			}
		}
		if lost && actionNumber == 0 {
			if _, err := restored.CloseCompleted(ctx); err == nil {
				t.Fatal("lost native HTTP response acquired cleanup/checkpoint authority")
			}
			if _, _, err := restored.RetainCheckpoint(ctx); err == nil {
				t.Fatal("unacknowledged native action gained a checkpoint")
			}
			receipt, err := restored.CompactionReceipt(ctx)
			if err == nil || receipt.HTTPAccepted || !receipt.LifecycleCompleted || loss.posts.Load() != 1 || calls.Load() != 4 || len(manualClaims) != 2 {
				t.Fatal("uncertain native summarize lost once-only ownership", err)
			}
			if _, err := restored.StartCompaction(ctx, action); err == nil || loss.posts.Load() != 1 {
				t.Fatal("uncertain summarize was resent")
			}
			if InspectCheckpoint(ctx, home, raw, ref) != nil {
				t.Fatal("uncertain summarize mutated original protected source")
			}
			return
		}
		if _, err := restored.CloseCompleted(ctx); err != nil {
			t.Fatal("manual history or cleanup failed", actionNumber, err, logs.String())
		}
		receipt, err := restored.CompactionReceipt(ctx)
		if err != nil || !receipt.HTTPAccepted || !receipt.LifecycleCompleted || !receipt.CleanupVerified || receipt.ActionID != action || receipt.Context.Auto || receipt.Context.SourceInputID != ref.InputID {
			t.Fatal("manual HTTP substituted for original lifecycle", actionNumber, err, logs.String())
		}
		current, currentRef, err := restored.RetainCheckpoint(ctx)
		if err != nil || InspectCheckpoint(ctx, home, raw, ref) != nil {
			t.Fatal("manual retention changed original source", actionNumber, err, logs.String())
		}
		currentHome := filepath.Dir(manual.Probe.Home)
		compacted, err := decodeCheckpoint(current, currentRef, currentHome)
		if err != nil || compacted.Context == nil || len(compacted.Context.Records) != actionNumber+2 || len(compacted.Context.Inventory) != len(cp.Context.Inventory)+2*(actionNumber+1) || compacted.History.Digest != cp.History.Digest {
			t.Fatal("manual retention lost repeated complete lineage", actionNumber, err, logs.String())
		}
		home, raw, ref = currentHome, current, currentRef
	}
	replacement := fixtureOwnedAPIConfig(t)
	replacement.Probe.Process.Executable = binary
	replacement.Probe.Process.Logger = config.Probe.Process.Logger
	replacement.Workspace, replacement.NativeRoot, replacement.Settings, replacement.ServerOrigin = config.Workspace, config.NativeRoot, config.Settings, provider.URL
	replacement.Token = apiproxy.TokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	replacement.Claim = func(_ context.Context, c SessionClaim) error {
		if c.Validate() != nil {
			return sessionInvalid()
		}
		raw, _ := json.Marshal(c)
		return security.WriteAtomic(filepath.Join(filepath.Dir(filepath.Dir(replacement.Probe.Home)), string(c.RequestID)+".json"), raw)
	}
	next, err := OpenResumedAPI(ctx, replacement, home, raw, ref, domain.NewID(), BuildAgent, false)
	if err != nil {
		t.Fatal("native fresh compacted restoration failed", err, logs.String())
	}
	defer next.Close(context.Background())
	if _, err = next.StartText(ctx, domain.NewID(), "Private original successor."); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 512; n++ {
		if _, err = next.Next(ctx); err != nil {
			t.Fatal(err, logs.String())
		}
		p, _ := next.Progress(ctx)
		if p.SettledObserved {
			break
		}
	}
	if _, err = next.CloseCompleted(ctx); err != nil {
		t.Fatal(err, logs.String())
	}
	nextRaw, nextRef, err := next.RetainCheckpoint(ctx)
	if err != nil || InspectCheckpoint(ctx, home, raw, ref) != nil || calls.Load() != 6 {
		t.Fatal("native successor changed original ownership or history", err, logs.String())
	}
	continued, err := decodeCheckpoint(nextRaw, nextRef, filepath.Dir(replacement.Probe.Home))
	if err != nil || continued.Context == nil || len(continued.Context.Records) != 3 || len(continued.Context.Inventory) != len(cp.Context.Inventory)+6 || strings.Contains(string(nextRaw), summary) {
		t.Fatal("ordinary successor lost inherited complete context", err)
	}
}

type lostCompactionResponse struct {
	http.RoundTripper
	posts atomic.Int32
}

func (l *lostCompactionResponse) RoundTrip(r *http.Request) (*http.Response, error) {
	target := r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/summarize")
	if target {
		l.posts.Add(1)
	}
	response, err := l.RoundTripper.RoundTrip(r)
	if target && err == nil {
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return nil, errors.New("private fixture lost original summarize response")
	}
	return response, err
}
