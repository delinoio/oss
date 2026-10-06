package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestCLISearchRetainedConversationsAndFilters(t *testing.T) {
	root, ctx := filepath.Join(t.TempDir(), "server"), context.Background()
	s, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	session, agent, account, execution := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.cli-search", nil, func(tx *store.Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Name: "Archived General Chat", AgentID: agent, Workspace: domain.GeneralChat, Archive: domain.Archived, Outcome: domain.ExecutionSucceeded}); err != nil {
			return nil, err
		}
		input, _ := json.Marshal(domain.ExecutionJobInput{ExecutionID: execution, SessionID: session, AccountID: account})
		if _, err := tx.Put(domain.JobKind, domain.NewID(), 0, session, "", domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobQueued, Input: input}); err != nil {
			return nil, err
		}
		for range 3 {
			if _, err := tx.Put(domain.MessageKind, domain.NewID(), 0, session, "", domain.ExecutionMessage{ExecutionID: execution, Role: domain.AssistantMessage, Text: "Retained 검색 content", State: domain.MessageComplete}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	closeErr := s.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	running, cancel := context.WithCancel(ctx)
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- server.Serve(running, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	select {
	case <-ready:
	case err := <-done:
		done <- err
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("search server readiness timeout")
	}
	args := []string{"search", "--query", "검색", "--agent-id", string(agent), "--account-id", string(account), "--outcome", "succeeded", "--archive", "archived", "--limit", "1"}
	seen := map[string]bool{}
	cursor := ""
	for {
		call := append([]string{}, args...)
		if cursor != "" {
			call = append(call, "--page-token", cursor)
		}
		code, result := cliRun(t, root, call, "")
		if code != 0 {
			t.Fatal(result)
		}
		page := result["result"].(map[string]any)
		hits := page["hits"].([]any)
		if len(hits) != 1 {
			t.Fatal("lost matching CLI result")
		}
		hit := hits[0].(map[string]any)
		message := hit["message"].(map[string]any)
		id := message["id"].(string)
		if seen[id] || message["session_id"] != string(session) || hit["outcome"] != "succeeded" || hit["archive"] != "archived" {
			t.Fatal("CLI changed identity/filter/enum")
		}
		seen[id] = true
		cursor = page["next_page_token"].(string)
		if cursor == "" {
			break
		}
	}
	if len(seen) != 3 {
		t.Fatal("pagination omitted retained messages")
	}
	if code, result := cliRun(t, root, []string{"usage", "list"}, ""); code != 0 {
		t.Fatal("existing cumulative observation reads broke", result)
	}
	usageCode, usageResult := cliRun(t, root, []string{"usage", "summary", "--session-id", string(session), "--general-chat"}, "")
	if usageCode != 0 {
		t.Fatal(usageResult)
	}
	usageSummary := usageResult["result"].(map[string]any)
	if usageSummary["accepted_executions_without_response"] != float64(1) || usageSummary["actual_cost"] != "USAGE_COST_STATE_UNAVAILABLE" || usageSummary["totals"].(map[string]any)["total"].(map[string]any)["known_total"] != "" {
		t.Fatal("CLI invented complete usage or cost", usageResult)
	}
	dailyCode, dailyResult := cliRun(t, root, []string{"usage", "summary", "--granularity", "day", "--timezone", "Asia/Seoul"}, "")
	if dailyCode != 0 {
		t.Fatal("daily usage CLI failed", dailyResult)
	}
	dailySummary := dailyResult["result"].(map[string]any)
	analytics, ok := dailySummary["analytics"].(map[string]any)
	if !ok || analytics["time_zone"] != "Asia/Seoul" || len(analytics["days"].([]any)) == 0 {
		t.Fatal("daily usage CLI omitted server analytics", dailyResult)
	}
	for _, bad := range [][]string{{"usage"}, {"usage", "summary", "--from", "yesterday"}, {"usage", "summary", "--account-id", "invalid"}, {"usage", "summary", "--project-id", string(domain.NewID()), "--general-chat"}, {"usage", "summary", "--timezone", "UTC"}, {"usage", "summary", "--granularity", "day"}, {"usage", "summary", "--granularity", "hour", "--timezone", "UTC"}} {
		if code, _ := cliRun(t, root, bad, ""); code == 0 {
			t.Fatal("invalid usage scope accepted", bad)
		}
	}
	code, activity := cliRun(t, root, []string{"activity", "list", "--session-id", string(session)}, "")
	if code != 0 {
		t.Fatal(activity)
	}
	entries := activity["result"].(map[string]any)["entries"].([]any)
	if len(entries) != 1 {
		t.Fatal("CLI activity omitted accepted dispatch")
	}
	entry := entries[0].(map[string]any)
	if entry["kind"] != "execution-accepted" || entry["session_id"] != string(session) || entry["account_id"] != string(account) || entry["execution_id"] != string(execution) || entry["source_kind"] != "job" || entry["data"] != nil {
		t.Fatal("CLI activity changed metadata projection")
	}
	for _, invalid := range [][]string{{"activity"}, {"activity", "list", "--limit", "0"}, {"activity", "list", "--limit", "201"}, {"activity", "list", "--session-id", "bad"}} {
		if code, value := cliRun(t, root, invalid, ""); code == 0 || value["error"] == nil {
			t.Fatal("invalid activity command accepted", invalid)
		}
	}
	for _, invalid := range [][]string{{"search"}, {"search", "--query", "x", "--limit", "0"}, {"search", "--query", "x", "--limit", "201"}, {"search", "--query", "x", "--outcome", "complete"}, {"search", "--query", "x", "--archive", "trash"}, {"search", "--query", "x", "--agent-id", "bad"}} {
		if code, value := cliRun(t, root, invalid, ""); code == 0 || value["error"] == nil {
			t.Fatal("invalid CLI search accepted", invalid)
		}
	}
}
