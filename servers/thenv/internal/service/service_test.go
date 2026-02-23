package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	thenvv1 "github.com/delinoio/oss/servers/thenv/gen/proto/thenv/v1"
	thenvv1connect "github.com/delinoio/oss/servers/thenv/gen/proto/thenv/v1/thenvv1connect"
	"github.com/delinoio/oss/servers/thenv/internal/logging"
)

func TestPushPullRotateAndListFlow(t *testing.T) {
	svc, serverURL := newTestServiceAndServer(t)
	defer func() {
		_ = svc.Close()
	}()

	httpClient := &http.Client{}
	bundleClient := thenvv1connect.NewBundleServiceClient(httpClient, serverURL)

	scope := &thenvv1.Scope{WorkspaceId: "ws-1", ProjectId: "proj-1", EnvironmentId: "dev"}

	pushReq := connect.NewRequest(&thenvv1.PushBundleVersionRequest{
		Scope: scope,
		Files: []*thenvv1.BundleFile{{
			FileType:  thenvv1.FileType_FILE_TYPE_ENV,
			Plaintext: []byte("API_KEY=secret-value\n"),
		}},
	})
	setAuthHeaders(pushReq, "admin", "admin")

	pushRes, err := bundleClient.PushBundleVersion(context.Background(), pushReq)
	if err != nil {
		t.Fatalf("PushBundleVersion returned error: %v", err)
	}
	if pushRes.Msg.GetVersion().GetStatus() != thenvv1.BundleStatus_BUNDLE_STATUS_ACTIVE {
		t.Fatalf("expected first pushed version to be active, got=%s", pushRes.Msg.GetVersion().GetStatus().String())
	}

	listReq := connect.NewRequest(&thenvv1.ListBundleVersionsRequest{Scope: scope, Limit: 10})
	setAuthHeaders(listReq, "admin", "admin")
	listRes, err := bundleClient.ListBundleVersions(context.Background(), listReq)
	if err != nil {
		t.Fatalf("ListBundleVersions returned error: %v", err)
	}
	if len(listRes.Msg.GetVersions()) != 1 {
		t.Fatalf("expected one version, got=%d", len(listRes.Msg.GetVersions()))
	}

	pullReq := connect.NewRequest(&thenvv1.PullActiveBundleRequest{Scope: scope})
	setAuthHeaders(pullReq, "admin", "admin")
	pullRes, err := bundleClient.PullActiveBundle(context.Background(), pullReq)
	if err != nil {
		t.Fatalf("PullActiveBundle returned error: %v", err)
	}
	if len(pullRes.Msg.GetFiles()) != 1 {
		t.Fatalf("expected one pulled file, got=%d", len(pullRes.Msg.GetFiles()))
	}
	if got, want := string(pullRes.Msg.GetFiles()[0].GetPlaintext()), "API_KEY=secret-value\n"; got != want {
		t.Fatalf("unexpected pull payload: got=%q want=%q", got, want)
	}

	rotateReq := connect.NewRequest(&thenvv1.RotateBundleVersionRequest{Scope: scope})
	setAuthHeaders(rotateReq, "admin", "admin")
	rotateRes, err := bundleClient.RotateBundleVersion(context.Background(), rotateReq)
	if err != nil {
		t.Fatalf("RotateBundleVersion returned error: %v", err)
	}
	if rotateRes.Msg.GetVersion().GetBundleVersionId() == "" {
		t.Fatal("expected rotate to return new version id")
	}
	if rotateRes.Msg.GetVersion().GetStatus() != thenvv1.BundleStatus_BUNDLE_STATUS_ACTIVE {
		t.Fatalf("expected rotated version to be active, got=%s", rotateRes.Msg.GetVersion().GetStatus().String())
	}

	listRes, err = bundleClient.ListBundleVersions(context.Background(), listReq)
	if err != nil {
		t.Fatalf("ListBundleVersions after rotate returned error: %v", err)
	}
	if len(listRes.Msg.GetVersions()) != 2 {
		t.Fatalf("expected two versions after rotate, got=%d", len(listRes.Msg.GetVersions()))
	}
}

func TestRolePolicyEnforcement(t *testing.T) {
	svc, serverURL := newTestServiceAndServer(t)
	defer func() {
		_ = svc.Close()
	}()

	httpClient := &http.Client{}
	bundleClient := thenvv1connect.NewBundleServiceClient(httpClient, serverURL)
	policyClient := thenvv1connect.NewPolicyServiceClient(httpClient, serverURL)

	scope := &thenvv1.Scope{WorkspaceId: "ws-2", ProjectId: "proj-2", EnvironmentId: "staging"}

	setPolicyReq := connect.NewRequest(&thenvv1.SetPolicyRequest{
		Scope: scope,
		Bindings: []*thenvv1.PolicyBinding{
			{Subject: "admin", Role: thenvv1.Role_ROLE_ADMIN},
			{Subject: "reader", Role: thenvv1.Role_ROLE_READER},
		},
	})
	setAuthHeaders(setPolicyReq, "admin", "admin")
	if _, err := policyClient.SetPolicy(context.Background(), setPolicyReq); err != nil {
		t.Fatalf("SetPolicy returned error: %v", err)
	}

	pushReq := connect.NewRequest(&thenvv1.PushBundleVersionRequest{
		Scope: scope,
		Files: []*thenvv1.BundleFile{{
			FileType:  thenvv1.FileType_FILE_TYPE_ENV,
			Plaintext: []byte("X=1\n"),
		}},
	})
	setAuthHeaders(pushReq, "admin", "admin")
	if _, err := bundleClient.PushBundleVersion(context.Background(), pushReq); err != nil {
		t.Fatalf("admin push returned error: %v", err)
	}

	readerPushReq := connect.NewRequest(pushReq.Msg)
	setAuthHeaders(readerPushReq, "reader", "reader")
	if _, err := bundleClient.PushBundleVersion(context.Background(), readerPushReq); err == nil {
		t.Fatal("expected reader push to fail")
	}

	readerPullReq := connect.NewRequest(&thenvv1.PullActiveBundleRequest{Scope: scope})
	setAuthHeaders(readerPullReq, "reader", "reader")
	if _, err := bundleClient.PullActiveBundle(context.Background(), readerPullReq); err != nil {
		t.Fatalf("reader pull returned error: %v", err)
	}
}

func TestPayloadStoredEncrypted(t *testing.T) {
	svc, serverURL := newTestServiceAndServer(t)
	defer func() {
		_ = svc.Close()
	}()

	httpClient := &http.Client{}
	bundleClient := thenvv1connect.NewBundleServiceClient(httpClient, serverURL)
	scope := &thenvv1.Scope{WorkspaceId: "ws-enc", ProjectId: "proj-enc", EnvironmentId: "prod"}

	plaintext := []byte("PASSWORD=plain-text-should-not-be-stored\n")
	pushReq := connect.NewRequest(&thenvv1.PushBundleVersionRequest{
		Scope: scope,
		Files: []*thenvv1.BundleFile{{
			FileType:  thenvv1.FileType_FILE_TYPE_ENV,
			Plaintext: plaintext,
		}},
	})
	setAuthHeaders(pushReq, "admin", "admin")
	pushRes, err := bundleClient.PushBundleVersion(context.Background(), pushReq)
	if err != nil {
		t.Fatalf("PushBundleVersion returned error: %v", err)
	}

	var ciphertext []byte
	err = svc.db.QueryRowContext(
		context.Background(),
		`SELECT ciphertext FROM bundle_file_payloads WHERE bundle_version_id = ? AND file_type = ?`,
		pushRes.Msg.GetVersion().GetBundleVersionId(),
		int32(thenvv1.FileType_FILE_TYPE_ENV),
	).Scan(&ciphertext)
	if err != nil {
		t.Fatalf("query ciphertext returned error: %v", err)
	}

	if bytes.Contains(ciphertext, plaintext) {
		t.Fatal("ciphertext contains plaintext bytes")
	}
}

func TestOpaqueBearerWithoutSubjectIsRejected(t *testing.T) {
	svc, serverURL := newTestServiceAndServer(t)
	defer func() {
		_ = svc.Close()
	}()

	httpClient := &http.Client{}
	bundleClient := thenvv1connect.NewBundleServiceClient(httpClient, serverURL)
	scope := &thenvv1.Scope{WorkspaceId: "ws-auth-1", ProjectId: "proj-auth-1", EnvironmentId: "dev"}

	pushReq := connect.NewRequest(&thenvv1.PushBundleVersionRequest{
		Scope: scope,
		Files: []*thenvv1.BundleFile{{
			FileType:  thenvv1.FileType_FILE_TYPE_ENV,
			Plaintext: []byte("TOKEN_CHECK=1\n"),
		}},
	})
	pushReq.Header().Set("Authorization", "Bearer opaque-token-without-subject")

	_, err := bundleClient.PushBundleVersion(context.Background(), pushReq)
	if err == nil {
		t.Fatal("expected opaque bearer token without subject to be rejected")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect error, got=%v", err)
	}
	if connectErr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("expected unauthenticated error, got=%s", connectErr.Code())
	}
}

func TestJWTWithoutSubjectIsRejected(t *testing.T) {
	svc, serverURL := newTestServiceAndServer(t)
	defer func() {
		_ = svc.Close()
	}()

	httpClient := &http.Client{}
	bundleClient := thenvv1connect.NewBundleServiceClient(httpClient, serverURL)
	scope := &thenvv1.Scope{WorkspaceId: "ws-auth-2", ProjectId: "proj-auth-2", EnvironmentId: "dev"}

	pushReq := connect.NewRequest(&thenvv1.PushBundleVersionRequest{
		Scope: scope,
		Files: []*thenvv1.BundleFile{{
			FileType:  thenvv1.FileType_FILE_TYPE_ENV,
			Plaintext: []byte("JWT_SUBJECT=1\n"),
		}},
	})
	pushReq.Header().Set("Authorization", "Bearer eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJzdWIiOiJhZG1pbiJ9.signature")

	_, err := bundleClient.PushBundleVersion(context.Background(), pushReq)
	if err == nil {
		t.Fatal("expected JWT without explicit subject header to be rejected")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect error, got=%v", err)
	}
	if connectErr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("expected unauthenticated error, got=%s", connectErr.Code())
	}
}

func TestLegacySubjectEqualToTokenIsHashedInAuditAndLogs(t *testing.T) {
	token := "opaque-token-value-for-hash-assertion"
	logBuffer := &bytes.Buffer{}
	svc, serverURL := newTestServiceAndServerWithBootstrapAndLogger(t, token, logging.NewWithWriter(logBuffer))
	defer func() {
		_ = svc.Close()
	}()

	httpClient := &http.Client{}
	bundleClient := thenvv1connect.NewBundleServiceClient(httpClient, serverURL)
	scope := &thenvv1.Scope{WorkspaceId: "ws-auth-3", ProjectId: "proj-auth-3", EnvironmentId: "dev"}

	pushReq := connect.NewRequest(&thenvv1.PushBundleVersionRequest{
		Scope: scope,
		Files: []*thenvv1.BundleFile{{
			FileType:  thenvv1.FileType_FILE_TYPE_ENV,
			Plaintext: []byte("HASHED_ACTOR=1\n"),
		}},
	})
	setAuthHeaders(pushReq, token, token)
	pushRes, err := bundleClient.PushBundleVersion(context.Background(), pushReq)
	if err != nil {
		t.Fatalf("PushBundleVersion returned error: %v", err)
	}

	expectedActor := hashLegacyTokenActor(token)
	var createdBy string
	err = svc.db.QueryRowContext(
		context.Background(),
		`SELECT created_by FROM bundle_versions WHERE bundle_version_id = ?`,
		pushRes.Msg.GetVersion().GetBundleVersionId(),
	).Scan(&createdBy)
	if err != nil {
		t.Fatalf("query created_by returned error: %v", err)
	}
	if createdBy != expectedActor {
		t.Fatalf("expected created_by=%q, got=%q", expectedActor, createdBy)
	}

	var actor string
	err = svc.db.QueryRowContext(
		context.Background(),
		`SELECT actor FROM audit_events ORDER BY created_at_unix_ns DESC, event_id DESC LIMIT 1`,
	).Scan(&actor)
	if err != nil {
		t.Fatalf("query audit actor returned error: %v", err)
	}
	if actor != expectedActor {
		t.Fatalf("expected audit actor=%q, got=%q", expectedActor, actor)
	}
	logOutput := logBuffer.String()
	if strings.Contains(logOutput, token) {
		t.Fatal("log output contains raw bearer token")
	}
}

func newTestServiceAndServer(t *testing.T) (*Service, string) {
	return newTestServiceAndServerWithBootstrapAndLogger(t, "admin", logging.New())
}

func newTestServiceAndServerWithBootstrapAndLogger(t *testing.T, bootstrapAdminSubject string, logger *logging.Logger) (*Service, string) {
	t.Helper()

	masterKey := bytes.Repeat([]byte{0x42}, 32)
	masterKeyB64 := base64.StdEncoding.EncodeToString(masterKey)
	dbPath := filepath.Join(t.TempDir(), "thenv.sqlite3")

	svc, err := New(context.Background(), Config{
		DBPath:                dbPath,
		MasterKeyBase64:       masterKeyB64,
		BootstrapAdminSubject: bootstrapAdminSubject,
	}, logger)
	if err != nil {
		t.Fatalf("service.New returned error: %v", err)
	}

	mux := http.NewServeMux()
	bundlePath, bundleHandler := thenvv1connect.NewBundleServiceHandler(svc)
	policyPath, policyHandler := thenvv1connect.NewPolicyServiceHandler(svc)
	auditPath, auditHandler := thenvv1connect.NewAuditServiceHandler(svc)
	mux.Handle(bundlePath, bundleHandler)
	mux.Handle(policyPath, policyHandler)
	mux.Handle(auditPath, auditHandler)

	ts := httptest.NewServer(mux)
	t.Cleanup(func() {
		ts.Close()
	})

	return svc, ts.URL
}

func setAuthHeaders[T any](req *connect.Request[T], token string, subject string) {
	req.Header().Set("Authorization", "Bearer "+strings.TrimSpace(token))
	trimmedSubject := strings.TrimSpace(subject)
	if trimmedSubject != "" {
		req.Header().Set("X-Thenv-Subject", trimmedSubject)
	}
}

func mustOpenSQLite(t *testing.T, dbPath string) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open returned error: %v", err)
	}
	return db
}
