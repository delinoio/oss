package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func continuationFixture(t *testing.T) (*APISession, *sessionFixtureTransport) {
	t.Helper()
	s, f := sessionFixture(t)
	_, _, records, _ := historyFixture(t)
	records[1]["uuid"] = string(s.current.input)
	records[2]["parentUuid"] = string(s.current.input)
	s.history = &sessionHistory{}
	for _, record := range records {
		record["sessionId"] = s.config.SessionID
		if record["cwd"] != nil {
			record["cwd"] = s.config.Workspace
		}
		if record["type"] == "user" || record["type"] == "assistant" {
			raw, _ := json.Marshal(map[string]any{"type": record["type"], "uuid": record["uuid"], "session_id": s.config.SessionID, "parent_tool_use_id": nil, "message": record["message"]})
			if err := s.history.observe(LifecycleObservation{SessionID: s.config.SessionID, Native: &StreamEvent{Kind: NativeMessage, Type: record["type"].(string), Body: raw}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	path := filepath.Join(s.config.Home, "projects", "delidev", string(s.config.SessionID)+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, historyJSONL(t, records), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(s.config.Home), "instructions.txt"), []byte(s.config.Instructions), 0600); err != nil {
		t.Fatal(err)
	}
	s.authorities = map[[sha256.Size]byte]bool{sha256.Sum256([]byte(nativeContinuationToken(80))): true}
	s.owners = map[domain.ID]bool{s.config.Process.OwnerID: true}
	s.serverOrigin = s.config.API.ServerOrigin
	s.config.API = APIConfig{}
	s.config.Process.Env = retainedLookupEnvironment(s.config.Process.Env)
	s.config.Process.Args = nil
	return s, f
}

func TestClosedContinuationKeepsOriginalHistoryAndConsumesOneNativeLaunch(t *testing.T) {
	s, f := continuationFixture(t)
	closed, err := s.CloseForContinuation(context.Background())
	if err != nil || !s.closed.Load() || !f.closed.Load() || closed.transcript.MatchedMessages != 2 {
		t.Fatal("closed handoff lost cleanup or original history", err)
	}
	if _, err := s.CloseForContinuation(context.Background()); err == nil {
		t.Fatal("closed session created two handoffs")
	}
	owner := domain.NewID()
	next, err := ContinueAPISession(context.Background(), closed, owner, APIConfig{ServerOrigin: s.serverOrigin, Token: nativeAPIFixtureToken}, ContinueSuccessfulRun)
	if err != nil {
		t.Fatal("fixture native replacement failed", err)
	}
	defer next.Close()
	if next.config.Process.OwnerID != owner || next.config.SessionID != s.config.SessionID || next.config.API != (APIConfig{}) || next.current.input != s.current.input || next.history != s.history || !next.inputs[s.current.input] || !next.current.terminal.Successful() {
		t.Fatal("replacement erased original identity/configuration")
	}
	if _, err := ContinueAPISession(context.Background(), closed, domain.NewID(), APIConfig{ServerOrigin: s.serverOrigin, Token: nativeContinuationToken(81)}, ContinueSuccessfulRun); err == nil {
		t.Fatal("handoff launched twice")
	}
	if _, err := next.SendInput(context.Background(), s.current.input, "Never replay", ContinueSuccessfulRun); err == nil {
		t.Fatal("replacement accepted original input ID again")
	}
	raw, _ := json.Marshal(closed)
	if string(raw) != "{}" {
		t.Fatal("handoff exposed retained private data")
	}
	for _, entry := range next.config.Process.Env {
		if strings.Contains(entry, "foreign-") || strings.Contains(entry, nativeAPIFixtureToken) {
			t.Fatal("lookup environment retained caller secrets")
		}
	}
}

func TestClosedContinuationRefusesIneligibleNativeBoundariesBeforeCleanup(t *testing.T) {
	for _, name := range []string{"reading", "permission", "unaccepted", "unfinished", "no-terminal", "running", "problem", "continuing", "summary", "compaction", "task", "background", "callback", "tool", "server-tool", "active-provider", "open-tool", "callback-bytes", "missing-ledger", "pending-ledger", "empty-ledger", "queued-event", "canceled"} {
		t.Run(name, func(t *testing.T) {
			s, f := continuationFixture(t)
			ctx := context.Background()
			switch name {
			case "reading":
				s.reading = true
			case "permission":
				s.permissionChanged = true
			case "unaccepted":
				s.current.accepted = false
			case "unfinished":
				s.current.finished = false
			case "no-terminal":
				s.current.terminal = nil
			case "running":
				s.current.runState = RunRunning
			case "problem":
				s.current.problem = lifecycleUncertain()
			case "continuing":
				s.current.continuing = true
			case "summary":
				s.current.pendingCompaction = &compactionSummaryBinding{}
			case "compaction":
				s.compaction = &manualCompactionBinding{}
			case "task":
				s.current.tasks = map[string]nativeTaskState{"child": {status: TaskCompleted}}
			case "background":
				s.current.backgroundTasks = map[string]bool{"child": true}
			case "callback":
				s.current.interactions = map[domain.ID]*interactionState{domain.NewID(): {echoed: true}}
			case "tool":
				s.current.content.tools = map[string]nativeToolState{"tool": {}}
			case "server-tool":
				s.current.content.serverTools = map[string]serverToolState{"tool": {}}
			case "active-provider":
				s.current.content.active = map[string]*providerMessageState{"provider": {}}
			case "open-tool":
				s.current.content.openTools = 1
			case "callback-bytes":
				s.current.interactionBytes = 1
			case "missing-ledger":
				s.history = nil
			case "pending-ledger":
				s.history.action = domain.NewID()
			case "empty-ledger":
				s.history.messages = nil
			case "queued-event":
				f.barrier = sessionBusy()
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if closed, err := s.CloseForContinuation(ctx); err == nil || closed != nil || f.closed.Load() {
				t.Fatal("ineligible handoff changed native ownership")
			}
		})
	}
}

func TestClosedContinuationPreservesFailureUntilExplicitResume(t *testing.T) {
	for _, kind := range []string{"input", "automatic"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := continuationFixture(t)
			switch kind {
			case "input":
				s.current.terminal.Error = true
			case "automatic":
				s.current.continuationFailed = true
			}
			closed, err := s.CloseForContinuation(context.Background())
			if err != nil || !closed.requiresResume {
				t.Fatal("native failure lost at closure", err)
			}
			api := APIConfig{ServerOrigin: s.serverOrigin, Token: nativeAPIFixtureToken}
			if _, err := ContinueAPISession(context.Background(), closed, domain.NewID(), api, ContinueSuccessfulRun); err == nil || closed.used {
				t.Fatal("failed conversation automatically resumed")
			}
			next, err := ContinueAPISession(context.Background(), closed, domain.NewID(), api, ResumeTerminalRun)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			if _, err := next.SendInput(context.Background(), domain.NewID(), "Requires explicit intent", ContinueSuccessfulRun); err == nil {
				t.Fatal("replacement cleared prior failure")
			}
		})
	}
}

func TestOriginalFailedEOFCheckpointPreservesFailureAndRequiresExplicitResume(t *testing.T) {
	for _, change := range []string{"original", "read", "bash", "question", "unfinished-callback", "permission", "missing-history", "changed-history"} {
		t.Run(change, func(t *testing.T) {
			s, transport := continuationFixture(t)
			switch change {
			case "read":
				s = readContinuationFixture(t)
			case "bash":
				s, _ = approvedBashContinuationFixture(t)
			case "question":
				s, _ = answeredQuestionContinuationFixture(t)
			}
			transport = s.stream.(*sessionFixtureTransport)
			ctx := context.Background()
			s.current.terminal.Error = true
			for _, message := range s.history.messages {
				s.current.seen[message.NativeID] = true
			}
			if result, err := s.FinishOriginalInput(ctx, s.config.Process.OwnerID, s.config.SessionID, s.current.input, s.current.turnID); err != nil || !result.Error {
				t.Fatal("failed input lost original clean EOF", err)
			}
			path := filepath.Join(s.config.Home, "projects", "delidev", string(s.config.SessionID)+".jsonl")
			switch change {
			case "unfinished-callback":
				s.current.interactions = map[domain.ID]*interactionState{domain.NewID(): {echoed: true}}
			case "permission":
				s.permissionChanged = true
			case "missing-history":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "changed-history":
				if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			closed, err := s.RetainOriginalCompletion(ctx, s.config.Process.OwnerID, s.config.SessionID, s.current.input, s.current.turnID)
			if change != "original" && change != "read" && change != "bash" && change != "question" {
				if err == nil || closed != nil {
					t.Fatal("unproved failed history acquired a checkpoint")
				}
				return
			}
			if err != nil || !closed.requiresResume {
				t.Fatal("failed EOF did not retain paused history", err)
			}
			raw, ref, err := closed.RetainCheckpoint(ctx)
			if err != nil || !ref.RequiresResume {
				t.Fatal("failed checkpoint lost explicit intent", err)
			}
			cfg := s.config
			cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
			inspection := cfg
			inspection.Instructions = ""
			if err := InspectCheckpoint(ctx, inspection, checkpointDigest([]byte(cfg.Instructions)), raw, ref); err != nil {
				t.Fatal("comparison-only failed checkpoint rejected", err)
			}
			restored, err := RestoreCheckpoint(ctx, cfg, raw, ref)
			if err != nil {
				t.Fatal(err)
			}
			api := APIConfig{ServerOrigin: s.serverOrigin, Token: nativeAPIFixtureToken}
			if _, err := ContinueAPISession(ctx, restored, domain.NewID(), api, ContinueSuccessfulRun); err == nil || restored.used {
				t.Fatal("failed checkpoint automatically started another process")
			}
			next, err := ContinueAPISession(ctx, restored, domain.NewID(), api, ResumeTerminalRun)
			if err != nil {
				t.Fatal("explicit failed Resume was blocked", err)
			}
			defer next.Close()
			if next.current.terminal.Successful() || next.current.input != ref.InputID || next.config.Process.OwnerID == ref.OwnerID {
				t.Fatal("replacement lost failure or reused execution ownership")
			}
			if _, err := next.SendInput(ctx, ref.InputID, "Never resend the failed input", ResumeTerminalRun); err == nil {
				t.Fatal("Resume resent its predecessor")
			}
			if transport.sends.Load() != 0 || transport.replies.Load() != 0 || transport.interrupts.Load() != 0 {
				t.Fatal("retention or inspection replayed native work")
			}
		})
	}
}

func TestClosedContinuationRejectsChangedAuthorityOrHistoryWithoutReplay(t *testing.T) {
	for _, name := range []string{"owner", "token", "origin", "intent", "used", "file-missing", "file-content", "file-metadata", "instructions", "settings", "runtime", "canceled"} {
		t.Run(name, func(t *testing.T) {
			s, _ := continuationFixture(t)
			closed, err := s.CloseForContinuation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			owner, api, intent, ctx := domain.NewID(), APIConfig{ServerOrigin: s.serverOrigin, Token: nativeAPIFixtureToken}, ContinueSuccessfulRun, context.Background()
			path := filepath.Join(s.config.Home, "projects", "delidev", string(s.config.SessionID)+".jsonl")
			attempt := false
			switch name {
			case "owner":
				owner = s.config.Process.OwnerID
			case "token":
				api.Token = nativeContinuationToken(80)
			case "origin":
				api.ServerOrigin = "https://other.example"
			case "intent":
				intent = "unknown"
			case "used":
				closed.used = true
			case "file-missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				attempt = true
			case "file-content":
				if err := os.WriteFile(path, []byte("changed\n"), 0600); err != nil {
					t.Fatal(err)
				}
				attempt = true
			case "file-metadata":
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				raw = bytes.Replace(raw, []byte(`"output_tokens":3`), []byte(`"output_tokens":9`), 1)
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				attempt = true
			case "instructions":
				if err := os.WriteFile(filepath.Join(filepath.Dir(s.config.Home), "instructions.txt"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
				attempt = true
			case "settings":
				s.initial.Model = "other-model"
				attempt = true
			case "runtime":
				if err := os.Remove(filepath.Join(filepath.Dir(s.config.Home), "cache")); err != nil {
					t.Fatal(err)
				}
				attempt = true
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			next, err := ContinueAPISession(ctx, closed, owner, api, intent)
			if next != nil {
				next.Close()
			}
			if err == nil || next != nil || (attempt && !closed.used) {
				t.Fatal("changed continuation created replacement authority")
			}
		})
	}
}

func TestClosedContinuationConcurrentClaimsLaunchOnce(t *testing.T) {
	s, _ := continuationFixture(t)
	closed, err := s.CloseForContinuation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan *APISession, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, _ := ContinueAPISession(context.Background(), closed, domain.NewID(), APIConfig{ServerOrigin: s.serverOrigin, Token: nativeAPIFixtureToken}, ContinueSuccessfulRun)
			results <- next
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for next := range results {
		if next != nil {
			success++
			if err := next.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if success != 1 {
		t.Fatal("handoff did not own exactly one native replacement", success)
	}
}

func TestOriginalEOFHistoryRetentionRequiresExactOriginalCleanCompletion(t *testing.T) {
	for _, scenario := range []string{"valid", "open", "forced-close", "generic-finish", "owner", "session", "input", "turn", "permission", "missing-ledger", "changed-history", "duplicate", "prior-handoff"} {
		t.Run(scenario, func(t *testing.T) {
			s, transport := continuationFixture(t)
			owner, session, input, turn := s.config.Process.OwnerID, s.config.SessionID, s.current.input, s.current.turnID
			ctx := context.Background()
			switch scenario {
			case "open":
			case "forced-close":
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			case "generic-finish":
				if err := s.Finish(ctx); err != nil {
					t.Fatal(err)
				}
			case "prior-handoff":
				if _, err := s.CloseForContinuation(ctx); err != nil {
					t.Fatal(err)
				}
			default:
				if _, err := s.FinishOriginalInput(ctx, owner, session, input, turn); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "owner":
				owner = domain.NewID()
			case "session":
				session = domain.NewID()
			case "input":
				input = domain.NewID()
			case "turn":
				turn = string(domain.NewID())
			case "permission":
				s.permissionChanged = true
			case "missing-ledger":
				s.history = nil
			case "changed-history":
				path := filepath.Join(s.config.Home, "projects", "delidev", string(session)+".jsonl")
				if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				if _, err := s.RetainOriginalCompletion(ctx, owner, session, input, turn); err != nil {
					t.Fatal(err)
				}
			}
			closed, err := s.RetainOriginalCompletion(ctx, owner, session, input, turn)
			if scenario == "valid" {
				if err != nil || closed == nil || closed.transcript.MatchedMessages != 2 || !s.originalInputEOF || !s.handoffRetained {
					t.Fatal("original EOF lost verified history", err)
				}
			} else if err == nil || closed != nil {
				t.Fatal("unproved EOF granted history authority")
			}
			if transport.sends.Load() != 0 || transport.replies.Load() != 0 || transport.interrupts.Load() != 0 {
				t.Fatal("history inspection replayed native work")
			}
			if scenario == "open" && transport.closed.Load() {
				t.Fatal("history retention closed live controller")
			}
		})
	}
}
