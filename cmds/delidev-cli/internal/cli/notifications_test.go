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
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestNotificationCLIExplicitDefaultsPrecisionAndValidation(t *testing.T) {
	value, err := notificationOutput(&pb.GetNotificationPreferencesResponse{Preferences: &pb.NotificationPreferences{Revision: 9007199254740993, Interactions: true}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Preferences struct {
			Revision                string
			Interactions, Terminals *bool
		}
	}
	if json.Unmarshal(raw, &output) != nil || output.Preferences.Revision != "9007199254740993" || output.Preferences.Interactions == nil || output.Preferences.Terminals == nil || *output.Preferences.Terminals {
		t.Fatal("precision/defaults lost", string(raw))
	}
	for _, args := range [][]string{{"list", "--limit", "51"}, {"list", "--limit", "0"}, {"configure", "--revision", "1", "--interactions", "on"}, {"configure", "--revision", "0", "--interactions", "on", "--terminals", "off"}, {"claim", "--id", "invalid"}, {"report", "--id", string(domain.NewID()), "--claim-id", string(domain.NewID()), "--state", "seen"}} {
		if _, err := notificationCommand(context.Background(), client{}, options{}, args); err == nil {
			t.Fatal("invalid command reached transport", args)
		}
	}
}

func TestNotificationCLIRealServerClaimReportAndReplay(t *testing.T) {
	root, ctx := filepath.Join(t.TempDir(), "server"), context.Background()
	s, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	session, source, execution := domain.NewID(), domain.NewID(), domain.NewID()
	var inbox store.Record
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.notification-cli", nil, func(tx *store.Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Archive: domain.NotArchived, Dispatch: domain.DispatchClaimed, Recovery: domain.NoRecovery, ActiveExecutionID: execution}); err != nil {
			return nil, err
		}
		x := domain.ExecutionInteraction{ExecutionID: execution, NativeThreadID: "thread", NativeTurnID: "turn", NativeItemID: "question", NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: "request"}, Type: domain.UserQuestionInteraction, Questions: &domain.QuestionRequest{Blocking: true, Questions: []domain.Question{{ID: "choice", Text: "Original private question", Other: true}}}, Closure: domain.InteractionOpen, FirstSequence: 3, LastSequence: 3}
		if _, err := tx.Put(domain.InteractionKind, source, 0, session, "", x); err != nil {
			return nil, err
		}
		var err error
		inbox, err = tx.CreateInboxEntry(session, "", domain.InboxEntry{Source: domain.InteractionInbox, SourceID: source, ReadState: domain.InboxUnread})
		return nil, err
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
		t.Fatal("private server did not become ready")
	}
	run := func(args ...string) map[string]any {
		t.Helper()
		code, out := cliRun(t, root, append([]string{"notification"}, args...), "")
		if code != 0 {
			t.Fatal("notification command failed", args, out)
		}
		return out["result"].(map[string]any)
	}
	preferences := run("preferences")["preferences"].(map[string]any)
	if preferences["revision"] != "1" || preferences["interactions"] != true || preferences["terminals"] != false {
		t.Fatal("explicit defaults lost", preferences)
	}
	listed := run("list")
	if len(listed["candidates"].([]any)) != 1 || listed["more"] != false {
		t.Fatal("metadata list lost explicit bounds", listed)
	}
	request := string(domain.NewID())
	args := []string{"claim", "--id", string(inbox.ID), "--request-id", request}
	claimed := run(args...)
	if claimed["may_present"] != true || claimed["replayed"] != false {
		t.Fatal("fresh reservation missing", claimed)
	}
	if replay := run(args...); replay["may_present"] != false || replay["replayed"] != true {
		t.Fatal("CLI receipt repeated native authority", replay)
	}
	claim := claimed["delivery"].(map[string]any)["claim_id"].(string)
	reported := run("report", "--id", string(inbox.ID), "--claim-id", claim, "--state", "denied")
	if reported["delivery"].(map[string]any)["state"] != "NOTIFICATION_STATE_DENIED" {
		t.Fatal("denied state lost", reported)
	}
	if inspect := run("inspect", "--id", string(inbox.ID)); inspect["delivery"].(map[string]any)["claim_id"] != claim {
		t.Fatal("inspection replaced claim")
	}
	if values := run("list")["candidates"].([]any); len(values) != 0 {
		t.Fatal("reconnect queued duplicate")
	}
	configureArgs := []string{"configure", "--request-id", string(domain.NewID()), "--revision", "1", "--questions", "off", "--approvals", "off", "--succeeded", "on", "--failed", "on", "--stopped", "on"}
	configured := run(configureArgs...)["preferences"].(map[string]any)
	replayed := run(configureArgs...)
	if replayed["replayed"] != true {
		t.Fatal("exact preference retry lost receipt", replayed)
	}
	if configured["revision"] != "2" || configured["situations"].(map[string]any)["questions"] != false || configured["situations"].(map[string]any)["succeeded"] != true {
		t.Fatal("preferences not persisted", configured)
	}
}
