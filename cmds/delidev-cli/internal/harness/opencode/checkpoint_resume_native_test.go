package opencode

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestManualNativeOpenCodeCheckpointProcessReplacement(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit private pinned OpenCode process replacement fixture")
	}
	for _, mode := range []string{"build", "plan", "failed", "stopped", "build-plan-build", "plan-build-plan"} {
		t.Run(mode, func(t *testing.T) { nativeCheckpointReplacement(t, binary, mode) })
	}
}

func nativeCheckpointReplacement(t *testing.T, binary string, mode string, project ...nativeCheckpointWorkspace) {
	t.Helper()
	requireNoManagedOpenCodeConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	agentAt := func(turn int) PrimaryAgent {
		if mode == "plan" || mode == "build-plan-build" && turn%2 == 1 || mode == "plan-build-plan" && turn%2 == 0 {
			return PlanAgent
		}
		return BuildAgent
	}
	failedFirst := mode == "failed" || mode == "stopped"
	const turns = 3
	var requests atomic.Int32
	tokens := make([]string, turns)
	for index := range tokens {
		tokens[index] = apiproxy.TokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(index)}, 32))
	}
	prompt := func(index int) string { return fmt.Sprintf("Private original input %d.", index) }
	answer := func(index int) string { return fmt.Sprintf("Private original response %d.", index) }
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := int(requests.Add(1)) - 1
		var body struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if index >= turns || json.NewDecoder(io.LimitReader(r.Body, maxHTTPBody)).Decode(&body) != nil || body.Model != fixtureSettings().Model || !body.Stream || r.URL.Path != apiproxy.Prefix+"/chat/completions" || r.Header.Get("Authorization") != "Bearer "+tokens[index] {
			t.Error("replacement changed original provider/model/credential authority")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		type providerMessage struct{ role, text string }
		var expected []providerMessage
		for input := 0; input <= index; input++ {
			expected = append(expected, providerMessage{"user", prompt(input)})
			if input < index && !(input == 0 && failedFirst) {
				expected = append(expected, providerMessage{"assistant", answer(input)})
			}
		}
		seen := 0
		for _, message := range body.Messages {
			if message.Role == "system" {
				continue
			}
			if seen >= len(expected) {
				t.Error("replacement repeated original conversation")
				break
			}
			role, text := expected[seen].role, expected[seen].text
			var content string
			decoded := json.Unmarshal(message.Content, &content) == nil
			matches := content == text
			reminder := ""
			if agentAt(index) == PlanAgent {
				reminder = "455db97e0d21e8097c2afb539d167b4b2483e99b585dbc4fff23cafd4a3029b8"
			} else {
				for turn := 0; turn < index; turn++ {
					if agentAt(turn) == PlanAgent {
						reminder = "5e3db616a685a3dfaaf95fb86ae6e2acfbdf520bda60f7b27f727d2a88ba8a25"
					}
				}
			}
			if reminder != "" && seen == len(expected)-1 {
				// The pinned native SessionReminders.apply appends plan.txt to
				// or the Build transition to the latest provider input only.
				// Pin its exact bytes without
				// importing that upstream prompt into repository-owned code.
				var parts []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				decoded = domain.Decode(message.Content, &parts) == nil
				matches = decoded && len(parts) == 2 && parts[0].Type == "text" && parts[0].Text == text && parts[1].Type == "text" && mutationDigest([]byte(parts[1].Text)) == reminder
			}
			if !decoded || message.Role != role || !matches {
				t.Error("replacement lost, changed or repeated original conversation", index, seen)
			}
			seen++
		}
		if seen != len(expected) {
			t.Error("replacement omitted original conversation", index, seen)
		}
		if index == 0 && failedFirst {
			w.Header().Set("Content-Type", "application/json")
			status, errorType := http.StatusUnauthorized, "invalid_api_key"
			if mode == "stopped" {
				status, errorType = http.StatusServiceUnavailable, "server_error"
				w.Header().Set("Retry-After", "60")
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "Private original fixture error", "type": errorType}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-private-%d\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"private-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":%q},\"finish_reason\":null}]}\n\n", index, answer(index))
		_, _ = fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-private-%d\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"private-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":4,\"total_tokens\":24}}\n\ndata: [DONE]\n\n", index)
	}))
	defer provider.Close()
	var original apiSessionConfig
	var prior []byte
	var ref CheckpointReference
	var home string
	for turn := 0; turn < turns; turn++ {
		config := fixtureOwnedAPIConfig(t)
		config.Probe.Process.Logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
		config.Probe.Process.Executable = binary
		config.ServerOrigin, config.Token = provider.URL, tokens[turn]
		config.Settings.Agent, config.Settings.Permission = agentAt(turn), []PermissionRule{}
		config.Instructions = "Private immutable additive checkpoint instruction."
		if turn > 0 {
			config.Workspace, config.NativeRoot, config.Root = original.Workspace, original.NativeRoot, original.Root
		} else {
			if len(project) == 1 && project[0] != checkpointGeneralChat {
				prepareNativeCheckpointGit(t, &config, project[0])
			}
			if runtime.GOOS == "windows" && config.NativeRoot == filesystemBoundary(config.Workspace) {
				var err error
				config.Root, err = GlobalWorkspaceRoot(config.Workspace)
				if err != nil {
					t.Fatal(err)
				}
				config.NativeRoot = ""
			}
			original = config
		}
		var claims []SessionClaim
		config.Claim = func(_ context.Context, claim SessionClaim) error {
			if err := claim.Validate(); err != nil {
				return err
			}
			for _, old := range claims {
				if old.RequestID == claim.RequestID {
					return sessionConflict()
				}
			}
			claims = append(claims, claim)
			raw, _ := json.Marshal(claims)
			return security.WriteAtomic(filepath.Join(filepath.Dir(filepath.Dir(config.Probe.Home)), "claims.json"), raw)
		}
		var api *OwnedAPI
		var err error
		if turn == 0 {
			api, err = OpenOwnedAPI(ctx, config)
		} else {
			if ref.RequiresResume {
				if api, err := OpenResumedAPI(ctx, config, home, prior, ref, domain.NewID(), agentAt(turn-1), false); err == nil || api != nil || len(claims) != 0 {
					t.Fatal("failed/stopped predecessor gained implicit continuation")
				}
			}
			api, err = OpenResumedAPI(ctx, config, home, prior, ref, domain.NewID(), agentAt(turn-1), ref.RequiresResume)
		}
		if err != nil {
			t.Fatal("original checkpoint replacement unavailable", turn, err)
		}
		defer func(api *OwnedAPI) {
			cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := api.Close(cleanup); err != nil {
				t.Error(err)
			}
		}(api)
		if turn == 0 {
			if _, err := api.CreateSession(ctx, domain.NewID()); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := api.CreateSession(ctx, domain.NewID()); err == nil || len(claims) != 1 || claims[0].Kind != ResumeSessionMutation {
				t.Fatal("replacement recreated the original session")
			}
			if _, err := api.StartText(ctx, ref.InputRequestID, prompt(turn-1)); err == nil || api.session.eventAttempt || len(claims) != 1 {
				t.Fatal("original input replay consumed a new listener or claim")
			}
		}
		if _, err := api.StartText(ctx, domain.NewID(), prompt(turn)); err != nil {
			t.Fatal(err)
		}
		for count := 0; count < 512; count++ {
			observation, err := api.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if turn == 0 && mode == "stopped" && observation.Retry != nil {
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
		if turn == 0 && mode == "stopped" {
			_, err = api.CloseAfterStop(ctx)
		} else {
			_, err = api.CloseCompleted(ctx)
		}
		if err != nil {
			t.Fatal("replacement history or cleanup failed", turn, err)
		}
		if turn > 0 && InspectCheckpoint(ctx, home, prior, ref) != nil {
			t.Fatal("replacement modified original closed files")
		}
		raw, current, err := api.RetainCheckpoint(ctx)
		wantClaims := 2
		if turn == 0 && mode == "stopped" {
			wantClaims++
		}
		if err != nil || len(claims) != wantClaims || current.RequiresResume != (turn == 0 && failedFirst) || turn > 0 && (current.SessionID != ref.SessionID || current.CreationRequestID != ref.CreationRequestID || current.InputID == ref.InputID || current.OwnerID == ref.OwnerID) {
			t.Fatal("replacement lost immutable native lineage", turn, err)
		}
		home = filepath.Dir(config.Probe.Home)
		value, err := decodeCheckpoint(raw, current, home)
		if err != nil || len(value.Previous) != turn || InspectCheckpoint(ctx, home, raw, current) != nil {
			t.Fatal("replacement lost full history checkpoint", turn, err)
		}
		if len(project) == 1 && project[0] == checkpointFirstCommitProject && turn > 0 {
			if !validCheckpointProjectAdoption(value) || value.ProjectAdoption == nil || value.ProjectAdoption.AfterInputs != 1 || value.ProjectAdoption.From != "global" || value.Project == "global" {
				t.Fatal("first commit lost original project adoption evidence")
			}
		} else if value.ProjectAdoption != nil {
			t.Fatal("unchanged native project acquired adoption evidence")
		}
		if len(project) == 1 && project[0] != checkpointGeneralChat {
			if value.Snapshot == nil || !validCheckpointSnapshot(value) {
				t.Fatal("project lost original snapshot proof", turn)
			}
			if turn == 0 {
				inspectNativeProjectSnapshot(t, config, value, project[0])
				content := []byte("Original private snapshot fixture.\n")
				hash := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(content))), content...))
				object := hex.EncodeToString(hash[:])
				if project[0] == checkpointCommittedProject {
					if err := os.Remove(filepath.Join(config.Workspace, ".git", "objects", object[:2], object[2:])); err != nil {
						t.Fatal("remove original fixture object", err)
					}
				}
			}
			if turn > 0 {
				content, err := os.ReadFile(filepath.Join(config.Workspace, "original.txt"))
				if err != nil || string(content) != fmt.Sprintf("Later independent workspace change %d.\n", turn-1) {
					t.Fatal("replacement restored an old workspace snapshot", err)
				}
			}
			if err := os.WriteFile(filepath.Join(config.Workspace, "original.txt"), []byte(fmt.Sprintf("Later independent workspace change %d.\n", turn)), 0600); err != nil {
				t.Fatal(err)
			}

		}
		if turn == 0 && len(project) == 1 && project[0] == checkpointFirstCommitProject {
			command := exec.CommandContext(ctx, "git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "First independent fixture commit")
			command.Dir = config.Workspace
			command.Env = append(append([]string(nil), config.Probe.Process.Env...), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
			if out, err := command.CombinedOutput(); err != nil {
				t.Fatalf("first private commit: %v %s", err, out)
			}
		}
		prior, ref = raw, current
	}
	if requests.Load() != turns {
		t.Fatal("replacement repeated or omitted original input")
	}
}
