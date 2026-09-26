package opencode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func fixtureOwnedAPIConfig(t *testing.T) apiSessionConfig {
	t.Helper()
	probe, _ := fixtureConfig(t, "native-api")
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return apiSessionConfig{
		Probe: probe, Workspace: workspace, NativeRoot: filepath.VolumeName(workspace) + string(filepath.Separator),
		ServerOrigin: "http://127.0.0.1:1", Token: apiproxy.TokenPrefix + base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
		Settings: fixtureSettings(), ContextLimit: 32000, OutputLimit: 1000, Rejection: StopOnInteractionRejection,
		Claim: func(context.Context, SessionClaim) error {
			t.Error("initialization cannot claim native work")
			return sessionConflict()
		},
	}
}

func TestManualNativeOpenCodeOwnedAPIInitialization(t *testing.T) {
	nativeOwnedAPISession(t, false, false)
}

func TestManualNativeOpenCodeOwnedAPIInput(t *testing.T) {
	nativeOwnedAPISession(t, true, false)
}

func TestManualNativeOpenCodeOwnedAPIContextMismatch(t *testing.T) {
	nativeOwnedAPISession(t, false, true)
}

func nativeOwnedAPISession(t *testing.T, input, mismatch bool) {
	t.Helper()
	nativeOwnedAPISessionWithRelay(t, input, mismatch, nativeDirectScriptedAPI)
}

type nativeRelayFixtureMode uint8

const (
	nativeDirectScriptedAPI nativeRelayFixtureMode = iota
	nativeServerRelay
	nativeServerRelayRejectedCredential
	nativeServerRelayPlanEditDenied
	nativeServerRelayLostCreation
)

func TestManualNativeOpenCodeOwnedAPIProxy(t *testing.T) {
	nativeOwnedAPISessionWithRelay(t, true, false, nativeServerRelay)
}

func TestManualNativeOpenCodeOwnedAPIProxyRejectedCredential(t *testing.T) {
	nativeOwnedAPISessionWithRelay(t, true, false, nativeServerRelayRejectedCredential)
}

func TestManualNativeOpenCodeOwnedAPILostCreation(t *testing.T) {
	nativeOwnedAPISessionWithRelay(t, true, false, nativeServerRelayLostCreation)
}

type lostCreationResponse struct {
	http.RoundTripper
	posts atomic.Int32
}

func (l *lostCreationResponse) RoundTrip(request *http.Request) (*http.Response, error) {
	creation := request.Method == http.MethodPost && request.URL.Path == "/session"
	if creation {
		l.posts.Add(1)
	}
	response, err := l.RoundTripper.RoundTrip(request)
	if err == nil && creation {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return nil, errors.New("fixture lost native creation response")
	}
	return response, err
}

func TestManualNativeOpenCodeOwnedAPIUnknownLimits(t *testing.T) {
	nativeOwnedAPISessionWithProfile(t, true, false, nativeServerRelay, func(c *apiSessionConfig) {
		c.ContextLimit, c.OutputLimit = 0, 0
	})
}

func TestManualNativeOpenCodeOwnedAPIDefaultPermissions(t *testing.T) {
	nativeOwnedAPISessionWithProfile(t, true, false, nativeServerRelay, func(c *apiSessionConfig) {
		c.Settings.Permission = []PermissionRule{}
	})
}

func TestManualNativeOpenCodeOwnedAPIAdditiveInstructions(t *testing.T) {
	nativeOwnedAPISessionWithProfile(t, true, false, nativeServerRelay, func(c *apiSessionConfig) {
		c.Instructions = "Private first template 지침.\nPrivate second template: preserve order."
	})
}

func TestManualNativeOpenCodeOwnedAPIProjectInstructions(t *testing.T) {
	for _, selection := range []string{"agents", "fallback", "empty-agents"} {
		t.Run(selection, func(t *testing.T) {
			var paths, contents []string
			const ignoredContext = "Private fallback must remain absent when AGENTS exists."
			const ignoredClaude = "Private disabled Claude compatibility instructions."
			configure := func(c *apiSessionConfig) {
				root, directory := projectInstructionFixture(t)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "git", "init", "--quiet", root)
				cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
				if err := cmd.Run(); err != nil {
					t.Fatal("cannot initialize isolated native instruction repository")
				}
				c.NativeRoot, c.Workspace, c.Instructions = root, directory, privateInstructionsFixture
				filename := "AGENTS.md"
				if selection == "fallback" {
					filename = "CONTEXT.md"
				}
				for index, dir := range []string{directory, root} {
					path := filepath.Join(dir, filename)
					text := []string{"Private nested project 지침.\r\n", "Private repository root instruction.\n"}[index]
					if selection == "empty-agents" {
						text = ""
					}
					writeProjectInstruction(t, path, text)
					paths, contents = append(paths, path), append(contents, text)
					writeProjectInstruction(t, filepath.Join(dir, "CLAUDE.md"), ignoredClaude)
					if filename != "CONTEXT.md" {
						writeProjectInstruction(t, filepath.Join(dir, "CONTEXT.md"), ignoredContext)
					}
				}
				// Loading repository configuration would change the verified
				// model and fail before inference; only selected instructions load.
				writeProjectInstruction(t, filepath.Join(root, "opencode.json"), `{"model":"foreign/ignored"}`)
			}
			check := func(body map[string]json.RawMessage) {
				var messages []map[string]json.RawMessage
				if json.Unmarshal(body["messages"], &messages) != nil {
					t.Error("native provider messages unavailable")
					return
				}
				var system strings.Builder
				for _, message := range messages {
					if scalar(message["role"], "system") {
						var content string
						if json.Unmarshal(message["content"], &content) != nil {
							t.Error("native system message is not text")
						}
						system.WriteString(content)
					}
				}
				value, prior := system.String(), -1
				for index, text := range contents {
					if text == "" {
						if strings.Contains(value, "Instructions from: "+paths[index]) {
							t.Error("native empty project instruction acquired content")
						}
						continue
					}
					original := "Instructions from: " + paths[index] + "\n" + text
					position := strings.Index(value, original)
					if strings.Count(value, original) != 1 || position <= prior {
						t.Error("native project instruction bytes, source or order changed")
					}
					prior = position
				}
				if strings.Contains(value, ignoredContext) || strings.Contains(value, ignoredClaude) || strings.Index(value, privateInstructionsFixture) <= prior {
					t.Error("native instruction precedence or additive template order changed")
				}
			}
			nativeOwnedAPISessionWithRequestCheck(t, true, false, nativeServerRelay, configure, check)
		})
	}
}

func TestManualNativeOpenCodeOwnedAPIPlan(t *testing.T) {
	nativeOwnedAPISessionWithProfile(t, true, false, nativeServerRelay, func(c *apiSessionConfig) {
		c.Settings.Agent = PlanAgent
		c.Settings.Permission = []PermissionRule{}
		c.Instructions = privateInstructionsFixture
	})
}

func TestManualNativeOpenCodeOwnedAPIPlanDeniesOrdinaryWrite(t *testing.T) {
	nativeOwnedAPISessionWithProfile(t, true, false, nativeServerRelayPlanEditDenied, func(c *apiSessionConfig) {
		c.Settings.Agent = PlanAgent
		c.Settings.Permission = []PermissionRule{}
	})
}

// Only the lease authority is a fixture here. Native OpenCode and the actual
// server relay handler perform their real HTTP/auth/body/stream boundaries.
type nativeAPIProxyAuthority struct {
	token, key string
	scope      apiproxy.Scope
	acquired   atomic.Int32
	keys       atomic.Int32
	released   atomic.Int32
}

func (a *nativeAPIProxyAuthority) Acquire(ctx context.Context, token string) (*apiproxy.Lease, error) {
	if token != a.token {
		return nil, domain.Fail(domain.Unauthenticated, "Unknown fixture execution authority.", "")
	}
	a.acquired.Add(1)
	return &apiproxy.Lease{Scope: a.scope, Context: ctx, Release: func() { a.released.Add(1) }, Key: func(ctx context.Context) ([]byte, error) {
		a.keys.Add(1)
		return []byte(a.key), ctx.Err()
	}}, nil
}

func nativeOwnedAPISessionWithRelay(t *testing.T, input, mismatch bool, relayMode nativeRelayFixtureMode) {
	t.Helper()
	nativeOwnedAPISessionWithProfile(t, input, mismatch, relayMode, nil)
}

func nativeOwnedAPISessionWithProfile(t *testing.T, input, mismatch bool, relayMode nativeRelayFixtureMode, configure func(*apiSessionConfig)) {
	t.Helper()
	nativeOwnedAPISessionWithRequestCheck(t, input, mismatch, relayMode, configure, nil)
}

func nativeOwnedAPISessionWithRequestCheck(t *testing.T, input, mismatch bool, relayMode nativeRelayFixtureMode, configure func(*apiSessionConfig), check func(map[string]json.RawMessage)) {
	t.Helper()
	realRelay := relayMode != nativeDirectScriptedAPI
	executable := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit isolated native OpenCode owned API initializer")
	}
	requireNoManagedOpenCodeConfig(t)
	config := fixtureOwnedAPIConfig(t)
	if configure != nil {
		configure(&config)
	}
	var logs bytes.Buffer
	config.Probe.Process.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	defer func() {
		if t.Failed() {
			t.Logf("owned native phase diagnostics: %s", logs.String())
		}
	}()
	config.Probe.Process.Executable = executable
	upstreamKey := "private-upstream-only-" + string(domain.NewID())
	t.Cleanup(func() {
		// All original native/HTTP cleanup defers have joined before Cleanup.
		if strings.Contains(logs.String(), upstreamKey) || strings.Contains(logs.String(), config.Token) {
			t.Error("native/relay diagnostics disclosed protected credentials")
		}
	})
	var requests atomic.Int32
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		number := requests.Add(1)
		var body map[string]json.RawMessage
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBody+1))
		path, key := apiproxy.Prefix+"/chat/completions", config.Token
		if realRelay {
			path, key = "/provider/chat/completions", upstreamKey
			if r.Header.Get("HTTP-Referer") != "https://deli.dev" || strings.Contains(string(raw), config.Token) || strings.Contains(string(raw), upstreamKey) {
				t.Error("actual relay leaked credentials or changed the required referer")
			}
		}
		if !input || err != nil || domain.Decode(raw, &body) != nil || r.Method != http.MethodPost || r.URL.Path != path || r.Header.Get("Authorization") != "Bearer "+key || !scalar(body["model"], config.Settings.Model) || string(body["stream"]) != "true" {
			t.Error("owned native request escaped the fixed scripted relay scope")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if check != nil {
			check(body)
		}
		if config.Instructions != "" {
			var messages []map[string]json.RawMessage
			if json.Unmarshal(body["messages"], &messages) != nil {
				t.Error("native provider messages are unavailable")
			}
			var system strings.Builder
			for _, message := range messages {
				if !scalar(message["role"], "system") {
					continue
				}
				var content string
				if json.Unmarshal(message["content"], &content) != nil {
					t.Error("native system message is not text")
				}
				system.WriteString(content)
			}
			if strings.Count(system.String(), config.Instructions) != 1 || !strings.Contains(system.String(), "You are opencode, an interactive CLI tool") || !strings.Contains(system.String(), "Working directory: "+config.Workspace) {
				t.Error("additive instructions replaced native base/environment or lost original template bytes/order")
			}
		}
		if relayMode == nativeServerRelayPlanEditDenied {
			if number > 2 {
				t.Error("native Plan fixture repeated provider work")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if number == 1 {
				arguments, _ := json.Marshal(map[string]any{"filePath": filepath.Join(config.Workspace, "forbidden.txt"), "content": "private refused write"})
				w.Header().Set("Content-Type", "text/event-stream")
				for _, chunk := range []map[string]any{
					{"id": "chatcmpl-private", "object": "chat.completion.chunk", "created": 1, "model": "private-model", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_private_plan_write", "type": "function", "function": map[string]any{"name": "write", "arguments": string(arguments)}}}}, "finish_reason": nil}}},
					{"id": "chatcmpl-private", "object": "chat.completion.chunk", "created": 1, "model": "private-model", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}},
				} {
					encoded, _ := json.Marshal(chunk)
					_, _ = io.WriteString(w, "data: "+string(encoded)+"\n\n")
				}
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
				return
			}
			if result, ok := providerToolResult(body["messages"], "call_private_plan_write"); !ok || !strings.Contains(result, "The user has specified a rule which prevents you") {
				t.Error("native Plan continuation omitted its original write error")
			}
		}
		if relayMode == nativeServerRelayRejectedCredential {
			// The real relay must not forward this deliberately reflected
			// upstream key in the provider's diagnostic body or headers.
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Request-Id", upstreamKey)
			w.Header().Set("Set-Cookie", upstreamKey)
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": upstreamKey, "type": "invalid_api_key"}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Private owned fixture response"},"finish_reason":null}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer relay.Close()
	config.ServerOrigin = relay.URL
	var authority *nativeAPIProxyAuthority
	if realRelay {
		authority = &nativeAPIProxyAuthority{token: config.Token, key: upstreamKey, scope: apiproxy.Scope{
			ExecutionID: domain.NewID(), SessionID: domain.NewID(), AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(),
			NativeModel: config.Settings.Model, Provider: domain.Provider{Name: "Private native relay fixture", Endpoint: relay.URL + "/provider", Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth}, Operations: []apiproxy.Operation{apiproxy.ChatCompletion},
		}}
		proxy := httptest.NewServer(apiproxy.New(authority, config.Probe.Process.Logger))
		defer proxy.Close()
		config.ServerOrigin = proxy.URL
	}
	var claims []SessionClaim
	config.Claim = func(_ context.Context, claim SessionClaim) error {
		if err := claim.Validate(); err != nil {
			return err
		}
		claims = append(claims, claim)
		raw, _ := json.Marshal(claims)
		return security.WriteAtomic(filepath.Join(filepath.Dir(config.Probe.Home), "claims.json"), raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if mismatch {
		config.NativeRoot = filepath.Dir(config.Workspace)
	}
	api, err := openAPISession(ctx, config)
	if mismatch {
		if err == nil || api != nil || domain.SafeError(err).Code != domain.Unsupported || len(claims) != 0 || requests.Load() != 0 {
			t.Fatal("foreign native project root gained execution authority")
		}
		if !strings.Contains(logs.String(), `"phase":"/path"`) {
			t.Fatal("mismatch test did not reach native path validation")
		}
		if err := process.ReconcileOwner(config.Probe.Process.Directory, config.Probe.Process.OwnerID); err != nil {
			t.Fatal("failed owned initialization did not complete native cleanup")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := api.closeOwned(cleanup); err != nil {
			t.Error(err)
		}
		if err := process.ReconcileOwner(config.Probe.Process.Directory, config.Probe.Process.OwnerID); err != nil {
			t.Error(err)
		}
	}()
	if !api.apiVerified || api.runtimeRoot != config.NativeRoot || api.runtimeRead || len(claims) != 0 || api.alive() != nil {
		t.Fatal("owned native initialization did not preserve its exact live context")
	}
	observed, observationErr := api.initialObservedSettings(ctx)
	if len(config.Settings.Permission) == 0 {
		mode := domain.ExecuteMode
		if config.Settings.Agent == PlanAgent {
			mode = domain.PlanMode
		}
		original := domain.ExecutionConfiguration{Harness: domain.OpenCode, NativeModel: config.Settings.Model, Options: domain.AgentOptions{Permission: domain.PermissionDefault}}
		if observationErr != nil || observed.OpenCodeAgent != config.Settings.Agent || observed.ValidateForInput(original, mode) != nil {
			t.Fatal("verified native initialization lost its exact primary-agent observation")
		}
	} else if observationErr == nil {
		t.Fatal("custom native rules were misreported as native defaults")
	}
	if relayMode == nativeServerRelayLostCreation {
		lost := &lostCreationResponse{RoundTripper: api.client.Transport}
		api.client.Transport = lost
		request := domain.NewID()
		if id, err := api.create(ctx, request, config.Settings); err == nil || domain.SafeError(err).Code != domain.RecoveryRequired || id != "" {
			t.Fatal("native creation response was not lost")
		}
		owned := &OwnedAPI{session: api, reading: make(chan struct{}, 1)}
		receipt, err := owned.InspectSession(ctx)
		if err != nil || !receipt.Recorded || receipt.HTTPAccepted || receipt.RequestID != request || !nativeID(receipt.SessionID, "ses") || lost.posts.Load() != 1 {
			t.Fatalf("original native creation reconciliation failed: %v", err)
		}
		if _, err := api.create(ctx, domain.NewID(), config.Settings); err == nil || lost.posts.Load() != 1 {
			t.Fatal("reconciliation repeated actual native creation")
		}
	} else {
		if _, err := api.create(ctx, domain.NewID(), config.Settings); err != nil {
			t.Fatal(err)
		}
	}
	if len(claims) != 1 || claims[0].Kind != CreateSessionMutation || requests.Load() != 0 {
		t.Fatal("owned initialization or creation performed unrequested inference")
	}
	if !strings.HasSuffix(api.apiProfile.BaseURL, apiproxy.Prefix) {
		t.Fatal("owned API origin did not use the fixed relay path")
	}
	if !input {
		return
	}
	stream, err := api.openEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := api.submitText(ctx, domain.NewID(), "Reply using the private owned fixture response."); err != nil {
		t.Fatal(err)
	}
	if _, err := api.observeInput(ctx, config.Workspace); err == nil {
		t.Fatal("unverified project root replaced initializer evidence")
	}
	observer, err := api.observeInput(ctx, config.NativeRoot)
	if err != nil {
		t.Fatal(err)
	}
	for count := 0; count < 256 && !observer.snapshot().SettledObserved; count++ {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := observer.observe(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	progress := observer.snapshot()
	expectedRequests := int32(1)
	if relayMode == nativeServerRelayPlanEditDenied {
		expectedRequests = 2
		if _, err := os.Lstat(filepath.Join(config.Workspace, "forbidden.txt")); !os.IsNotExist(err) {
			t.Fatal("native Plan changed an ordinary workspace file")
		}
		found := false
		for _, part := range observer.parts {
			if tool := part.value.Tool; tool != nil && tool.CallID == "call_private_plan_write" {
				if found || tool.State != ToolError || tool.Name != "write" || tool.Error == nil || !strings.Contains(*tool.Error, "The user has specified a rule which prevents you") {
					t.Fatal("native Plan refusal lost its original failed tool identity")
				}
				var original map[string]json.RawMessage
				if domain.Decode(tool.Input, &original) != nil || !scalar(original["filePath"], filepath.Join(config.Workspace, "forbidden.txt")) || !scalar(original["content"], "private refused write") {
					t.Fatal("native Plan refusal lost the exact original write input")
				}
				found = true
			}
		}
		if !found {
			t.Fatal("native Plan did not retain its original write failure")
		}
	}
	if !progress.SettledObserved || !progress.UserSeen || !progress.InputPartSeen || requests.Load() != expectedRequests || len(claims) != 2 {
		t.Fatal("owned native input did not settle with exact original claims")
	}
	if receipt, err := api.inspectInput(ctx); err != nil || !receipt.Recorded || receipt.MessageID != claims[1].MessageID || receipt.PartID != claims[1].PartID {
		t.Fatal("fresh native IDs did not retain the exact originally claimed stored input")
	}
	ownedHistory := &OwnedAPI{session: api, reading: make(chan struct{}, 1)}
	history, err := ownedHistory.InspectHistory(ctx)
	if err != nil || history.RequestID != claims[1].RequestID || history.SessionID != claims[1].SessionID || history.InputID != claims[1].MessageID || history.AssistantID != progress.AssistantID || len(history.Messages) != len(observer.messages) || len(history.Digest) != 64 {
		t.Fatalf("original stored native history did not match its live observation: %v", err)
	}
	encodedHistory, _ := json.Marshal(history)
	for _, private := range []string{config.Workspace, config.Token, upstreamKey, "Reply using the private owned fixture response.", "Private owned fixture response", config.Instructions} {
		if private != "" && bytes.Contains(encodedHistory, []byte(private)) {
			t.Fatal("native history comparison evidence disclosed private payloads")
		}
	}
	assistant := observer.messages[progress.AssistantID]
	if assistant == nil || assistant.value.Assistant == nil || assistant.value.Assistant.Completed == nil {
		t.Fatal("owned input did not retain its final native assistant")
	}
	if relayMode == nativeServerRelayRejectedCredential {
		problem := assistant.value.Assistant.Error
		if problem == nil || problem.Kind != APIErrorKind || problem.StatusCode == nil || *problem.StatusCode != http.StatusUnauthorized || problem.Retryable == nil || *problem.Retryable {
			t.Fatal("upstream credential rejection lost its native error classification")
		}
	} else if assistant.value.Assistant.Error != nil || assistant.value.Assistant.Finish == nil || *assistant.value.Assistant.Finish != FinishStop {
		t.Fatal("owned native scripted success became a settled error")
	}
	if realRelay {
		if authority.acquired.Load() != expectedRequests || authority.keys.Load() != expectedRequests || authority.released.Load() != expectedRequests {
			t.Fatal("actual relay did not preserve one scoped request/key/release lifecycle")
		}
		for _, message := range observer.messages {
			if bytes.Contains(message.raw, []byte(upstreamKey)) {
				t.Fatal("server-only upstream key reached native retained message evidence")
			}
		}
		for _, part := range observer.parts {
			if bytes.Contains(part.raw, []byte(upstreamKey)) || strings.Contains(part.text, upstreamKey) {
				t.Fatal("server-only upstream key reached native retained part evidence")
			}
		}
	}
}
