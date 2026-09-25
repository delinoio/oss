package cli

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestCLIInboxJoinedInspectionReadStateAndReceiptReplay(t *testing.T) {
	root, ctx := filepath.Join(t.TempDir(), "server"), context.Background()
	s, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	session, question := domain.NewID(), domain.NewID()
	var inbox store.Record
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.cli-inbox", nil, func(tx *store.Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Name: "Retained inbox fixture", Archive: domain.Archived, Outcome: domain.ExecutionStopped, Dispatch: domain.DispatchPaused}); err != nil {
			return nil, err
		}
		value := domain.ExecutionInteraction{ExecutionID: domain.NewID(), NativeThreadID: "thread", NativeTurnID: "turn", NativeItemID: "question", NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: "request"}, Type: domain.UserQuestionInteraction, Questions: &domain.QuestionRequest{Blocking: true, Questions: []domain.Question{{ID: "choice", Text: "Retained original question", Other: true}}}, Closure: domain.InteractionTurnEnded, FirstSequence: 3, LastSequence: 4}
		if _, err := tx.Put(domain.InteractionKind, question, 0, session, "", value); err != nil {
			return nil, err
		}
		var err error
		inbox, err = tx.CreateInboxEntry(session, "", domain.InboxEntry{Source: domain.InteractionInbox, SourceID: question, ReadState: domain.InboxUnread})
		return nil, err
	})
	closeErr := s.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	running, cancel := context.WithCancel(ctx)
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- server.Serve(running, server.Config{DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
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
		// The cleanup must still be able to join an already observed result.
		done <- err
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("inbox fixture server readiness timed out")
	}
	run := func(args ...string) map[string]any {
		t.Helper()
		code, value := cliRun(t, root, append([]string{"inbox"}, args...), "")
		if code != 0 {
			t.Fatalf("inbox %v: %d %v", args, code, value)
		}
		return value
	}
	listed := run("list", "--session-id", string(session), "--read-state", "unread", "--source", "interaction")
	entries := listed["result"].(map[string]any)["entries"].([]any)
	if len(entries) != 1 {
		t.Fatal("CLI omitted the archived session's retained inbox entry")
	}
	view := run("inspect", "--id", string(inbox.ID))["result"].(map[string]any)["view"].(map[string]any)
	if view["entry"].(map[string]any)["id"] != string(inbox.ID) || view["interaction"].(map[string]any)["id"] != string(question) || view["session"].(map[string]any)["data"].(map[string]any)["archive"] != "archived" {
		t.Fatal("CLI inspection lost the original question or current session visibility")
	}
	requestID := string(domain.NewID())
	args := []string{"mark-read", "--id", string(inbox.ID), "--revision", "1", "--request-id", requestID}
	marked := run(args...)
	view = marked["result"].(map[string]any)["view"].(map[string]any)
	if marked["request_id"] != requestID || view["entry"].(map[string]any)["revision"] != float64(2) || view["interaction"].(map[string]any)["revision"] != float64(1) {
		t.Fatal("CLI mark-read changed the wrong revision or lost retry identity")
	}
	if entries := run("list", "--read-state", "unread")["result"].(map[string]any)["entries"].([]any); len(entries) != 0 {
		t.Fatal("read inbox entry remained in unread selection")
	}
	unread := run("mark-unread", "--id", string(inbox.ID), "--revision", "2")
	if domain.ID(unread["request_id"].(string)).Validate() != nil {
		t.Fatal("CLI did not generate a durable read-state request identity")
	}
	replayed := run(args...)["result"].(map[string]any)
	view = replayed["view"].(map[string]any)
	entry := view["entry"].(map[string]any)
	if replayed["replayed"] != true || entry["revision"] != float64(3) || entry["data"].(map[string]any)["read_state"] != "unread" {
		t.Fatal("old CLI receipt reapplied mark-read instead of returning current state")
	}
	if view["interaction"].(map[string]any)["data"].(map[string]any)["closure"] != "turn-ended" || view["session"].(map[string]any)["data"].(map[string]any)["dispatch"] != "paused" {
		t.Fatal("inbox mutation revived a closed native request or resumed the session")
	}
	for _, invalid := range [][]string{{"mark-read", "--id", string(inbox.ID)}, {"mark-read", "--id", string(inbox.ID), "--revision", "1"}, {"list", "--read-state", "resolved"}, {"list", "--source", "permission"}, {"list", "--limit", "201"}, {"list", "--limit", "0"}, {"list", "--session-id", "invalid"}, {"get"}, {"get", "--id", string(inbox.ID), "--revision", "3"}} {
		if code, value := cliRun(t, root, append([]string{"inbox"}, invalid...), ""); code == 0 || value["error"] == nil {
			t.Fatalf("CLI accepted invalid inbox operation: %v", invalid)
		}
	}
}
