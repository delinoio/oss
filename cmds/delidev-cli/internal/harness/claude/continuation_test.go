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
	for _, name := range []string{"reading", "permission", "unaccepted", "unfinished", "no-terminal", "running", "problem", "continuing", "summary", "compaction", "task", "background", "callback", "tool", "server-tool", "active-provider", "open-tool", "callback-bytes", "missing-ledger", "manual", "pending-ledger", "empty-ledger", "queued-event", "canceled"} {
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
			case "manual":
				s.history.actions = []HistoryCompactionActionProof{{ActionID: domain.NewID()}}
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
