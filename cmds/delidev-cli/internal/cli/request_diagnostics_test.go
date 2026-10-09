package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestCLIRequestDiagnosticsExactPagesAndUnavailableFields(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "server")
	db, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	session, execution := domain.NewID(), domain.NewID()
	provider := domain.NewID()
	model := (domain.ModelIdentity{ProviderID: provider, NativeID: "original-native"}).Key()
	record := domain.RequestDiagnostic{ID: domain.NewID(), SessionID: session, ExecutionID: execution, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: provider, ModelID: model, Source: domain.DiagnosticNativeInput, Operation: domain.DiagnosticInput, State: domain.DiagnosticInProgress, Purpose: domain.ConversationUsage, Harness: domain.Codex, InputID: domain.NewID(), NativeThreadID: string(domain.NewID()), ObservedAt: time.Now().UTC()}
	record.NativeRequestID = string(record.ID)
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.diagnostics", nil, func(tx *store.Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Name: "Private conversation", Workspace: domain.GeneralChat, Archive: domain.Archived, Dispatch: domain.DispatchPaused}); err != nil {
			return nil, err
		}
		if err := tx.PutRequestDiagnostic(record, 0); err != nil {
			return nil, err
		}
		other := record
		other.ID, other.InputID = domain.NewID(), domain.NewID()
		other.NativeRequestID = string(other.ID)
		return nil, tx.PutRequestDiagnostic(other, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	running, cancel := context.WithCancel(ctx)
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- server.Serve(running, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("server readiness timeout")
	}
	read := func(args ...string) map[string]any {
		t.Helper()
		code, value := cliRun(t, root, append([]string{"session", "diagnostics", "--id", string(session)}, args...), "")
		if code != 0 {
			t.Fatal("diagnostic read failed", value)
		}
		return value["result"].(map[string]any)
	}
	page := read("--execution-id", string(execution), "--page-size", "1")
	row := page["records"].([]any)[0].(map[string]any)
	if row["session_id"] != string(session) || row["execution_id"] != string(execution) || row["revision"] != "1" || row["account_id"] != string(record.AccountID) {
		t.Fatal("CLI changed original provenance", row)
	}
	for _, absent := range []string{"duration_ms", "http_attempted", "requested_effort", "effective_effort", "effective_service_tier"} {
		if _, found := row[absent]; found {
			t.Fatal("CLI fabricated an unavailable observation", absent)
		}
	}
	token := page["next_page_token"].(string)
	next := read("--execution-id", string(execution), "--page-size", "1", "--page-token", token)
	if next["records"].([]any)[0].(map[string]any)["id"] == row["id"] {
		t.Fatal("CLI duplicated first page")
	}
	if len(read("--execution-id", string(domain.NewID()))) != 0 {
		t.Fatal("CLI crossed execution attribution")
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "Private conversation") || strings.Contains(string(raw), root) {
		t.Fatal("CLI exposed payload or path")
	}
	for _, invalid := range [][]string{{"--page-size", "101"}, {"--execution-id", "wrong"}, {"--page-size", "1", "--page-token", token}} {
		code, _ := cliRun(t, root, append([]string{"session", "diagnostics", "--id", string(session)}, invalid...), "")
		if code == 0 {
			t.Fatal("CLI accepted invalid or cross-scope selection", invalid)
		}
	}
}
