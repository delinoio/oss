package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func fixtureResult() map[string]any {
	return map[string]any{
		"commands": []any{}, "agents": []any{map[string]any{"name": "Explore", "description": "Private native descriptor: \u03bb\U0001f600"}},
		"output_style": "default", "available_output_styles": []string{"default"},
		"models":  []any{map[string]any{"value": "fixture", "resolvedModel": "fixture-model", "displayName": "Fixture", "description": "Private local model descriptor", "supportsEffort": true, "supportedEffortLevels": []string{"low", "high"}}},
		"account": map[string]any{"tokenSource": "none", "apiProvider": "firstParty"},
		"pid":     os.Getpid(), "current_permission_mode": "dontAsk", "remote_control_auto_enable": false,
		"remote_control_auto_on_by_default": false, "ide_rc_auto_enable_gate": false,
		"fast_mode_state": "off", "fast_mode_disabled_reason": "sdk_opt_in_required",
	}
}

// fixtureResult287 is the sanitized Claude Code 2.1.287 initialize result: the
// 2.1.236 profile plus bounded private metadata. The empty pending request
// arrays are members of the control-response envelope (fixtureEnvelope287).
func fixtureResult287() map[string]any {
	result := fixtureResult()
	result["analytics_disabled"] = true
	result["capabilities"] = []any{"fixture-capability"}
	result["feedback_mode"] = map[string]any{"kind": "disabled"}
	result["remote_control_available"] = false
	result["remote_control_auto_connect_default"] = false
	result["session_state"] = "idle"
	result["user_output_styles_dir"] = "/private/fixture/output-styles"
	return result
}

// fixtureEnvelope287 holds the 2.1.287 members of the inner control-response
// envelope, next to subtype, request_id and response.
func fixtureEnvelope287() map[string]any {
	return map[string]any{"pending_permission_requests": []any{}, "pending_user_dialog_requests": []any{}}
}

// fixtureResponseWith builds a control response whose inner envelope also
// carries the given members.
func fixtureResponseWith(id domain.ID, result any, envelope map[string]any) []byte {
	inner := map[string]any{"subtype": "success", "request_id": id, "response": result}
	for key, value := range envelope {
		inner[key] = value
	}
	raw, _ := json.Marshal(map[string]any{"type": "control_response", "response": inner})
	return append(raw, '\n')
}

const fixturePrelude = `{"type":"system","subtype":"ui_invalidate","event":"ui.render","uuid":"fixture-uuid","session_id":"fixture-session"}` + "\n"

// fixture287Frames builds the stdout for a "v287*" fixture mode.
func fixture287Frames(mode string, id domain.ID) []byte {
	result := fixtureResult287()
	envelope := fixtureEnvelope287()
	prelude := fixturePrelude
	switch mode {
	case "v287-subtype":
		prelude = `{"type":"system","subtype":"unknown_event"}` + "\n"
	case "v287-field":
		prelude = `{"type":"system","subtype":"ui_invalidate","token":"private"}` + "\n"
	case "v287-event":
		prelude = `{"type":"system","subtype":"ui_invalidate","event":"other"}` + "\n"
	case "v287-malformed":
		prelude = `{"type":"system",` + "\n"
	case "v287-pending":
		envelope["pending_permission_requests"] = []any{map[string]any{"id": "x"}}
	case "v287-dialog":
		envelope["pending_user_dialog_requests"] = []any{map[string]any{"id": "x"}}
	case "v287-result-pending":
		result["pending_permission_requests"] = []any{}
	case "v287-busy":
		result["session_state"] = "running"
	case "v287-remote":
		result["remote_control_auto_connect_default"] = true
	case "v287-feedback":
		result["feedback_mode"] = map[string]any{"kind": "enabled"}
	case "v287-feedback-field":
		result["feedback_mode"] = map[string]any{"kind": "disabled", "extra": true}
	case "v287-account":
		result["account"] = map[string]any{"tokenSource": "oauth", "apiProvider": "firstParty"}
	case "v287-foreign":
		id = domain.NewID()
	}
	response := string(fixtureResponseWith(id, result, envelope))
	switch mode {
	case "v287-twice":
		return []byte(prelude + prelude + response)
	case "v287-late":
		return []byte(response + prelude)
	case "v287-duplicate":
		return []byte(prelude + response + response)
	case "v287-trailing":
		return []byte(prelude + response + "{")
	case "v287-oversize":
		return append([]byte(prelude), bytes.Repeat([]byte{'x'}, maxProbeFrame+1)...)
	case "v287-noprelude":
		return []byte(response)
	}
	return []byte(prelude + response)
}

func fixtureResponse(id domain.ID, result any) []byte {
	raw, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": result}})
	return append(raw, '\n')
}

func init() {
	if len(os.Args) < 2 || os.Args[1] != "--bare" || !strings.HasPrefix(filepath.Base(os.Getenv("HOME")), "fixture-") {
		return
	}
	expected := []string{"--bare", "--print", "--input-format=stream-json", "--output-format=stream-json", "--verbose", "--setting-sources=", "--strict-mcp-config", `--mcp-config={"mcpServers":{}}`, "--no-session-persistence", "--tools=", "--permission-mode=dontAsk", "--no-chrome", "--disable-slash-commands"}
	if !reflect.DeepEqual(os.Args[1:], expected) {
		os.Exit(11)
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "AWS_ACCESS_KEY_ID", "NODE_OPTIONS", "HTTPS_PROXY", "SSH_AUTH_SOCK", "BASH_ENV"} {
		if os.Getenv(key) != "" {
			os.Exit(12)
		}
	}
	root := os.Getenv("HOME")
	cwd, _ := os.Getwd()
	if cwd != root || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(root, "claude") || os.Getenv("ANTHROPIC_BASE_URL") != "http://127.0.0.1:1" || os.Getenv("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC") != "1" {
		os.Exit(13)
	}
	mode := strings.TrimPrefix(filepath.Base(root), "fixture-")
	if mode == "exit" {
		os.Exit(0)
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		os.Exit(14)
	}
	var request struct {
		Type      string    `json:"type"`
		RequestID domain.ID `json:"request_id"`
		Request   struct {
			Subtype string `json:"subtype"`
			Hooks   any    `json:"hooks"`
		} `json:"request"`
	}
	if domain.Decode(scanner.Bytes(), &request) != nil || request.Type != "control_request" || request.RequestID.Validate() != nil || request.Request.Subtype != "initialize" || request.Request.Hooks != nil {
		os.Exit(15)
	}
	result := fixtureResult()
	id := request.RequestID
	if strings.HasPrefix(mode, "v287") {
		_, _ = os.Stdout.Write(fixture287Frames(mode, id))
		if scanner.Scan() {
			os.Exit(16)
		}
		os.Exit(0)
	}
	if mode == "foreign" {
		id = domain.NewID()
	}
	if mode == "account" {
		result["account"] = map[string]any{"tokenSource": "oauth", "apiProvider": "firstParty"}
	}
	raw := fixtureResponse(id, result)
	switch mode {
	case "error":
		raw, _ = json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": id, "error": "private-native-error-sentinel"}})
		raw = append(raw, '\n')
	case "duplicate":
		raw = append(raw, raw...)
	case "trailing":
		raw = append(raw, '{')
	case "request":
		raw = []byte(`{"type":"control_request","request_id":"foreign","request":{"subtype":"can_use_tool"}}` + "\n")
	case "oversize":
		raw = bytes.Repeat([]byte{'x'}, maxProbeFrame+1)
	case "stderr":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte{'e'}, maxProbeStderr+1))
	case "timeout":
		raw = nil
	}
	_, _ = os.Stderr.WriteString("private-stderr-sentinel")
	if mode == "valid" {
		// Native pipes can split UTF-8 anywhere; framing must retain whole bytes.
		for _, b := range raw {
			_, _ = os.Stdout.Write([]byte{b})
		}
	} else {
		_, _ = os.Stdout.Write(raw)
	}
	if scanner.Scan() {
		os.Exit(16) // No second control, input, login or inference is permitted.
	}
	os.Exit(0)
}

func fixtureConfig(t *testing.T, mode string) (ProbeConfig, *bytes.Buffer) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fixture-"+mode)
	for _, name := range []string{"", "claude", "config", "cache", "data", "state", "tmp"} {
		if err := security.PrivateDir(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var path []string
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.IsAbs(entry) {
			path = append(path, entry)
		}
	}
	logs := &bytes.Buffer{}
	env := []string{"PATH=" + strings.Join(path, string(os.PathListSeparator))}
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return ProbeConfig{Version: SupportedVersion, Home: filepath.Join(root, "claude"), Process: process.Config{Directory: filepath.Join(root, "processes"), OwnerID: domain.NewID(), Executable: executable, Env: env, Cwd: root, Logger: slog.New(slog.NewJSONHandler(logs, nil))}}, logs
}

// This opt-in test checks only isolated native initialization. It never reads
// an existing Claude home, account credential or user workspace.
func TestManualNativeClaudeProbe(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	version := os.Getenv("DELIDEV_NATIVE_CLAUDE_VERSION")
	if binary == "" && version == "" {
		t.Skip("set DELIDEV_NATIVE_CLAUDE_EXECUTABLE and DELIDEV_NATIVE_CLAUDE_VERSION for isolated native validation")
	}
	if binary == "" || version == "" {
		t.Fatal("native probe requires both executable and observed version")
	}
	config, _ := fixtureConfig(t, "native")
	config.Process.Executable = binary
	config.Version = version
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := Probe(ctx, config); err != nil {
		t.Fatalf("isolated native initialization failed: %s", domain.SafeError(err).Code)
	}
	if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
		t.Fatal("native probe cleanup was not confirmed")
	}
}

func TestProbeOwnsOnlyBoundedPrivateInitialization(t *testing.T) {
	for _, test := range []struct {
		mode string
		code domain.Code
	}{
		{"valid", ""}, {"foreign", domain.Unsupported}, {"account", domain.Unsupported},
		{"error", domain.Unavailable}, {"duplicate", domain.Unsupported}, {"trailing", domain.Unsupported},
		{"request", domain.Unsupported}, {"oversize", domain.ResourceExhausted}, {"stderr", domain.ResourceExhausted},
		{"exit", domain.Unavailable}, {"timeout", domain.Unavailable},
	} {
		t.Run(test.mode, func(t *testing.T) {
			cfg, logs := fixtureConfig(t, test.mode)
			cfg.Process.Env = append(cfg.Process.Env, "ANTHROPIC_API_KEY=private-key-sentinel", "ANTHROPIC_AUTH_TOKEN=private-token-sentinel", "CLAUDE_CODE_OAUTH_TOKEN=private-oauth-sentinel", "NODE_OPTIONS=private-loader-sentinel", "HTTPS_PROXY=private-proxy-sentinel", "BASH_ENV=private-shell-sentinel", "HOME=foreign", "CLAUDE_CONFIG_DIR=foreign")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if test.mode == "timeout" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 250*time.Millisecond)
			}
			defer cancel()
			err := Probe(ctx, cfg)
			if test.code == "" && err != nil || test.code != "" && (err == nil || domain.SafeError(err).Code != test.code) {
				t.Fatalf("probe outcome: %v; want %s", err, test.code)
			}
			if err := process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(logs.String(), "sentinel") || strings.Contains(logs.String(), "Private native descriptor") || strings.Contains(logs.String(), "fixture-model") {
				t.Fatal("raw private content entered logs")
			}
			if err := filepath.WalkDir(cfg.Process.Directory, func(path string, entry os.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				raw, err := os.ReadFile(path)
				if strings.Contains(string(raw), "sentinel") || strings.Contains(string(raw), "fixture-model") {
					t.Fatal("raw private content entered an ownership journal")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestProbeAcceptsClaude287StartupFrameAndInitializeFields covers the sanitized
// 2.1.287 shape; installed-native and real-account results are separate.
func TestProbeAcceptsClaude287StartupFrameAndInitializeFields(t *testing.T) {
	for _, test := range []struct {
		mode string
		code domain.Code
	}{
		{"v287", ""}, {"v287-noprelude", ""},
		{"v287-twice", domain.Unsupported}, {"v287-late", domain.Unsupported}, {"v287-subtype", domain.Unsupported},
		{"v287-field", domain.Unsupported}, {"v287-event", domain.Unsupported}, {"v287-malformed", domain.Unsupported}, {"v287-oversize", domain.ResourceExhausted},
		{"v287-foreign", domain.Unsupported}, {"v287-duplicate", domain.Unsupported}, {"v287-trailing", domain.Unsupported},
		{"v287-pending", domain.Unsupported}, {"v287-dialog", domain.Unsupported}, {"v287-result-pending", domain.Unsupported}, {"v287-busy", domain.Unsupported},
		{"v287-remote", domain.Unsupported}, {"v287-feedback", domain.Unsupported}, {"v287-feedback-field", domain.Unsupported}, {"v287-account", domain.Unsupported},
	} {
		t.Run(test.mode, func(t *testing.T) {
			cfg, logs := fixtureConfig(t, test.mode)
			cfg.Version = "2.1.287"
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := Probe(ctx, cfg)
			if test.code == "" && err != nil || test.code != "" && (err == nil || domain.SafeError(err).Code != test.code) {
				t.Fatalf("probe outcome: %v; want %s; logs: %s", err, test.code, logs.String())
			}
			if test.code == "" && !strings.Contains(logs.String(), "2.1.287") {
				t.Fatal("observed installed version was not logged")
			}
			if strings.Contains(logs.String(), "private") || strings.Contains(logs.String(), "fixture-session") {
				t.Fatal("raw native content entered logs")
			}
			if err := process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
				t.Fatal("probe cleanup was not confirmed", err)
			}
		})
	}
}

// TestProbeWireAcceptsOnePreludeBeforeResponse checks the prelude state machine
// without a native process.
func TestProbeWireAcceptsOnePreludeBeforeResponse(t *testing.T) {
	for _, test := range []struct {
		name   string
		frames []string
		ok     bool
	}{
		{"prelude-then-response", []string{fixturePrelude, string(fixtureResponseWith("ID", fixtureResult287(), fixtureEnvelope287()))}, true},
		{"second-prelude", []string{fixturePrelude, fixturePrelude}, false},
		{"prelude-after-response", []string{string(fixtureResponseWith("ID", fixtureResult287(), fixtureEnvelope287())), fixturePrelude}, false},
		{"other-system-event", []string{`{"type":"system","subtype":"init"}`}, false},
		{"non-system-event", []string{`{"type":"assistant"}`}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			id := domain.NewID()
			_, cancel := context.WithCancel(context.Background())
			defer cancel()
			wire := &probeWire{id: id, ready: make(chan struct{}, 2), cancel: cancel}
			for _, frame := range test.frames {
				wire.receive([]byte(strings.ReplaceAll(strings.TrimSuffix(frame, "\n"), `"request_id":"ID"`, `"request_id":"`+string(id)+`"`)))
			}
			if test.ok != (wire.status() == nil && wire.initialized()) {
				t.Fatalf("wire outcome: problem=%v initialized=%v", wire.status(), wire.initialized())
			}
		})
	}
}

func TestInitializeValidatesClaude287Fields(t *testing.T) {
	id := domain.NewID()
	if err := validateInitialize(fixtureResponseWith(id, fixtureResult287(), fixtureEnvelope287()), id); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"disabled", "off"} {
		result := fixtureResult287()
		result["feedback_mode"] = mode
		if err := validateInitialize(fixtureResponseWith(id, result, fixtureEnvelope287()), id); err != nil {
			t.Fatalf("previous feedback mode %q was rejected: %v", mode, err)
		}
	}
	for _, change := range []struct {
		name  string
		field string
		value any
	}{
		{"analytics-string", "analytics_disabled", "yes"},
		{"analytics-false", "analytics_disabled", false},
		{"analytics-null", "analytics_disabled", nil},
		{"capabilities-scalar", "capabilities", "all"},
		{"capabilities-null", "capabilities", nil},
		{"capabilities-duplicate", "capabilities", []any{"a", "a"}},
		{"capabilities-too-many", "capabilities", make([]any, 65)},
		{"capabilities-nested", "capabilities", map[string]any{"a": []any{}}},
		{"feedback-enabled", "feedback_mode", "enabled"},
		{"feedback-object-enabled", "feedback_mode", map[string]any{"kind": "enabled"}},
		{"feedback-object-extra", "feedback_mode", map[string]any{"kind": "disabled", "extra": true}},
		{"feedback-object-null", "feedback_mode", map[string]any{"kind": nil}},
		{"feedback-type", "feedback_mode", true},
		{"remote-available", "remote_control_available", true},
		{"remote-null", "remote_control_auto_connect_default", nil},
		{"session-running", "session_state", "running"},
		{"session-type", "session_state", 1},
		{"styles-empty", "user_output_styles_dir", ""},
		{"styles-oversize", "user_output_styles_dir", strings.Repeat("x", maxProbeStylesDir+1)},
		{"styles-type", "user_output_styles_dir", 7},
	} {
		t.Run(change.name, func(t *testing.T) {
			result := fixtureResult287()
			if change.name == "capabilities-too-many" {
				names := make([]any, 65)
				for i := range names {
					names[i] = strings.Repeat("c", i+1)
				}
				change.value = names
			}
			result[change.field] = change.value
			if err := validateInitialize(fixtureResponse(id, result), id); err == nil {
				t.Fatal("unsafe 2.1.287 field accepted")
			}
		})
	}
	for _, shape := range []any{map[string]any{"a": true, "b": "text"}, []any{}} {
		result := fixtureResult287()
		result["capabilities"] = shape
		if err := validateInitialize(fixtureResponse(id, result), id); err != nil {
			t.Fatal("bounded capabilities rejected", err)
		}
	}
}

// rawFrame marshals a frame so tests can control exact key spelling.
func rawFrame(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestControlResponseEnvelopeIsClosedAndExact covers the control-response
// schema: pending lists belong to the inner envelope, are empty when declared,
// and every key must use its exact lowercase spelling.
func TestControlResponseEnvelopeIsClosedAndExact(t *testing.T) {
	id := domain.NewID()
	build := func(mutate func(outer, inner map[string]any)) []byte {
		inner := map[string]any{"subtype": "success", "request_id": id, "response": fixtureResult287()}
		for key, value := range fixtureEnvelope287() {
			inner[key] = value
		}
		outer := map[string]any{"type": "control_response", "response": inner}
		mutate(outer, inner)
		return rawFrame(t, outer)
	}
	for _, test := range []struct {
		name   string
		mutate func(outer, inner map[string]any)
		ok     bool
	}{
		{"both-empty", func(outer, inner map[string]any) {}, true},
		{"absent", func(outer, inner map[string]any) {
			delete(inner, "pending_permission_requests")
			delete(inner, "pending_user_dialog_requests")
		}, true},
		{"only-permission", func(outer, inner map[string]any) { delete(inner, "pending_user_dialog_requests") }, true},
		{"permission-null", func(outer, inner map[string]any) { inner["pending_permission_requests"] = nil }, false},
		{"dialog-null", func(outer, inner map[string]any) { inner["pending_user_dialog_requests"] = nil }, false},
		{"permission-object", func(outer, inner map[string]any) { inner["pending_permission_requests"] = map[string]any{} }, false},
		{"permission-string", func(outer, inner map[string]any) { inner["pending_permission_requests"] = "none" }, false},
		{"permission-nonempty", func(outer, inner map[string]any) {
			inner["pending_permission_requests"] = []any{map[string]any{"request_id": "x"}}
		}, false},
		{"dialog-nonempty", func(outer, inner map[string]any) { inner["pending_user_dialog_requests"] = []any{1} }, false},
		{"unknown-envelope-member", func(outer, inner map[string]any) { inner["pending_other"] = []any{} }, false},
		{"unknown-outer-member", func(outer, inner map[string]any) { outer["extra"] = true }, false},
		{"inner-null", func(outer, inner map[string]any) { outer["response"] = nil }, false},
		{"alias-subtype", func(outer, inner map[string]any) { inner["Subtype"] = "success" }, false},
		{"alias-request-id", func(outer, inner map[string]any) { inner["Request_Id"] = id }, false},
		{"alias-pending", func(outer, inner map[string]any) { inner["Pending_Permission_Requests"] = []any{} }, false},
		{"alias-only-pending", func(outer, inner map[string]any) {
			delete(inner, "pending_permission_requests")
			inner["PENDING_PERMISSION_REQUESTS"] = []any{}
		}, false},
		{"alias-outer-type", func(outer, inner map[string]any) { outer["Type"] = "control_response" }, false},
		{"alias-outer-response", func(outer, inner map[string]any) { outer["Response"] = inner }, false},
		{"alias-only-outer-response", func(outer, inner map[string]any) {
			outer["Response"] = outer["response"]
			delete(outer, "response")
		}, false},
		{"alias-result-key", func(outer, inner map[string]any) {
			result := inner["response"].(map[string]any)
			result["Commands"] = result["commands"]
			delete(result, "commands")
		}, false},
		{"alias-result-account-key", func(outer, inner map[string]any) {
			inner["response"].(map[string]any)["account"].(map[string]any)["APIProvider"] = "firstParty"
		}, false},
		{"alias-result-model-key", func(outer, inner map[string]any) {
			result := inner["response"].(map[string]any)
			models := result["models"].([]any)
			models[0].(map[string]any)["DisplayName"] = "Shadow"
		}, false},
		{"pending-in-result", func(outer, inner map[string]any) {
			inner["response"].(map[string]any)["pending_permission_requests"] = []any{}
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateInitialize(build(test.mutate), id)
			if test.ok != (err == nil) {
				t.Fatalf("outcome: %v; want ok=%t", err, test.ok)
			}
		})
	}
	// Duplicate keys with identical spelling stay rejected by the shared decoder.
	duplicate := strings.Replace(string(build(func(outer, inner map[string]any) {})), `"subtype":"success"`, `"subtype":"success","subtype":"success"`, 1)
	if err := validateInitialize([]byte(duplicate), id); err == nil {
		t.Fatal("duplicate envelope key accepted")
	}
}

// TestProbePreludeIsClosedExactAndNonNull is table-driven over real-shaped
// startup frames: declared null is not a bounded text value, keys must use
// their exact lowercase spelling and the schema stays closed.
func TestProbePreludeIsClosedExactAndNonNull(t *testing.T) {
	for _, test := range []struct {
		name  string
		frame string
		ok    bool
	}{
		{"current", `{"type":"system","subtype":"ui_invalidate","event":"ui.render","uuid":"fixture-uuid","session_id":"fixture-session"}`, true},
		{"full", `{"type":"system","subtype":"ui_invalidate","uuid":"0b1e2d3c-1111-4222-8333-444455556666","session_id":"fixture-session"}`, true},
		{"bare", `{"type":"system","subtype":"ui_invalidate"}`, true},
		{"uuid-only", `{"type":"system","subtype":"ui_invalidate","uuid":"fixture-uuid"}`, true},
		{"session-only", `{"type":"system","subtype":"ui_invalidate","session_id":"fixture-session"}`, true},
		{"uuid-null", `{"type":"system","subtype":"ui_invalidate","uuid":null,"session_id":"fixture-session"}`, false},
		{"session-null", `{"type":"system","subtype":"ui_invalidate","uuid":"fixture-uuid","session_id":null}`, false},
		{"both-null", `{"type":"system","subtype":"ui_invalidate","uuid":null,"session_id":null}`, false},
		{"uuid-number", `{"type":"system","subtype":"ui_invalidate","uuid":7}`, false},
		{"session-object", `{"type":"system","subtype":"ui_invalidate","session_id":{}}`, false},
		{"uuid-empty", `{"type":"system","subtype":"ui_invalidate","uuid":""}`, false},
		{"uuid-oversize", `{"type":"system","subtype":"ui_invalidate","uuid":"` + strings.Repeat("u", maxProbeIdentity+1) + `"}`, false},
		{"uuid-control", `{"type":"system","subtype":"ui_invalidate","uuid":"a\u0000b"}`, false},
		{"subtype-null", `{"type":"system","subtype":null}`, false},
		{"subtype-other", `{"type":"system","subtype":"init"}`, false},
		{"event-other", `{"type":"system","subtype":"ui_invalidate","event":"other"}`, false},
		{"event-null", `{"type":"system","subtype":"ui_invalidate","event":null}`, false},
		{"event-object", `{"type":"system","subtype":"ui_invalidate","event":{}}`, false},
		{"event-alias", `{"type":"system","subtype":"ui_invalidate","Event":"ui.render"}`, false},
		{"unknown-field", `{"type":"system","subtype":"ui_invalidate","extra":1}`, false},
		{"alias-type", `{"Type":"system","Subtype":"ui_invalidate"}`, false},
		{"alias-subtype", `{"type":"system","Subtype":"ui_invalidate"}`, false},
		{"alias-uuid", `{"type":"system","subtype":"ui_invalidate","UUID":"fixture-uuid"}`, false},
		{"alias-session", `{"type":"system","subtype":"ui_invalidate","Session_ID":"fixture-session"}`, false},
		{"alias-shadows-canonical", `{"type":"system","subtype":"ui_invalidate","uuid":"fixture-uuid","UUID":null}`, false},
		{"alias-shadows-type", `{"type":"system","Type":"assistant","subtype":"ui_invalidate"}`, false},
		{"duplicate-key", `{"type":"system","subtype":"ui_invalidate","uuid":"a","uuid":"b"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			id := domain.NewID()
			_, cancel := context.WithCancel(context.Background())
			defer cancel()
			wire := &probeWire{id: id, ready: make(chan struct{}, 2), cancel: cancel}
			wire.receive([]byte(test.frame))
			if test.ok != (wire.status() == nil) {
				t.Fatalf("prelude outcome: problem=%v want ok=%t", wire.status(), test.ok)
			}
		})
	}
}

// TestProbeFramesWithCaseAliasedTypeAreNeverPreludes checks that an aliased
// type key cannot route a control response around the exact-key validation.
func TestProbeFramesWithCaseAliasedTypeAreNeverPreludes(t *testing.T) {
	id := domain.NewID()
	if !probeFrameIsPrelude([]byte(`{"type":"system","subtype":"ui_invalidate"}`)) {
		t.Fatal("exact prelude not recognized")
	}
	for _, frame := range []string{`{"Type":"system"}`, `{"type":"control_response","Type":"system"}`, `{"type":null}`, `[]`, `{`} {
		if probeFrameIsPrelude([]byte(frame)) {
			t.Fatalf("%s treated as prelude", frame)
		}
	}
	shadowed := `{"type":"system","Type":"control_response","response":{"subtype":"success","request_id":"` + string(id) + `","response":{}}}`
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire := &probeWire{id: id, ready: make(chan struct{}, 2), cancel: cancel}
	wire.receive([]byte(shadowed))
	if wire.status() == nil || wire.initialized() {
		t.Fatal("aliased type frame was accepted")
	}
}

func TestProbeRejectsInvalidProfileOrRuntimeBeforeLaunch(t *testing.T) {
	for _, change := range []string{"version", "cwd", "home", "retained-config", "path", "duplicate-path", "nonprivate"} {
		t.Run(change, func(t *testing.T) {
			if change == "nonprivate" && runtime.GOOS == "windows" {
				t.Skip("Unix mode fixture; Windows uses native ACLs")
			}
			cfg, _ := fixtureConfig(t, "valid")
			switch change {
			case "version":
				cfg.Version = "2.1.235"
			case "cwd":
				cfg.Process.Cwd = filepath.Dir(cfg.Process.Cwd)
			case "home":
				cfg.Home = "relative"
			case "retained-config":
				if err := os.WriteFile(filepath.Join(cfg.Home, "settings.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "path":
				cfg.Process.Env = []string{"PATH=."}
			case "duplicate-path":
				cfg.Process.Env = append(cfg.Process.Env, "Path=/bin")
			case "nonprivate":
				if err := os.Chmod(cfg.Home, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := Probe(context.Background(), cfg); err == nil {
				t.Fatal("invalid native probe accepted")
			}
			if _, err := os.Stat(cfg.Process.Directory); !os.IsNotExist(err) {
				t.Fatal("rejected runtime launched a process")
			}
		})
	}
}

func TestInitializeRejectsChangedOrAmbiguousNativeScope(t *testing.T) {
	id := domain.NewID()
	for _, change := range []string{"missing-remote", "remote", "permission", "style", "missing-account", "credential", "empty-api-source", "null-api-source", "provider", "commands", "empty-models", "duplicate-model", "unknown-level", "pid", "unknown-field", "duplicate-json", "invalid-utf8", "null-body", "missing-body", "mixed-error", "null-error", "null-envelope"} {
		t.Run(change, func(t *testing.T) {
			result := fixtureResult()
			switch change {
			case "missing-remote":
				delete(result, "remote_control_auto_enable")
			case "remote":
				result["remote_control_auto_enable"] = true
			case "permission":
				result["current_permission_mode"] = "bypassPermissions"
			case "style":
				result["output_style"] = "unknown"
			case "missing-account":
				delete(result, "account")
			case "credential":
				result["account"].(map[string]any)["tokenSource"] = "apiKeyHelper"
			case "empty-api-source":
				result["account"].(map[string]any)["apiKeySource"] = ""
			case "null-api-source":
				result["account"].(map[string]any)["apiKeySource"] = nil
			case "provider":
				result["account"].(map[string]any)["apiProvider"] = "bedrock"
			case "commands":
				result["commands"] = []any{map[string]any{"name": "external"}}
			case "empty-models":
				result["models"] = []any{}
			case "duplicate-model":
				result["models"] = append(result["models"].([]any), result["models"].([]any)[0])
			case "unknown-level":
				result["models"].([]any)[0].(map[string]any)["supportedEffortLevels"] = []string{"unknown"}
			case "pid":
				result["pid"] = 0
			case "unknown-field":
				result["unknownAuthority"] = true
			}
			raw := fixtureResponse(id, result)
			switch change {
			case "duplicate-json":
				raw = bytes.Replace(raw, []byte(`"tokenSource":"none"`), []byte(`"tokenSource":"oauth","tokenSource":"none"`), 1)
			case "invalid-utf8":
				raw = bytes.Replace(raw, []byte("Fixture"), []byte{0xff}, 1)
			case "null-body":
				raw = fixtureResponse(id, nil)
			case "missing-body":
				raw = []byte(`{"type":"control_response","response":{"subtype":"success","request_id":"` + string(id) + `"}}`)
			case "mixed-error":
				raw = bytes.Replace(raw, []byte(`"subtype":"success"`), []byte(`"subtype":"success","error":"private"`), 1)
			case "null-error":
				raw = bytes.Replace(raw, []byte(`"subtype":"success"`), []byte(`"subtype":"success","error":null`), 1)
			case "null-envelope":
				raw = []byte(`null`)
			}
			if err := validateInitialize(raw, id); err == nil {
				t.Fatal("invalid native initialization accepted")
			}
		})
	}
	if err := validateInitialize(fixtureResponse(id, fixtureResult()), id); err != nil {
		t.Fatal(err)
	}
}

func TestProbeFramesBoundUnterminatedOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire := &probeWire{id: domain.NewID(), ready: make(chan struct{}, 1), cancel: cancel}
	w := &probeFrames{wire: wire}
	if _, err := io.CopyN(w, strings.NewReader(strings.Repeat("x", maxProbeFrame+1)), maxProbeFrame+1); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil || wire.status() == nil || wire.status().Code != domain.ResourceExhausted || w.buffer != nil {
		t.Fatal("unbounded incomplete native frame")
	}
}

// Cancel synchronously after real native ownership is prepared and before
// process.Start returns its handle. This covers the narrow start-barrier race
// without sleeping or extending the production probe deadline.
type cancelPreparedProbeLog struct {
	bytes.Buffer
	cancel context.CancelFunc
	once   sync.Once
}

func (w *cancelPreparedProbeLog) Write(raw []byte) (int, error) {
	if bytes.Contains(raw, []byte(`"msg":"native process prepared"`)) {
		w.once.Do(w.cancel)
	}
	return w.Buffer.Write(raw)
}

func TestProbeCancellationAtPreparedNativeBarrierIsTypedAndJoined(t *testing.T) {
	cfg, _ := fixtureConfig(t, "valid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := &cancelPreparedProbeLog{cancel: cancel}
	cfg.Process.Logger = slog.New(slog.NewJSONHandler(log, nil))
	err := Probe(ctx, cfg)
	problem, ok := err.(*domain.Error)
	if !ok || problem.Code != domain.Canceled || ctx.Err() != context.Canceled {
		t.Fatal("prepared probe cancellation lost its typed boundary", err)
	}
	if strings.Contains(log.String(), `"msg":"native process resumed"`) || strings.Contains(log.String(), "broken pipe") || strings.Contains(problem.Error(), cfg.Process.Directory) {
		t.Fatal("canceled probe crossed native start or disclosed private transport")
	}
	if err := process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
		t.Fatal("canceled prepared probe did not join cleanup", err)
	}
}

// A persistent profile keeps native caches and history; only user-authored
// configuration surfaces are refused.
func TestProbeAcceptsRetainedNativeCacheAndHelperState(t *testing.T) {
	cfg, _ := fixtureConfig(t, "valid")
	if err := os.WriteFile(filepath.Join(cfg.Process.Cwd, "cache", "previous"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.Home, "projects"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := probeEnvironment(cfg); err != nil {
		t.Fatalf("retained native state blocked reuse: %v", err)
	}
}

// TestProbeAdmitsNewerVersionsToRealNativeChecks proves that a SemVer at or above
// the baseline reaches the same native checks, while malformed or lower
// versions stop before any process launch.
func TestProbeAdmitsNewerVersionsToRealNativeChecks(t *testing.T) {
	for _, version := range []string{"2.1.236", "2.1.237", "3.0.0-rc.1", "2.1.236+build.7"} {
		t.Run("admit-"+version, func(t *testing.T) {
			config, logs := fixtureConfig(t, "valid")
			config.Version = version
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := Probe(ctx, config); err != nil {
				t.Fatalf("probe: %v; logs: %s", err, logs.String())
			}
			if !strings.Contains(logs.String(), version) {
				t.Fatal("observed installed version was not logged")
			}
			if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, version := range []string{"", "v2.1.237", "2.1.235", "2.1.236-rc.1", "not-a-version", "2.1.237.0"} {
		t.Run("reject-"+version, func(t *testing.T) {
			config, _ := fixtureConfig(t, "valid")
			config.Version = version
			err := Probe(context.Background(), config)
			if err == nil || domain.SafeError(err).Code != domain.Unsupported {
				t.Fatalf("version %q outcome: %v", version, err)
			}
			if _, statErr := os.Stat(config.Process.Directory); !os.IsNotExist(statErr) {
				t.Fatal("rejected version launched a process")
			}
		})
	}
}

// TestProbeNewerVersionStillFailsChangedNativeBehavior proves admission does not
// bypass the native protocol or settings validation.
func TestProbeNewerVersionStillFailsChangedNativeBehavior(t *testing.T) {
	config, logs := fixtureConfig(t, "account")
	config.Version = "2.1.237"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := Probe(ctx, config)
	if err == nil || domain.SafeError(err).Code != domain.Unsupported {
		t.Fatalf("changed native behavior outcome: %v; logs: %s", err, logs.String())
	}
	if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
		t.Fatal(err)
	}
}

// TestManagedTokenInitializeSharesClaude287Validation proves the API/session
// initializer applies the same strict additions and still rejects unsafe state.
func TestManagedTokenInitializeSharesClaude287Validation(t *testing.T) {
	build := func() map[string]any {
		result := fixtureResult287()
		result["account"] = map[string]any{"tokenSource": managedTokenSource, "apiProvider": "firstParty"}
		result["commands"] = []any{map[string]any{"name": "compact", "description": "Compact", "argumentHint": "", "builtin": true}}
		result["remote_control_available"] = true
		return result
	}
	raw, _ := json.Marshal(build())
	if err := validateInitializeManagedToken(raw, "dontAsk"); err != nil {
		t.Fatal(err)
	}
	result := build()
	result["session_state"] = "running"
	raw, _ = json.Marshal(result)
	if err := validateInitializeManagedToken(raw, "dontAsk"); err == nil {
		t.Fatal("non-idle managed session accepted")
	}
	for _, field := range []string{"remote_control_auto_enable", "remote_control_auto_connect_default", "remote_control_auto_on_by_default", "ide_rc_auto_enable_gate"} {
		result := build()
		result[field] = true
		raw, _ := json.Marshal(result)
		if validateInitializeManagedToken(raw, "dontAsk") == nil {
			t.Fatalf("enabled activation gate accepted: %s", field)
		}
	}
}

// descriptorAdditions returns unused members of every JSON type.
func descriptorAdditions() map[string]any {
	return map[string]any{
		"builtin": true, "futureString": "text", "futureBool": false, "futureNumber": 12, "futureNull": nil,
		"futureArray": []any{1, "two", nil, map[string]any{"deep": []any{}}}, "futureObject": map[string]any{"a": map[string]any{"b": nil}},
	}
}

func withDescriptorAdditions(result map[string]any) map[string]any {
	for _, list := range []string{"commands", "agents", "models"} {
		for _, item := range result[list].([]any) {
			for key, value := range descriptorAdditions() {
				item.(map[string]any)[key] = value
			}
		}
	}
	return result
}

// TestInitializeDiscardsUnusedDescriptorMembers proves the one shared policy
// serves the probe, API/subscription and managed-token validators and that the
// accepted projection is identical with and without additions.
func TestInitializeDiscardsUnusedDescriptorMembers(t *testing.T) {
	id := domain.NewID()
	build := func() map[string]any {
		result := fixtureResult287()
		result["account"] = map[string]any{"tokenSource": "ANTHROPIC_AUTH_TOKEN", "apiProvider": "firstParty", "apiKeySource": "ANTHROPIC_API_KEY"}
		result["commands"] = []any{map[string]any{"name": "compact", "description": "Compact", "argumentHint": ""}}
		return result
	}
	bare := fixtureResult287()
	for _, list := range []string{"agents", "models"} {
		for _, item := range bare[list].([]any) {
			for key, value := range descriptorAdditions() {
				item.(map[string]any)[key] = value
			}
		}
	}
	if err := validateInitialize(fixtureResponseWith(id, bare, fixtureEnvelope287()), id); err != nil {
		t.Fatalf("probe rejected descriptor additions: %v", err)
	}
	plain, _ := json.Marshal(build())
	extended, _ := json.Marshal(withDescriptorAdditions(build()))
	if err := validateInitializeMode(plain, "dontAsk", "ANTHROPIC_API_KEY", false); err != nil {
		t.Fatal(err)
	}
	if err := validateInitializeMode(extended, "dontAsk", "ANTHROPIC_API_KEY", false); err != nil {
		t.Fatalf("API initialization rejected descriptor additions: %v", err)
	}
	projectedPlain, err := projectInitializeDescriptors(plain)
	if err != nil {
		t.Fatal(err)
	}
	projectedExtended, err := projectInitializeDescriptors(extended)
	if err != nil {
		t.Fatal(err)
	}
	var left, right map[string]any
	if json.Unmarshal(projectedPlain, &left) != nil || json.Unmarshal(projectedExtended, &right) != nil || !reflect.DeepEqual(left, right) {
		t.Fatal("descriptor additions changed the accepted projection")
	}

	subscription := withDescriptorAdditions(build())
	subscription["account"] = map[string]any{"apiProvider": "firstParty", "email": "fixture@example.invalid", "subscriptionType": "pro"}
	raw, _ := json.Marshal(subscription)
	if err := validateInitializeMode(raw, "dontAsk", "ANTHROPIC_API_KEY", true); err != nil {
		t.Fatalf("subscription initialization rejected descriptor additions: %v", err)
	}
	managed := withDescriptorAdditions(build())
	managed["account"] = map[string]any{"tokenSource": managedTokenSource, "apiProvider": "firstParty"}
	raw, _ = json.Marshal(managed)
	if err := validateInitializeManagedToken(raw, "dontAsk"); err != nil {
		t.Fatalf("managed-token initialization rejected descriptor additions: %v", err)
	}
}

// TestInitializeDescriptorProjectionStaysFailClosed covers the boundaries the
// projection must not relax.
func TestInitializeDescriptorProjectionStaysFailClosed(t *testing.T) {
	build := func() map[string]any {
		result := fixtureResult287()
		result["account"] = map[string]any{"tokenSource": managedTokenSource, "apiProvider": "firstParty"}
		result["commands"] = []any{map[string]any{"name": "compact", "description": "Compact", "argumentHint": ""}}
		return withDescriptorAdditions(result)
	}
	marshal := func(result map[string]any) []byte {
		raw, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	first := func(result map[string]any, list string) map[string]any {
		return result[list].([]any)[0].(map[string]any)
	}
	nested := func(depth int) any {
		var value any = "leaf"
		for i := 0; i < depth; i++ {
			value = []any{value}
		}
		return value
	}
	cases := []struct {
		name string
		edit func(map[string]any) []byte
	}{
		{"command-required-deleted", func(r map[string]any) []byte { delete(first(r, "commands"), "name"); return marshal(r) }},
		{"agent-required-deleted", func(r map[string]any) []byte { delete(first(r, "agents"), "description"); return marshal(r) }},
		{"model-required-deleted", func(r map[string]any) []byte { delete(first(r, "models"), "resolvedModel"); return marshal(r) }},
		{"command-type-changed", func(r map[string]any) []byte { first(r, "commands")["description"] = 7; return marshal(r) }},
		{"model-type-changed", func(r map[string]any) []byte { first(r, "models")["supportsEffort"] = "yes"; return marshal(r) }},
		{"command-alias-case", func(r map[string]any) []byte { first(r, "commands")["ArgumentHint"] = "x"; return marshal(r) }},
		{"agent-alias-case", func(r map[string]any) []byte { first(r, "agents")["NAME"] = "x"; return marshal(r) }},
		{"model-alias-case", func(r map[string]any) []byte { first(r, "models")["Value"] = "x"; return marshal(r) }},
		{"known-duplicate", func(r map[string]any) []byte {
			return bytes.Replace(marshal(r), []byte(`"name":"compact"`), []byte(`"name":"compact","name":"compact"`), 1)
		}},
		{"unknown-duplicate", func(r map[string]any) []byte {
			return bytes.Replace(marshal(r), []byte(`"futureBool":false`), []byte(`"futureBool":false,"futureBool":true`), 1)
		}},
		{"duplicate-inside-discarded", func(r map[string]any) []byte {
			return bytes.Replace(marshal(r), []byte(`"b":null`), []byte(`"b":null,"b":1`), 1)
		}},
		{"invalid-json-inside-discarded", func(r map[string]any) []byte {
			first(r, "commands")["futureString"] = "@@"
			return bytes.Replace(marshal(r), []byte(`"@@"`), []byte(`{"broken":`), 1)
		}},
		{"nesting-beyond-64", func(r map[string]any) []byte { first(r, "agents")["futureArray"] = nested(70); return marshal(r) }},
		{"string-limit", func(r map[string]any) []byte {
			first(r, "commands")["description"] = strings.Repeat("x", (16<<10)+1)
			return marshal(r)
		}},
		{"list-limit", func(r map[string]any) []byte { r["commands"] = make([]any, 257); return marshal(r) }},
		{"top-level-unknown", func(r map[string]any) []byte { r["futureTopLevel"] = true; return marshal(r) }},
		{"account-unknown", func(r map[string]any) []byte {
			r["account"].(map[string]any)["futureAccount"] = true
			return marshal(r)
		}},
		{"pending-in-result", func(r map[string]any) []byte { r["pending_permission_requests"] = []any{}; return marshal(r) }},
		{"top-level-alias", func(r map[string]any) []byte { r["Commands"] = []any{}; return marshal(r) }},
		{"token-source", func(r map[string]any) []byte {
			r["account"].(map[string]any)["tokenSource"] = "none"
			return marshal(r)
		}},
		{"permission", func(r map[string]any) []byte { r["current_permission_mode"] = "default"; return marshal(r) }},
		{"session-state", func(r map[string]any) []byte { r["session_state"] = "running"; return marshal(r) }},
		{"feedback", func(r map[string]any) []byte { r["feedback_mode"] = "enabled"; return marshal(r) }},
		{"remote-control", func(r map[string]any) []byte { r["remote_control_auto_enable"] = true; return marshal(r) }},
	}
	if err := validateInitializeManagedToken(marshal(build()), "dontAsk"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateInitializeManagedToken(tc.edit(build()), "dontAsk")
			if err == nil || domain.SafeError(err).Code != domain.Unsupported {
				t.Fatalf("outcome %v, want typed incompatible refusal", err)
			}
		})
	}
	// The same decoder refuses the control-response envelope's unknown members.
	id := domain.NewID()
	envelope := fixtureEnvelope287()
	envelope["futureEnvelope"] = true
	probe := fixtureResult287()
	if err := validateInitialize(fixtureResponseWith(id, probe, envelope), id); err == nil {
		t.Fatal("unknown envelope member accepted")
	}
}

func TestPrivateClaudeEnvironmentPinsFeedbackOptOut(t *testing.T) {
	config, _ := fixtureConfig(t, "feedback-opt-out")
	config.Process.Env = append(config.Process.Env, "DISABLE_FEEDBACK_COMMAND=0", "CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY=0")
	env, err := probeEnvironment(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"DISABLE_FEEDBACK_COMMAND=1", "CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"} {
		found := false
		for _, value := range env {
			if value == expected {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing private feedback setting: %s", expected)
		}
	}
}

// TestInitializeProjectionPreservesRetainedEncodingAndFrameBound proves the
// projection never re-encodes retained bytes. Characters that json.Marshal
// would expand (HTML characters and U+2028/U+2029) must not push an initialize
// result that passed the 1 MiB frame bound past it, for every initializer and
// every descriptor list.
func TestInitializeProjectionPreservesRetainedEncodingAndFrameBound(t *testing.T) {
	const (
		commandCount = 55
		payloadBytes = 15000
	)
	payloads := []struct {
		name  string
		glyph string
	}{
		{"less-than", "<"},
		{"greater-than", ">"},
		{"ampersand", "&"},
		{"line-separator", "\u2028"},
		{"paragraph-separator", "\u2029"},
	}
	base := func(account map[string]any) map[string]any {
		result := fixtureResult287()
		result["account"] = account
		return result
	}
	initializers := []struct {
		name     string
		account  map[string]any
		validate func([]byte) error
	}{
		{"api", map[string]any{"tokenSource": "ANTHROPIC_AUTH_TOKEN", "apiProvider": "firstParty", "apiKeySource": "ANTHROPIC_API_KEY"}, func(raw []byte) error {
			return validateInitializeMode(raw, "dontAsk", "ANTHROPIC_API_KEY", false)
		}},
		{"subscription", map[string]any{"apiProvider": "firstParty", "email": "fixture@example.invalid", "subscriptionType": "pro"}, func(raw []byte) error {
			return validateInitializeMode(raw, "dontAsk", "ANTHROPIC_API_KEY", true)
		}},
		{"managed-token", map[string]any{"tokenSource": managedTokenSource, "apiProvider": "firstParty"}, func(raw []byte) error {
			return validateInitializeManagedToken(raw, "dontAsk")
		}},
	}
	for _, payload := range payloads {
		for _, initializer := range initializers {
			t.Run(payload.name+"/"+initializer.name, func(t *testing.T) {
				// A leading letter keeps the description non-blank for text validation.
				text := "x" + strings.Repeat(payload.glyph, (payloadBytes-1)/len(payload.glyph))
				commands := make([]any, commandCount)
				for i := range commands {
					commands[i] = map[string]any{"name": fmt.Sprintf("cmd-%d", i), "description": "@@LARGE@@", "argumentHint": ""}
				}
				commands[0].(map[string]any)["name"] = "compact"
				result := base(initializer.account)
				result["commands"] = commands
				encoded, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				// Substitute the literal characters so the frame carries them
				// unescaped, as a native process writes them.
				raw := bytes.ReplaceAll(encoded, []byte("@@LARGE@@"), []byte(text))
				if len(raw) >= 1<<20 {
					t.Fatalf("fixture frame is %d bytes, want below the 1 MiB bound", len(raw))
				}
				projected, err := projectInitializeDescriptors(raw)
				if err != nil {
					t.Fatalf("projection refused a frame within the bound: %v", err)
				}
				if !bytes.Equal(projected, raw) {
					t.Fatalf("projection re-encoded retained bytes: %d projected bytes from %d", len(projected), len(raw))
				}
				if err := initializer.validate(raw); err != nil {
					t.Fatalf("initializer refused a frame within the bound: %v", err)
				}
			})
		}
	}

	t.Run("agents-and-models-keep-original-encoding", func(t *testing.T) {
		result := fixtureResult287()
		result["commands"] = []any{}
		result["agents"] = []any{map[string]any{"name": "Explore", "description": "@@TEXT@@", "futureAgent": "@@TEXT@@"}}
		models := fixtureResult287()["models"].([]any)
		models[0].(map[string]any)["description"] = "@@TEXT@@"
		models[0].(map[string]any)["futureModel"] = "@@TEXT@@"
		result["models"] = models
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		text := "a<b>&c\u2028d\u2029e"
		raw := bytes.ReplaceAll(encoded, []byte("@@TEXT@@"), []byte(text))
		projected, err := projectInitializeDescriptors(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := bytes.Count(projected, []byte(text)); got != 2 {
			t.Fatalf("projection kept %d retained descriptions unescaped, want 2 (the discarded members are dropped)", got)
		}
		if len(projected) > len(raw) {
			t.Fatalf("projection grew from %d to %d bytes", len(raw), len(projected))
		}
		if err := validateInitializeMode(raw, "dontAsk", "", false); err != nil {
			t.Fatalf("probe initializer refused retained descriptors: %v", err)
		}
	})

	t.Run("expanding-top-level-key-fails-closed", func(t *testing.T) {
		result := fixtureResult287()
		result["commands"] = []any{}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		raw := bytes.Replace(encoded, []byte(`{`), []byte("{\"a\u2028\":true,"), 1)
		if _, err := projectInitializeDescriptors(raw); err == nil {
			t.Fatal("a projection larger than its validated frame was accepted")
		}
	})
}
