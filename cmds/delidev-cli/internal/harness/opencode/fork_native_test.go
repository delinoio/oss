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

func TestManualNativeOpenCodeGeneralChatFork(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost_all_acknowledgments_%t", lost), func(t *testing.T) { runNativeGeneralChatFork(t, lost) })
	}
}

func runNativeGeneralChatFork(t *testing.T, lost bool) {
	binary := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit isolated pinned native General Chat fork")
	}
	requireNoManagedOpenCodeConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	initial := fixtureOwnedAPIConfig(t)
	initial.Probe.Process.Executable = binary
	initial.Settings.Agent, initial.Settings.Permission = BuildAgent, []PermissionRule{}
	initial.ContextLimit = 1_000_000
	initial.Instructions = "Private immutable fork instructions"
	var logs bytes.Buffer
	initial.Probe.Process.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if r.Method != http.MethodPost || r.URL.Path != apiproxy.Prefix+"/chat/completions" || json.NewDecoder(io.LimitReader(r.Body, maxHTTPBody)).Decode(&body) != nil || !scalar(body["model"], initial.Settings.Model) {
			t.Error("fork provider escaped original route/model")
			w.WriteHeader(403)
			return
		}
		n := calls.Add(1)
		if n > 3 {
			t.Error("fork preparation replayed inference")
			w.WriteHeader(403)
			return
		}
		if n == 3 {
			for _, marker := range []string{"First original text before native fork.", "Second original text before native fork.", initial.Instructions} {
				if !bytes.Contains(body["messages"], []byte(marker)) {
					t.Error("native child lost immutable original conversation/instructions")
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, delta := range []any{map[string]any{"role": "assistant", "content": fmt.Sprintf("Original ordinary answer %d", n)}, map[string]any{}} {
			finish := any(nil)
			if len(delta.(map[string]any)) == 0 {
				finish = "stop"
			}
			chunk := map[string]any{"id": fmt.Sprintf("chat_fork_%d", n), "object": "chat.completion.chunk", "model": initial.Settings.Model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 4, "total_tokens": 24}}
			raw, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()
	initial.ServerOrigin = provider.URL
	initial.Claim = func(_ context.Context, c SessionClaim) error { return c.Validate() }
	fresh := func(workspace string) APIExecutionConfig {
		c := fixtureOwnedAPIConfig(t)
		c.Probe.Process.Executable, c.Probe.Process.Logger = binary, initial.Probe.Process.Logger
		c.Workspace, c.NativeRoot, c.Settings, c.ServerOrigin, c.ContextLimit, c.OutputLimit, c.Instructions = workspace, "/", initial.Settings, initial.ServerOrigin, initial.ContextLimit, initial.OutputLimit, initial.Instructions
		nonce, err := security.RandomToken()
		if err != nil {
			t.Fatal(err)
		}
		c.Token = apiproxy.TokenPrefix + nonce
		c.Claim = initial.Claim
		return c
	}
	settle := func(api *OwnedAPI) {
		t.Helper()
		for i := 0; i < 1024; i++ {
			if _, err := api.Next(ctx); err != nil {
				t.Fatal(err, logs.String())
			}
			p, err := api.Progress(ctx)
			if err != nil || p.NeedsRecovery {
				t.Fatal("native fork input uncertain", err, logs.String())
			}
			if p.SettledObserved {
				return
			}
		}
		t.Fatal("native fork input did not settle")
	}
	first, err := OpenOwnedAPI(ctx, initial)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	defer first.Close(context.Background())
	if _, err := first.CreateSession(ctx, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	if _, err := first.StartText(ctx, domain.NewID(), "First original text before native fork."); err != nil {
		t.Fatal(err)
	}
	settle(first)
	if _, err := first.CloseCompleted(ctx); err != nil {
		t.Fatal(err, logs.String())
	}
	firstRaw, firstRef, err := first.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secondConfig := fresh(initial.Workspace)
	second, err := OpenResumedAPI(ctx, secondConfig, filepath.Dir(initial.Probe.Home), firstRaw, firstRef, domain.NewID(), BuildAgent, false)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	defer second.Close(context.Background())
	if _, err := second.StartText(ctx, domain.NewID(), "Second original text before native fork."); err != nil {
		t.Fatal(err)
	}
	settle(second)
	if _, err := second.CloseCompleted(ctx); err != nil {
		t.Fatal(err, logs.String())
	}
	sourceRaw, sourceRef, err := second.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sourceHome := filepath.Dir(secondConfig.Probe.Home)
	target := filepath.Join(filepath.Dir(initial.Workspace), "independent-native-fork")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".hidden-private-file", "executable-private-file"} {
		mode := os.FileMode(0600)
		if strings.HasPrefix(name, "executable") {
			mode = 0700
		}
		for _, directory := range []string{initial.Workspace, target} {
			if err := os.WriteFile(filepath.Join(directory, name), []byte("Original independent bytes"), mode); err != nil {
				t.Fatal(err)
			}
		}
	}
	prep := fresh(initial.Workspace)
	claimed := map[SessionMutation]bool{}
	prep.Claim = func(_ context.Context, c SessionClaim) error {
		if c.Validate() != nil || claimed[c.Kind] {
			return sessionConflict()
		}
		claimed[c.Kind] = true
		return nil
	}
	requests := ForkRequests{domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()}
	var raw []byte
	var ref CheckpointReference
	if lost {
		api, openErr := OpenResumedAPI(ctx, prep, sourceHome, sourceRaw, sourceRef, requests.Restore, BuildAgent, false)
		if openErr != nil {
			t.Fatal(openErr, logs.String())
		}
		defer api.Close(context.Background())
		loss := &lostForkResponses{RoundTripper: api.session.client.Transport, attempts: map[string]int{}}
		api.session.client.Transport = loss
		source, decodeErr := decodeCheckpoint(sourceRaw, sourceRef, sourceHome)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		raw, ref, err = prepareOwnedForkAPI(ctx, prep, api, sourceHome, sourceRaw, sourceRef, source, target, requests)
		if err == nil && (len(loss.attempts) != 4 || loss.lost != 4) {
			t.Fatal("did not reconcile all original lost acknowledgments")
		}
		for _, count := range loss.attempts {
			if count != 1 {
				t.Fatal("repeated uncertain native mutation")
			}
		}
	} else {
		raw, ref, err = PrepareForkAPI(ctx, prep, sourceHome, sourceRaw, sourceRef, target, requests)
	}
	if err != nil {
		t.Fatal(err, logs.String())
	}
	if calls.Load() != 2 || len(claimed) != 5 || ref.SessionID == sourceRef.SessionID || ref.InputID == sourceRef.InputID || InspectCheckpoint(ctx, sourceHome, sourceRaw, sourceRef) != nil {
		t.Fatal("native fork inferred, repeated or mutated source", logs.String())
	}
	forkHome := filepath.Dir(prep.Probe.Home)
	cp, err := decodeCheckpoint(raw, ref, forkHome)
	if err != nil || cp.Fork == nil || !cp.Fork.SelectionPending || len(cp.Fork.Identities) != 4 || len(cp.Previous) != 1 {
		t.Fatal("native fork seed lacks complete explicit preparation state", err, logs.String())
	}
	if err := os.WriteFile(filepath.Join(target, ".hidden-private-file"), []byte("Independent child bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(initial.Workspace, ".hidden-private-file"))
	if err != nil || string(original) != "Original independent bytes" {
		t.Fatal("fork file ownership changed source")
	}
	nextConfig := fresh(target)
	next, err := OpenResumedAPI(ctx, nextConfig, forkHome, raw, ref, domain.NewID(), BuildAgent, false)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	defer next.Close(context.Background())
	if _, err := next.StartText(ctx, domain.NewID(), "One explicit new child input."); err != nil {
		t.Fatal(err, logs.String())
	}
	settle(next)
	if _, err := next.CloseCompleted(ctx); err != nil {
		t.Fatal(err, logs.String())
	}
	nextRaw, nextRef, err := next.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	final, err := decodeCheckpoint(nextRaw, nextRef, filepath.Dir(nextConfig.Probe.Home))
	if err != nil || final.Fork == nil || final.Fork.SelectionPending || len(final.Previous) != 2 || calls.Load() != 3 || nextRef.SessionID != ref.SessionID {
		t.Fatal("child new input lost verified inherited native history", err, logs.String())
	}
	for _, private := range []string{initial.Token, prep.Token, nextConfig.Token, initial.Workspace, target, initial.Instructions, "First original text"} {
		if strings.Contains(logs.String(), private) {
			t.Fatal("native fork logged private content")
		}
	}
}

// Lose each original response only after the real pinned native operation has
// committed. The controller must reconcile independent state without resend.
type lostForkResponses struct {
	http.RoundTripper
	attempts map[string]int
	lost     int
}

func (l *lostForkResponses) RoundTrip(r *http.Request) (*http.Response, error) {
	mutation := r.Method == http.MethodPatch || r.Method == http.MethodDelete || r.Method == http.MethodPost && (strings.HasSuffix(r.URL.Path, "/fork") || r.URL.Path == "/experimental/control-plane/move-session")
	if mutation {
		l.attempts[r.Method+" "+r.URL.Path]++
	}
	response, err := l.RoundTripper.RoundTrip(r)
	if err == nil && mutation {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		l.lost++
		return nil, io.ErrUnexpectedEOF
	}
	return response, err
}
