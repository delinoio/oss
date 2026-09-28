package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestManualNativeOpenCodeOriginalClosedCheckpoint(t *testing.T) {
	executable := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit isolated pinned OpenCode checkpoint fixture")
	}
	for _, mode := range []string{"build", "plan", "failed", "stopped"} {
		t.Run(mode, func(t *testing.T) { nativeClosedCheckpoint(t, executable, mode) })
	}
}

func nativeClosedCheckpoint(t *testing.T, executable, mode string, project ...nativeCheckpointWorkspace) {
	t.Helper()
	requireNoManagedOpenCodeConfig(t)
	config := fixtureOwnedAPIConfig(t)
	config.Settings.Agent, config.Settings.Permission = BuildAgent, []PermissionRule{}
	if mode == "plan" {
		config.Settings.Agent = PlanAgent
	}
	config.Probe.Process.Executable = executable
	config.Instructions = "Private checkpoint additive instruction."
	if len(project) == 1 && project[0] != checkpointGeneralChat {
		prepareNativeCheckpointGit(t, &config, project[0])
	}
	var logs bytes.Buffer
	config.Probe.Process.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	const prompt = "Private original checkpoint input."
	const answer = "Private original checkpoint answer."
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBody+1))
		var body map[string]json.RawMessage
		if err != nil || domain.Decode(raw, &body) != nil || r.Method != http.MethodPost || r.URL.Path != apiproxy.Prefix+"/chat/completions" || r.Header.Get("Authorization") != "Bearer "+config.Token || !scalar(body["model"], config.Settings.Model) || string(body["stream"]) != "true" || requests.Add(1) != 1 {
			t.Error("checkpoint fixture escaped its single original native input")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if mode == "stopped" || mode == "failed" {
			w.Header().Set("Content-Type", "application/json")
			status := http.StatusUnauthorized
			errorType := "invalid_api_key"
			if mode == "stopped" {
				w.Header().Set("Retry-After", "60")
				status = http.StatusServiceUnavailable
				errorType = "server_error"
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "Private fixture failure", "type": errorType}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{"role":"assistant","content":"`+answer+`"},"finish_reason":null}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	config.ServerOrigin = provider.URL
	var claims []SessionClaim
	config.Claim = func(_ context.Context, claim SessionClaim) error {
		if err := claim.Validate(); err != nil {
			return err
		}
		claims = append(claims, claim)
		raw, _ := json.Marshal(claims)
		return security.WriteAtomic(filepath.Join(filepath.Dir(filepath.Dir(config.Probe.Home)), "claims.json"), raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	api, err := OpenOwnedAPI(ctx, config)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := api.Close(cleanup); err != nil {
			t.Error(err)
		}
	}()
	if _, err := api.CreateSession(ctx, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	if _, err := api.StartText(ctx, domain.NewID(), prompt); err != nil {
		t.Fatal(err)
	}
	if raw, _, err := api.RetainCheckpoint(ctx); err == nil || len(raw) != 0 || api.checkpointAttempted {
		t.Fatal("live original execution gained closed checkpoint authority")
	}
	for count := 0; count < 512; count++ {
		observation, err := api.Next(ctx)
		if err != nil {
			t.Fatal(err, logs.String())
		}
		if observation.Retry != nil && mode == "stopped" {
			if _, err := api.Interrupt(ctx, domain.NewID()); err != nil {
				t.Fatal(err)
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
	var history HistoryObservation
	if mode == "stopped" {
		proof, closed := api.CloseAfterStop(ctx)
		history, err = proof.History, closed
	} else {
		history, err = api.CloseCompleted(ctx)
	}
	if err != nil {
		t.Fatal(err, logs.String())
	}
	raw, ref, err := api.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatal("original closed native checkpoint unavailable", err, logs.String())
	}
	home := filepath.Dir(config.Probe.Home)
	wantClaims := 2
	if mode == "stopped" {
		wantClaims++
	}
	if ref.RequiresResume != (mode == "stopped" || mode == "failed") || ref.HistorySHA256 != history.Digest || ref.OwnerID != config.Probe.Process.OwnerID || InspectCheckpoint(ctx, home, raw, ref) != nil || len(claims) != wantClaims || requests.Load() != 1 {
		t.Fatal("checkpoint lost original ownership, history or files")
	}
	for _, secret := range []string{prompt, answer, config.Token, config.Instructions} {
		if bytes.Contains(raw, []byte(secret)) || bytes.Contains(logs.Bytes(), []byte(secret)) {
			t.Fatal("checkpoint or logs retained original private content")
		}
	}
	if len(project) == 1 && project[0] != checkpointGeneralChat {
		value, err := decodeCheckpoint(raw, ref, home)
		if err != nil {
			t.Fatal(err)
		}
		inspectNativeProjectSnapshot(t, config, value, project[0])
	}
	original := bytes.Clone(raw)
	raw[0] = '['
	again, same, err := api.RetainCheckpoint(ctx)
	if err != nil || same != ref || !bytes.Equal(again, original) {
		t.Fatal("returned bytes mutated original checkpoint retention")
	}
	if err := os.WriteFile(filepath.Join(home, "unexpected"), []byte("changed original runtime"), 0600); err != nil {
		t.Fatal(err)
	}
	if InspectCheckpoint(ctx, home, original, ref) == nil {
		t.Fatal("changed native runtime retained checkpoint validity")
	}
	if _, _, err := api.RetainCheckpoint(ctx); err == nil {
		t.Fatal("changed native runtime was adopted as a new checkpoint")
	}
}
