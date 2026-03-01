package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/delinoio/oss/cmds/thenv/internal/contracts"
	thenvv1 "github.com/delinoio/oss/servers/thenv/gen/proto/thenv/v1"
	thenvv1connect "github.com/delinoio/oss/servers/thenv/gen/proto/thenv/v1/thenvv1connect"
)

func TestHasConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")

	conflict, err := hasConflict(path, []byte("A=1\n"))
	if err != nil {
		t.Fatalf("hasConflict returned error: %v", err)
	}
	if conflict {
		t.Fatal("expected no conflict for missing file")
	}

	if err := os.WriteFile(path, []byte("A=1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	conflict, err = hasConflict(path, []byte("A=1\n"))
	if err != nil {
		t.Fatalf("hasConflict returned error: %v", err)
	}
	if conflict {
		t.Fatal("expected no conflict for equal content")
	}

	conflict, err = hasConflict(path, []byte("A=2\n"))
	if err != nil {
		t.Fatalf("hasConflict returned error: %v", err)
	}
	if !conflict {
		t.Fatal("expected conflict for different content")
	}
}

func TestResolveOutputPath(t *testing.T) {
	envPath, err := resolveOutputPath(thenvv1.FileType_FILE_TYPE_ENV, "./.env", "./.dev.vars")
	if err != nil {
		t.Fatalf("resolveOutputPath returned error: %v", err)
	}
	if envPath == "" {
		t.Fatal("expected env path")
	}

	if _, err := resolveOutputPath(thenvv1.FileType_FILE_TYPE_UNSPECIFIED, "./.env", "./.dev.vars"); err == nil {
		t.Fatal("expected error for unsupported file type")
	}
}

func TestApplyAuthHeadersSetsSubjectHeader(t *testing.T) {
	req := connect.NewRequest(&thenvv1.ListBundleVersionsRequest{})
	correlation := applyAuthHeaders(req, "token-123", "subject-abc")

	if got := req.Header().Get("Authorization"); got != "Bearer token-123" {
		t.Fatalf("unexpected authorization header: got=%q", got)
	}
	if got := req.Header().Get("X-Thenv-Subject"); got != "subject-abc" {
		t.Fatalf("unexpected subject header: got=%q", got)
	}
	if req.Header().Get("X-Request-Id") == "" {
		t.Fatal("expected request id header to be set")
	}
	if req.Header().Get("X-Trace-Id") == "" {
		t.Fatal("expected trace id header to be set")
	}
	if correlation.requestID != req.Header().Get("X-Request-Id") {
		t.Fatalf("requestID mismatch: got=%q want=%q", correlation.requestID, req.Header().Get("X-Request-Id"))
	}
	if correlation.traceID != req.Header().Get("X-Trace-Id") {
		t.Fatalf("traceID mismatch: got=%q want=%q", correlation.traceID, req.Header().Get("X-Trace-Id"))
	}
}

func TestResolvedSubjectFallsBackToToken(t *testing.T) {
	flags := commonFlags{token: " token-value "}
	if got, want := flags.resolvedSubject(), "token-value"; got != want {
		t.Fatalf("resolvedSubject fallback mismatch: got=%q want=%q", got, want)
	}

	flags.subject = " explicit-subject "
	if got, want := flags.resolvedSubject(), "explicit-subject"; got != want {
		t.Fatalf("resolvedSubject explicit mismatch: got=%q want=%q", got, want)
	}
}

func TestExecutePullConflictEmitsStructuredLog(t *testing.T) {
	service := &fakeBundleService{
		pullResponse: &thenvv1.PullActiveBundleResponse{
			Version: &thenvv1.BundleVersionSummary{BundleVersionId: "bundle-1"},
			Files: []*thenvv1.BundleFile{{
				FileType:  thenvv1.FileType_FILE_TYPE_ENV,
				Plaintext: []byte("A=2\n"),
			}},
		},
	}
	server := newBundleTestServer(t, service)

	tempDir := t.TempDir()
	envOutputPath := filepath.Join(tempDir, ".env")
	if err := os.WriteFile(envOutputPath, []byte("A=1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{
		"pull",
		"--server", server,
		"--token", "token-1",
		"--workspace", "ws-1",
		"--project", "project-1",
		"--env", "env-1",
		"--output-env-file", envOutputPath,
	}, stdout, stderr)
	if code != 1 {
		t.Fatalf("expected code=1, got=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "pull conflict on") {
		t.Fatalf("expected conflict message in stderr, got=%s", stderr.String())
	}

	logEntry := extractFirstJSONLogEntry(t, stderr.String())
	assertStringField(t, logEntry, "operation", "pull")
	assertStringField(t, logEntry, "event_type", "AUDIT_EVENT_TYPE_PULL")
	assertStringField(t, logEntry, "result", "failure")
	assertStringField(t, logEntry, "role_decision", "allow")
	assertStringField(t, logEntry, "conflict_policy", "fail-closed")
	assertStringField(t, logEntry, "bundle_version_id", "bundle-1")
	assertNonEmptyStringField(t, logEntry, "request_id")
	assertNonEmptyStringField(t, logEntry, "trace_id")

	scope, ok := logEntry["scope"].(map[string]any)
	if !ok {
		t.Fatalf("expected scope object, got=%T", logEntry["scope"])
	}
	if scope["workspaceId"] != "ws-1" || scope["projectId"] != "project-1" || scope["environmentId"] != "env-1" {
		t.Fatalf("unexpected scope payload: %+v", scope)
	}
}

func TestExecutePullSuccessEmitsStructuredLog(t *testing.T) {
	service := &fakeBundleService{
		pullResponse: &thenvv1.PullActiveBundleResponse{
			Version: &thenvv1.BundleVersionSummary{BundleVersionId: "bundle-2"},
			Files: []*thenvv1.BundleFile{{
				FileType:  thenvv1.FileType_FILE_TYPE_ENV,
				Plaintext: []byte("A=2\n"),
			}},
		},
	}
	server := newBundleTestServer(t, service)

	tempDir := t.TempDir()
	envOutputPath := filepath.Join(tempDir, ".env")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{
		"pull",
		"--server", server,
		"--token", "token-1",
		"--workspace", "ws-1",
		"--project", "project-1",
		"--env", "env-1",
		"--output-env-file", envOutputPath,
	}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected code=0, got=%d stderr=%s", code, stderr.String())
	}

	payload, err := os.ReadFile(envOutputPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(payload) != "A=2\n" {
		t.Fatalf("unexpected output file content: %q", string(payload))
	}

	var output map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("failed to parse stdout JSON: %v; output=%s", err, stdout.String())
	}
	if output["bundleVersionId"] != "bundle-2" {
		t.Fatalf("unexpected bundleVersionId: %v", output["bundleVersionId"])
	}

	logEntry := extractFirstJSONLogEntry(t, stderr.String())
	assertStringField(t, logEntry, "operation", "pull")
	assertStringField(t, logEntry, "event_type", "AUDIT_EVENT_TYPE_PULL")
	assertStringField(t, logEntry, "result", "success")
	assertStringField(t, logEntry, "role_decision", "allow")
	assertStringField(t, logEntry, "conflict_policy", "fail-closed")
	assertStringField(t, logEntry, "bundle_version_id", "bundle-2")
	assertNonEmptyStringField(t, logEntry, "request_id")
	assertNonEmptyStringField(t, logEntry, "trace_id")
}

func TestOperationAuditEventType(t *testing.T) {
	testCases := []struct {
		name      string
		operation contracts.ThenvOperation
		eventType thenvv1.AuditEventType
	}{
		{name: "push", operation: contracts.ThenvOperationPush, eventType: thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH},
		{name: "pull", operation: contracts.ThenvOperationPull, eventType: thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL},
		{name: "list", operation: contracts.ThenvOperationList, eventType: thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST},
		{name: "rotate", operation: contracts.ThenvOperationRotate, eventType: thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE},
		{name: "unspecified", operation: contracts.ThenvOperation("unknown"), eventType: thenvv1.AuditEventType_AUDIT_EVENT_TYPE_UNSPECIFIED},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := operationAuditEventType(tc.operation); got != tc.eventType {
				t.Fatalf("operationAuditEventType mismatch: got=%s want=%s", got, tc.eventType)
			}
		})
	}
}

func TestRoleDecisionAndResultFromError(t *testing.T) {
	testCases := []struct {
		name         string
		err          error
		roleDecision contracts.RoleDecision
		result       contracts.OperationResult
	}{
		{
			name:         "permission denied connect error",
			err:          connect.NewError(connect.CodePermissionDenied, errors.New("rbac denied")),
			roleDecision: contracts.RoleDecisionDeny,
			result:       contracts.OperationResultDenied,
		},
		{
			name:         "unauthenticated connect error",
			err:          connect.NewError(connect.CodeUnauthenticated, errors.New("subject mismatch")),
			roleDecision: contracts.RoleDecisionDeny,
			result:       contracts.OperationResultDenied,
		},
		{
			name:         "generic error",
			err:          errors.New("network timeout"),
			roleDecision: contracts.RoleDecisionAllow,
			result:       contracts.OperationResultFailure,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			roleDecision, result := roleDecisionAndResultFromError(tc.err)
			if roleDecision != tc.roleDecision {
				t.Fatalf("roleDecision mismatch: got=%s want=%s", roleDecision, tc.roleDecision)
			}
			if result != tc.result {
				t.Fatalf("result mismatch: got=%s want=%s", result, tc.result)
			}
		})
	}
}

func extractFirstJSONLogEntry(t *testing.T, stderr string) map[string]any {
	t.Helper()

	for _, line := range strings.Split(stderr, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			return parsed
		}
	}

	t.Fatalf("failed to find JSON log line in stderr=%s", stderr)
	return nil
}

func assertStringField(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()

	got, ok := payload[key].(string)
	if !ok {
		t.Fatalf("expected string field %q, got type=%T value=%v", key, payload[key], payload[key])
	}
	if got != want {
		t.Fatalf("field %q mismatch: got=%q want=%q", key, got, want)
	}
}

func assertNonEmptyStringField(t *testing.T, payload map[string]any, key string) {
	t.Helper()

	got, ok := payload[key].(string)
	if !ok {
		t.Fatalf("expected string field %q, got type=%T value=%v", key, payload[key], payload[key])
	}
	if strings.TrimSpace(got) == "" {
		t.Fatalf("expected non-empty field %q", key)
	}
}

type fakeBundleService struct {
	pullResponse *thenvv1.PullActiveBundleResponse
	pullError    error
}

func (f *fakeBundleService) PushBundleVersion(
	_ context.Context,
	_ *connect.Request[thenvv1.PushBundleVersionRequest],
) (*connect.Response[thenvv1.PushBundleVersionResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (f *fakeBundleService) PullActiveBundle(
	_ context.Context,
	_ *connect.Request[thenvv1.PullActiveBundleRequest],
) (*connect.Response[thenvv1.PullActiveBundleResponse], error) {
	if f.pullError != nil {
		return nil, f.pullError
	}
	return connect.NewResponse(f.pullResponse), nil
}

func (f *fakeBundleService) ListBundleVersions(
	_ context.Context,
	_ *connect.Request[thenvv1.ListBundleVersionsRequest],
) (*connect.Response[thenvv1.ListBundleVersionsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (f *fakeBundleService) ActivateBundleVersion(
	_ context.Context,
	_ *connect.Request[thenvv1.ActivateBundleVersionRequest],
) (*connect.Response[thenvv1.ActivateBundleVersionResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (f *fakeBundleService) RotateBundleVersion(
	_ context.Context,
	_ *connect.Request[thenvv1.RotateBundleVersionRequest],
) (*connect.Response[thenvv1.RotateBundleVersionResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func newBundleTestServer(t *testing.T, service thenvv1connect.BundleServiceHandler) string {
	t.Helper()

	path, handler := thenvv1connect.NewBundleServiceHandler(service)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL
}
