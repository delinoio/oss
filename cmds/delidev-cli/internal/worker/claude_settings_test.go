package worker

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type claudeSettingsAuthority struct{ t *testing.T }

func (a claudeSettingsAuthority) Acquire(context.Context, string) (*apiproxy.Lease, error) {
	a.t.Error("permission initialization attempted to acquire provider authority")
	return nil, domain.Fail(domain.Unauthenticated, "No fixture inference authority.", "")
}

func TestClaudeExecutionSettingsNeverDropUnsupportedSelections(t *testing.T) {
	f, _ := claudeCheckpointMetadataFixture(t)
	for _, change := range []func(*domain.ExecutionConfiguration){
		func(c *domain.ExecutionConfiguration) { c.Harness = domain.Codex },
		func(c *domain.ExecutionConfiguration) { c.Options.SubagentModel = "other" },
		func(c *domain.ExecutionConfiguration) { c.Options.SubagentEffort = "high" },
		func(c *domain.ExecutionConfiguration) { c.Options.MaxConcurrency = 1 },
		func(c *domain.ExecutionConfiguration) { c.Options.ApprovalReviewModel = "other" },
		func(c *domain.ExecutionConfiguration) { c.Options.ApprovalPolicy = "never" },
		func(c *domain.ExecutionConfiguration) { c.Options.ServiceTier = "fast" },
		func(c *domain.ExecutionConfiguration) { c.Effort = "High" },
		func(c *domain.ExecutionConfiguration) { c.Options.Permission = domain.PermissionReadOnly },
	} {
		c := f.input.Configuration
		change(&c)
		if _, _, err := claudeExecutionSettings(c, domain.PlanMode); err == nil {
			t.Fatal("unsupported explicit option was silently omitted")
		}
	}
}

func TestManualNativeClaudeWorkerPermissionSettings(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native binary and private runtime required")
	}
	// Native full-mode initialization may HEAD its /api/hello path. The actual
	// relay rejects that unsupported route before acquiring any account/key;
	// it is distinct from inference and needs no permissive fixture endpoint.
	relay := httptest.NewServer(apiproxy.New(claudeSettingsAuthority{t}, slog.New(slog.NewJSONHandler(io.Discard, nil))))
	defer relay.Close()
	for _, selected := range []domain.ClaudePermissionMode{"", domain.ClaudePermissionDefault, domain.ClaudePermissionPlan, domain.ClaudePermissionAcceptEdits, domain.ClaudePermissionDontAsk, domain.ClaudePermissionBypass} {
		t.Run(string(selected), func(t *testing.T) {
			f, _ := claudeCheckpointMetadataFixture(t)
			c := f.input.Configuration
			c.Options.ClaudePermission = selected
			permission, effort, err := claudeExecutionSettings(c, domain.ExecuteMode)
			if err != nil {
				t.Fatal(err)
			}
			runtimeRoot := filepath.Join(f.root, "runtimes", string(f.input.ExecutionID))
			env, err := harness.PrivateRuntimeEnvironment(runtimeRoot)
			if err != nil {
				t.Fatal(err)
			}
			workdir := filepath.Join(f.root, "workspace")
			if err := security.PrivateDir(workdir); err != nil {
				t.Fatal(err)
			}
			cfg := claude.APIStreamConfig{Process: process.Config{Directory: filepath.Join(f.root, "processes"), OwnerID: f.jobID, Executable: binary, Cwd: runtimeRoot, Env: env, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, Version: claude.SupportedVersion, Home: filepath.Join(runtimeRoot, "claude"), Workspace: workdir, SessionID: f.input.SessionID, Model: c.NativeModel, Effort: effort, Permission: permission, Instructions: c.Instructions, API: claude.APIConfig{ServerOrigin: relay.URL, Token: claudeCheckpointToken(44)}}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// Open verifies the exact native permission and account sources, then
			// separately queries applied model/effort. No prompt is submitted.
			s, err := claude.OpenAPIStream(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
				if err := process.ReconcileOwner(cfg.Process.Directory, f.jobID); err != nil {
					t.Error(err)
				}
			}()
			applied, ok := s.InitialAppliedSettings()
			if !ok || applied.Effort == nil {
				t.Fatal("native applied settings missing")
			}
			observedEffort := string(*applied.Effort)
			o := domain.ObservedExecutionSettings{Model: applied.Model, Effort: &observedEffort, Permission: domain.PermissionDefault, ClaudePermission: domain.ClaudePermissionMode(permission)}
			if err := o.ValidateForInput(c, domain.ExecuteMode); err != nil {
				t.Fatal(err)
			}
		})
	}
}
