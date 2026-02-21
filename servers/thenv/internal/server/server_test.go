package server

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/pkg/thenv/api"
	"github.com/golang-jwt/jwt/v5"
)

func TestPushActivatePullRotateFlow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workspace := "ws1"
	scope := api.Scope{WorkspaceID: workspace, ProjectID: "proj", EnvironmentID: "dev"}

	dbPath := filepath.Join(t.TempDir(), "thenv.db")
	cfg := Config{
		ListenAddr:    ":0",
		DatabasePath:  dbPath,
		JWTSecret:     []byte("unit-test-secret"),
		WorkspaceKeys: map[string][]byte{workspace: deriveMasterKey("master-secret")},
		DefaultKey:    nil,
		SuperAdmins:   map[string]struct{}{"admin": {}},
		LogLevel:      slog.LevelDebug,
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	srv, err := New(ctx, cfg, logger)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	adminToken := mustToken(t, cfg.JWTSecret, "admin")
	writerToken := mustToken(t, cfg.JWTSecret, "writer")
	readerToken := mustToken(t, cfg.JWTSecret, "reader")

	setPolicyReq := connect.NewRequest(&api.SetPolicyRequest{
		Scope: scope,
		Bindings: []api.PolicyBinding{
			{Subject: "writer", Role: api.RoleWriter},
			{Subject: "reader", Role: api.RoleReader},
			{Subject: "admin", Role: api.RoleAdmin},
		},
	})
	setPolicyReq.Header().Set("Authorization", "Bearer "+adminToken)
	if _, err := srv.handleSetPolicy(ctx, setPolicyReq); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	pushReq := connect.NewRequest(&api.PushBundleVersionRequest{
		Scope: scope,
		Files: []api.BundleFilePayload{
			{FileType: api.FileTypeEnv, Content: []byte("API_KEY=secret\n")},
		},
	})
	pushReq.Header().Set("Authorization", "Bearer "+writerToken)
	pushRes, err := srv.handlePushBundleVersion(ctx, pushReq)
	if err != nil {
		t.Fatalf("push bundle version: %v", err)
	}
	if pushRes.Msg.BundleVersionID == "" {
		t.Fatal("expected bundle version id")
	}

	pullBeforeActive := connect.NewRequest(&api.PullActiveBundleRequest{Scope: scope})
	pullBeforeActive.Header().Set("Authorization", "Bearer "+readerToken)
	_, err = srv.handlePullActiveBundle(ctx, pullBeforeActive)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected not found when active pointer missing, got %v", err)
	}

	activateReq := connect.NewRequest(&api.ActivateBundleVersionRequest{Scope: scope, BundleVersionID: pushRes.Msg.BundleVersionID})
	activateReq.Header().Set("Authorization", "Bearer "+adminToken)
	if _, err := srv.handleActivateBundleVersion(ctx, activateReq); err != nil {
		t.Fatalf("activate bundle version: %v", err)
	}

	pullReq := connect.NewRequest(&api.PullActiveBundleRequest{Scope: scope})
	pullReq.Header().Set("Authorization", "Bearer "+readerToken)
	pullRes, err := srv.handlePullActiveBundle(ctx, pullReq)
	if err != nil {
		t.Fatalf("pull active bundle: %v", err)
	}
	if len(pullRes.Msg.Files) != 1 {
		t.Fatalf("expected one file, got %d", len(pullRes.Msg.Files))
	}
	if string(pullRes.Msg.Files[0].Content) != "API_KEY=secret\n" {
		t.Fatalf("unexpected pull content: %q", string(pullRes.Msg.Files[0].Content))
	}

	listReq := connect.NewRequest(&api.ListBundleVersionsRequest{Scope: scope, Limit: 10})
	listReq.Header().Set("Authorization", "Bearer "+readerToken)
	listRes, err := srv.handleListBundleVersions(ctx, listReq)
	if err != nil {
		t.Fatalf("list bundle versions: %v", err)
	}
	if len(listRes.Msg.Versions) != 1 {
		t.Fatalf("expected one version in list, got %d", len(listRes.Msg.Versions))
	}

	rotateReq := connect.NewRequest(&api.RotateBundleVersionRequest{Scope: scope})
	rotateReq.Header().Set("Authorization", "Bearer "+writerToken)
	rotateRes, err := srv.handleRotateBundleVersion(ctx, rotateReq)
	if err != nil {
		t.Fatalf("rotate bundle version: %v", err)
	}
	if rotateRes.Msg.BundleVersionID == pushRes.Msg.BundleVersionID {
		t.Fatal("rotate should create a new version id")
	}

	row := srv.db.QueryRowContext(ctx, `SELECT ciphertext FROM bundle_files WHERE bundle_version_id = ? AND file_type = ?`, pushRes.Msg.BundleVersionID, int(api.FileTypeEnv))
	var ciphertext []byte
	if err := row.Scan(&ciphertext); err != nil {
		t.Fatalf("scan ciphertext: %v", err)
	}
	if string(ciphertext) == "API_KEY=secret\n" {
		t.Fatal("ciphertext must not match plaintext")
	}
}

func TestReaderCannotPush(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scope := api.Scope{WorkspaceID: "ws2", ProjectID: "proj", EnvironmentID: "dev"}
	dbPath := filepath.Join(t.TempDir(), "thenv.db")
	cfg := Config{
		ListenAddr:    ":0",
		DatabasePath:  dbPath,
		JWTSecret:     []byte("unit-test-secret"),
		WorkspaceKeys: map[string][]byte{"ws2": deriveMasterKey("master-secret")},
		SuperAdmins:   map[string]struct{}{"admin": {}},
		LogLevel:      slog.LevelDebug,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	srv, err := New(ctx, cfg, logger)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	setPolicyReq := connect.NewRequest(&api.SetPolicyRequest{
		Scope: scope,
		Bindings: []api.PolicyBinding{
			{Subject: "reader", Role: api.RoleReader},
		},
	})
	setPolicyReq.Header().Set("Authorization", "Bearer "+mustToken(t, cfg.JWTSecret, "admin"))
	if _, err := srv.handleSetPolicy(ctx, setPolicyReq); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	pushReq := connect.NewRequest(&api.PushBundleVersionRequest{
		Scope: scope,
		Files: []api.BundleFilePayload{{FileType: api.FileTypeEnv, Content: []byte("A=B\n")}},
	})
	pushReq.Header().Set("Authorization", "Bearer "+mustToken(t, cfg.JWTSecret, "reader"))
	_, err = srv.handlePushBundleVersion(ctx, pushReq)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected permission denied, got %v", err)
	}
}

func TestParseCursor(t *testing.T) {
	t.Parallel()
	if _, err := parseCursor("abc"); err == nil {
		t.Fatal("expected parse error")
	}
	value, err := parseCursor("12")
	if err != nil {
		t.Fatalf("parse cursor: %v", err)
	}
	if value != 12 {
		t.Fatalf("expected 12, got %d", value)
	}
}

func mustToken(t *testing.T, secret []byte, subject string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   subject,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	})
	signed, err := token.SignedString(secret)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func TestNoRoleReturnsPermissionDenied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "thenv.db")
	cfg := Config{
		ListenAddr:    ":0",
		DatabasePath:  dbPath,
		JWTSecret:     []byte("secret"),
		WorkspaceKeys: map[string][]byte{"ws": deriveMasterKey("m")},
		SuperAdmins:   map[string]struct{}{},
	}
	srv, err := New(ctx, cfg, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	_, err = srv.authorizeAtLeast(ctx, identity{Subject: "unknown"}, api.Scope{WorkspaceID: "ws", ProjectID: "p", EnvironmentID: "e"}, api.RoleReader)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected permission denied, got %v", err)
	}
}

func TestResolveActiveVersionMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE active_bundle_pointers (
		workspace_id TEXT NOT NULL,
		project_id TEXT NOT NULL,
		environment_id TEXT NOT NULL,
		bundle_version_id TEXT NOT NULL,
		updated_by TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		PRIMARY KEY(workspace_id, project_id, environment_id)
	);`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	srv := &Server{db: db}
	_, err = srv.resolveActiveVersionID(ctx, api.Scope{WorkspaceID: "w", ProjectID: "p", EnvironmentID: "e"})
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}
