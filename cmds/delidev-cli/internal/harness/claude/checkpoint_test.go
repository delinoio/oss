package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func retainedCheckpointFixture(t *testing.T) (*ClosedAPISession, APIStreamConfig, []byte, CheckpointReference) {
	t.Helper()
	s, _ := continuationFixture(t)
	for _, message := range s.history.messages {
		s.current.seen[message.NativeID] = true
	}
	s.current.content.seen = map[string]bool{"\x00msg_fixture_checkpoint": true}
	closed, err := s.CloseForContinuation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, ref, err := closed.RetainCheckpoint(context.Background())
	if err != nil {
		t.Fatal("retaining original checkpoint failed", err)
	}
	cfg := s.config
	cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
	return closed, cfg, raw, ref
}

func TestCheckpointRestoresOnlyOriginalClosedEvidence(t *testing.T) {
	original, cfg, raw, ref := retainedCheckpointFixture(t)
	if !original.used {
		t.Fatal("retention did not consume the live handoff")
	}
	if _, _, err := original.RetainCheckpoint(context.Background()); err == nil {
		t.Fatal("original closed state exported twice")
	}
	for _, private := range []string{cfg.Home, cfg.Workspace, cfg.Instructions, nativeAPIFixtureToken, "Private request", "Private response"} {
		if private != "" && bytes.Contains(raw, []byte(private)) {
			t.Fatal("checkpoint retained private content")
		}
	}
	restored, err := RestoreCheckpoint(context.Background(), cfg, raw, ref)
	if err != nil {
		t.Fatal(err)
	}
	clear(raw)
	p := restored.previous
	if !p.closed.Load() || p.stream != nil || p.config.API != (APIConfig{}) || p.current.input != ref.InputID || p.current.turnID != ref.NativeTurnID || !p.current.terminal.Successful() || !p.current.content.seen["\x00msg_fixture_checkpoint"] {
		t.Fatal("restoration launched native work or lost original identities")
	}
	next, err := ContinueAPISession(context.Background(), restored, domain.NewID(), APIConfig{ServerOrigin: cfg.API.ServerOrigin, Token: nativeAPIFixtureToken}, ContinueSuccessfulRun)
	if err != nil {
		t.Fatal("restored exact fixture could not replace its process", err)
	}
	defer next.Close()
	if _, err := next.SendInput(context.Background(), ref.InputID, "Never replay", ContinueSuccessfulRun); err == nil {
		t.Fatal("restored checkpoint forgot an accepted input")
	}
	if _, err := ContinueAPISession(context.Background(), restored, domain.NewID(), APIConfig{ServerOrigin: cfg.API.ServerOrigin, Token: nativeContinuationToken(23)}, ContinueSuccessfulRun); err == nil {
		t.Fatal("one restored capability launched two replacements")
	}
}

func TestCheckpointRejectsMissingOriginalReferenceOrChangedConfiguration(t *testing.T) {
	for _, name := range []string{"digest", "session", "owner", "input", "input-digest", "turn", "failure", "model", "effort", "permission", "workspace", "home", "instructions", "executable", "directory", "runtime", "origin", "missing-history", "changed-history", "canceled"} {
		t.Run(name, func(t *testing.T) {
			_, cfg, raw, ref := retainedCheckpointFixture(t)
			ctx := context.Background()
			switch name {
			case "digest":
				ref.SHA256 = string(bytes.Repeat([]byte{'0'}, 64))
			case "session":
				ref.SessionID = domain.NewID()
			case "owner":
				ref.OwnerID = domain.NewID()
			case "input":
				ref.InputID = domain.NewID()
			case "input-digest":
				ref.InputSHA256 = string(bytes.Repeat([]byte{'0'}, 64))
			case "turn":
				ref.NativeTurnID = string(domain.NewID())
			case "failure":
				ref.RequiresResume = true
			case "model":
				cfg.Model = "different-model"
			case "effort":
				cfg.Effort = LowEffort
			case "permission":
				cfg.Permission = BypassPermission
			case "workspace":
				cfg.Workspace = filepath.Dir(cfg.Workspace)
			case "home":
				cfg.Home = filepath.Dir(cfg.Home)
			case "instructions":
				cfg.Instructions += "Changed"
			case "executable":
				cfg.Process.Executable += "-different"
			case "directory":
				cfg.Process.Directory = filepath.Dir(cfg.Process.Directory)
			case "runtime":
				cfg.Process.Cwd = filepath.Dir(cfg.Process.Cwd)
			case "origin":
				cfg.API.ServerOrigin = "http://127.0.0.1:1"
			case "missing-history":
				if err := os.Remove(filepath.Join(cfg.Home, "projects", "delidev", string(cfg.SessionID)+".jsonl")); err != nil {
					t.Fatal(err)
				}
			case "changed-history":
				if err := os.WriteFile(filepath.Join(cfg.Home, "projects", "delidev", string(cfg.SessionID)+".jsonl"), []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if restored, err := RestoreCheckpoint(ctx, cfg, raw, ref); err == nil || restored != nil {
				t.Fatal("changed original execution granted native resume authority")
			}
		})
	}
}

func TestCheckpointRejectsCanonicalAndIdentityCorruption(t *testing.T) {
	for _, name := range []string{"duplicate-key", "unknown-key", "whitespace", "omitted-field", "version", "null-applied", "kind", "reason", "false-error", "last-action", "input-ids", "owner-ids", "native-order", "native-duplicate", "native-missing-message", "provider-parent", "authorities", "action", "resume", "summary", "large"} {
		t.Run(name, func(t *testing.T) {
			_, cfg, raw, ref := retainedCheckpointFixture(t)
			var cp sessionCheckpoint
			if err := json.Unmarshal(raw, &cp); err != nil {
				t.Fatal(err)
			}
			special := false
			switch name {
			case "duplicate-key":
				raw = append([]byte(`{"version":1,`), raw[1:]...)
				special = true
			case "unknown-key":
				raw = append([]byte(`{"future":false,`), raw[1:]...)
				special = true
			case "whitespace":
				raw = append(raw, '\n')
				special = true
			case "omitted-field":
				raw = bytes.Replace(raw, []byte(`"continuation_failed":false,`), nil, 1)
				special = true
			case "version":
				cp.Version = 2
			case "null-applied":
				raw = bytes.Replace(raw, []byte(`"model":"`+cfg.Model+`"`), []byte(`"model":null`), 1)
				special = true
			case "kind":
				cp.Kind = "unknown"
			case "reason":
				cp.Reason = "unknown"
			case "false-error":
				cp.Kind = ResultExecutionError
			case "last-action":
				cp.LastAction = CompactSucceeded
			case "input-ids":
				cp.Inputs = []domain.ID{domain.NewID()}
			case "owner-ids":
				cp.Owners = []domain.ID{domain.NewID()}
			case "native-order":
				slices.Reverse(cp.NativeIDs)
			case "native-duplicate":
				cp.NativeIDs = append(cp.NativeIDs, cp.NativeIDs[0])
				slices.Sort(cp.NativeIDs)
			case "native-missing-message":
				id := cp.Messages[len(cp.Messages)-1].NativeID
				cp.NativeIDs = slices.DeleteFunc(cp.NativeIDs, func(candidate string) bool { return candidate == id })
			case "provider-parent":
				cp.ProviderIDs = []string{"foreign\x00provider"}
			case "authorities":
				cp.Authorities = nil
			case "action":
				cp.Actions = []HistoryCompactionActionProof{{ActionID: domain.NewID()}}
			case "resume":
				cp.Resumes = []checkpointResume{{Transcript: cp.Transcript, Messages: len(cp.Messages), Action: domain.NewID()}}
			case "summary":
				cp.Compactions = []HistoryCompactionProof{{NativeID: string(domain.NewID())}}
			case "large":
				raw = bytes.Repeat([]byte{' '}, maxSessionCheckpoint+1)
				special = true
			}
			if !special {
				var err error
				raw, err = json.Marshal(cp)
				if err != nil {
					t.Fatal(err)
				}
			}
			// Even an independently pinned malformed representation cannot pass the
			// native profile/schema check. Ordinary mutation also fails its old hash.
			ref.SHA256 = checkpointDigest(raw)
			if restored, err := RestoreCheckpoint(context.Background(), cfg, raw, ref); err == nil || restored != nil {
				t.Fatal("malformed native checkpoint normalized into authority")
			}
		})
	}
}

func TestCheckpointPreservesExplicitFailureAndUsedCredentialHistory(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		s, _ := continuationFixture(t)
		for _, message := range s.history.messages {
			s.current.seen[message.NativeID] = true
		}
		if automatic {
			s.current.continuationFailed = true
		} else {
			s.current.terminal.Error = true
		}
		closed, err := s.CloseForContinuation(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		raw, ref, err := closed.RetainCheckpoint(context.Background())
		if err != nil || !ref.RequiresResume {
			t.Fatal("retention erased original failure", err)
		}
		cfg := s.config
		cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
		restored, err := RestoreCheckpoint(context.Background(), cfg, raw, ref)
		if err != nil {
			t.Fatal(err)
		}
		api := APIConfig{ServerOrigin: s.serverOrigin, Token: nativeContinuationToken(80)}
		if _, err := ContinueAPISession(context.Background(), restored, domain.NewID(), api, ResumeTerminalRun); err == nil || restored.used {
			t.Fatal("restoration forgot a used execution credential")
		}
		api.Token = nativeAPIFixtureToken
		if _, err := ContinueAPISession(context.Background(), restored, domain.NewID(), api, ContinueSuccessfulRun); err == nil || restored.used {
			t.Fatal("restored failure resumed implicitly")
		}
		next, err := ContinueAPISession(context.Background(), restored, domain.NewID(), api, ResumeTerminalRun)
		if err != nil {
			t.Fatal(err)
		}
		defer next.Close()
	}
}

func TestCheckpointRetainsLargeOriginalIdentityRegistryWithoutTruncation(t *testing.T) {
	s, _ := continuationFixture(t)
	for _, message := range s.history.messages {
		s.current.seen[message.NativeID] = true
	}
	for len(s.current.seen) < 33000 {
		s.current.seen[string(domain.NewID())] = true
	}
	closed, err := s.CloseForContinuation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, ref, err := closed.RetainCheckpoint(context.Background())
	if err != nil || len(raw) <= 1<<20 || len(raw) > maxSessionCheckpoint {
		t.Fatal("bounded private checkpoint truncated original native identities", err, len(raw))
	}
	cfg := s.config
	cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
	restored, err := RestoreCheckpoint(context.Background(), cfg, raw, ref)
	if err != nil || len(restored.previous.current.seen) != 33000 {
		t.Fatal("large private checkpoint used public JSON limits or lost identities", err)
	}
}

func TestCheckpointPreservesOriginalCanceledCommandState(t *testing.T) {
	s, _ := continuationFixture(t)
	for _, message := range s.history.messages {
		s.current.seen[message.NativeID] = true
	}
	s.current.command = CommandCancelled
	s.current.terminal.Reason = AbortedStreaming
	closed, err := s.CloseForContinuation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, ref, err := closed.RetainCheckpoint(context.Background())
	if err != nil || !ref.RequiresResume {
		t.Fatal(err)
	}
	cfg := s.config
	cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
	restored, err := RestoreCheckpoint(context.Background(), cfg, raw, ref)
	if err != nil || restored.previous.current.command != CommandCancelled || restored.previous.current.terminal.Reason != AbortedStreaming {
		t.Fatal("checkpoint fabricated a completed command", err)
	}
}
