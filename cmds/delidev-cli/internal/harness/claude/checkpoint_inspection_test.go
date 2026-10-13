package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestCheckpointInspectionNeedsNoPromptOrReplacementAuthority(t *testing.T) {
	for _, name := range []string{"root", "read", "bash", "question"} {
		t.Run(name, func(t *testing.T) {
			var s *APISession
			switch name {
			case "root":
				s, _ = continuationFixture(t)
			case "read":
				s = readContinuationFixture(t)
			case "bash":
				s, _ = approvedBashContinuationFixture(t)
			case "question":
				s, _ = answeredQuestionContinuationFixture(t)
			}
			for _, message := range s.history.messages {
				s.current.seen[message.NativeID] = true
			}
			// Pin a nonempty original instruction body in the independently
			// retained checkpoint, then inspect using its digest alone.
			s.config.Instructions = "Private original instructions for inspection."
			closed, err := s.CloseForContinuation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			raw, ref, err := closed.RetainCheckpoint(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			cfg := s.config
			digest := checkpointDigest([]byte(cfg.Instructions))
			cfg.Instructions = ""
			cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
			for i := 0; i < 2; i++ {
				if err := InspectCheckpoint(context.Background(), cfg, digest, raw, ref); err != nil {
					t.Fatal("original observation could not be inspected", err)
				}
			}
			if restored, err := RestoreCheckpoint(context.Background(), cfg, raw, ref); err == nil || restored != nil {
				t.Fatal("digest-only inspection granted ordinary replacement")
			}
			cfg.Instructions = s.config.Instructions
			if _, err := RestoreCheckpoint(context.Background(), cfg, raw, ref); err != nil {
				t.Fatal("inspection changed original history", err)
			}
		})
	}
}

func TestCheckpointInspectionRejectsChangedComparisonOrHistory(t *testing.T) {
	for _, name := range []string{"digest", "instruction-digest", "instruction-body", "credential", "model", "permission", "workspace", "owner", "input", "history", "canceled"} {
		t.Run(name, func(t *testing.T) {
			_, cfg, raw, ref := retainedCheckpointFixture(t)
			instructions := checkpointDigest([]byte(cfg.Instructions))
			cfg.Instructions = ""
			ctx := context.Background()
			switch name {
			case "digest":
				ref.SHA256 = checkpointDigest([]byte("foreign"))
			case "instruction-digest":
				instructions = checkpointDigest([]byte("foreign"))
			case "instruction-body":
				cfg.Instructions = "unexpected private body"
			case "credential":
				cfg.API.Token = nativeAPIFixtureToken
			case "model":
				cfg.Model = "different"
			case "permission":
				cfg.Permission = BypassPermission
			case "workspace":
				cfg.Workspace = filepath.Dir(cfg.Workspace)
			case "owner":
				ref.OwnerID = domain.NewID()
			case "input":
				ref.InputID = domain.NewID()
			case "history":
				if err := os.WriteFile(filepath.Join(cfg.Home, "projects", "delidev", string(cfg.SessionID)+".jsonl"), []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := InspectCheckpoint(ctx, cfg, instructions, raw, ref); err == nil {
				t.Fatal("unproved comparison gained inspection evidence")
			}
		})
	}
}

// Checkpoints pin the composed native instructions, so inspection accepts the
// composed digest (template plus Execute branch-prefix guidance) and refuses the
// uncomposed template digest without granting replacement authority.
func TestCheckpointInspectionPinsComposedExecuteInstructions(t *testing.T) {
	configuration := domain.ExecutionConfiguration{Instructions: "Private original template.", BranchPrefix: &domain.BranchPrefixSelection{Version: 1, Prefix: domain.DefaultBranchPrefix}}
	composed, err := configuration.NativeInstructions(domain.ExecuteMode)
	if err != nil || composed == configuration.Instructions {
		t.Fatal("fixture did not compose branch prefix guidance", err)
	}
	s, _ := continuationFixture(t)
	for _, message := range s.history.messages {
		s.current.seen[message.NativeID] = true
	}
	s.config.Instructions = composed
	closed, err := s.CloseForContinuation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, ref, err := closed.RetainCheckpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.config
	cfg.Instructions = ""
	cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
	if err := InspectCheckpoint(context.Background(), cfg, checkpointDigest([]byte(composed)), raw, ref); err != nil {
		t.Fatal("composed original instruction digest was refused", err)
	}
	if err := InspectCheckpoint(context.Background(), cfg, checkpointDigest([]byte(configuration.Instructions)), raw, ref); err == nil {
		t.Fatal("uncomposed template digest was accepted")
	}
}
