package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func init() {
	if len(os.Args) < 2 || filepath.Base(os.Getenv("HOME")) != "claude-code" {
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(domain.ClaudeProtocolVersion + " (Claude Code)")
		os.Exit(0)
	}
	if os.Args[1] != "--bare" {
		os.Exit(40)
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		os.Exit(41)
	}
	var request struct {
		Type      string    `json:"type"`
		RequestID domain.ID `json:"request_id"`
		Request   struct {
			Subtype string `json:"subtype"`
			Hooks   any    `json:"hooks"`
		} `json:"request"`
	}
	if domain.Decode(scanner.Bytes(), &request) != nil || request.Type != "control_request" || request.Request.Subtype != "initialize" || request.RequestID.Validate() != nil {
		os.Exit(42)
	}
	result := map[string]any{
		"commands": []any{}, "agents": []any{map[string]any{"name": "Explore", "description": "Private fixture"}},
		"output_style": "default", "available_output_styles": []string{"default"},
		"models":  []any{map[string]any{"value": "fixture", "resolvedModel": "fixture-model", "displayName": "Fixture", "description": "Private fixture"}},
		"account": map[string]any{"tokenSource": "none", "apiProvider": "firstParty"},
		"pid":     os.Getpid(), "current_permission_mode": "dontAsk", "remote_control_auto_enable": false,
		"remote_control_auto_on_by_default": false, "ide_rc_auto_enable_gate": false,
		"fast_mode_state": "off", "fast_mode_disabled_reason": "sdk_opt_in_required",
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": request.RequestID, "response": result}})
	if scanner.Scan() {
		os.Exit(43)
	}
	os.Exit(0)
}

func discoverClaude(t *testing.T, binary string) {
	t.Helper()
	input := allMissing(t)
	input.VerifyProtocol = true
	input.Selections.Executables[1].Path = binary
	root, owner := discoveryRoot(t), domain.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := Discover(ctx, DiscoveryConfig{Root: root, OwnerID: owner}, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := output.ValidateDiscovery(input); err != nil {
		t.Fatal(err)
	}
	i := output.Installations[1]
	if i.Harness != domain.ClaudeCode || i.Version != domain.ClaudeProtocolVersion || !i.ProtocolVerified || i.Protocol == nil || i.Protocol.State != domain.ProtocolVerified || i.Protocol.Problem != nil || len(i.Capabilities) != 0 {
		t.Fatalf("Claude discovery did not verify the isolated profile: state=%s version=%s protocol=%+v", i.State, i.Version, i.Protocol)
	}
	if err := process.ReconcileOwner(filepath.Join(root, "processes"), owner); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "probes"))
	if err != nil || len(entries) != 0 {
		t.Fatal("completed Claude probe runtime was not removed")
	}
}

func TestDiscoveryVerifiesClaudeWithoutGrantingExecution(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	discoverClaude(t, binary)
}

func TestManualNativeClaudeDiscovery(t *testing.T) {
	// Ordinary tests never invoke installed harnesses. This explicitly selected
	// binary receives only fresh private directories, bare mode, no credentials
	// and no input except the non-inference native initialize control request.
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit private Claude Code protocol validation")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("the selected native executable must be absolute")
	}
	discoverClaude(t, binary)
}
