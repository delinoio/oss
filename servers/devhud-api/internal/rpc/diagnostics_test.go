package rpc

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	testClientCorrelation = "0198c8b0-77d6-7d4a-a7d9-e4d7b11c4400"
	testCrashReportID     = "0198c8b0-77d6-7d4a-a7d9-e4d7b11c4401"
)

func TestSubmitCrashReportPersistsPreviewedContractAndReturnsCorrelations(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	var persisted domain.CrashReport
	repository := &serviceRepository{submitCrashReport: func(_ context.Context, userID string, report domain.CrashReport) (domain.CrashReport, error) {
		if userID != "018f7c1e-7b4a-7abc-8def-0123456789ab" {
			t.Fatalf("user ID = %q", userID)
		}
		persisted = report
		report.ID = testCrashReportID
		return report, nil
	}}
	response, err := NewDiagnosticsService(repository, serviceClock{}, logger).SubmitCrashReport(
		authenticatedContext(), connect.NewRequest(validCrashReportRequest()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.Msg.GetMetadata().GetCorrelationId().GetValue() != testCorrelationID || response.Msg.GetCrashReportId().GetValue() != testCrashReportID {
		t.Fatalf("unexpected response correlations: %v", response.Msg)
	}
	if persisted.ClientCorrelationID != testClientCorrelation || persisted.RequestCorrelationID != testCorrelationID || len(persisted.PayloadSHA256) != 32 {
		t.Fatalf("unexpected persisted correlations: %+v", persisted)
	}
	if persisted.ExpiresAt.Sub(persisted.AcceptedAt) != 30*24*time.Hour || persisted.DurationMilliseconds != 1250 {
		t.Fatalf("unexpected retention/duration: %+v", persisted)
	}
	if !strings.Contains(logs.String(), testCorrelationID) || !strings.Contains(logs.String(), testClientCorrelation) || strings.Contains(logs.String(), "018f7c1e-7b4a-7abc-8def-0123456789ab") {
		t.Fatalf("diagnostics log correlations or identity boundary are invalid: %s", logs.String())
	}
}

func TestSubmitCrashReportRequiresAuthenticationAndMapsBlockedUsers(t *testing.T) {
	service := NewDiagnosticsService(&serviceRepository{}, serviceClock{}, testServiceLogger())
	if _, err := service.SubmitCrashReport(WithCorrelationID(context.Background(), testCorrelationID), connect.NewRequest(validCrashReportRequest())); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("guest code = %v", connect.CodeOf(err))
	}
	service = NewDiagnosticsService(&serviceRepository{submitCrashReport: func(context.Context, string, domain.CrashReport) (domain.CrashReport, error) {
		return domain.CrashReport{}, &domain.PermissionError{Failure: domain.PermissionFailureAdministrativeBlock}
	}}, serviceClock{}, testServiceLogger())
	if _, err := service.SubmitCrashReport(authenticatedContext(), connect.NewRequest(validCrashReportRequest())); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("blocked code = %v", connect.CodeOf(err))
	}
}

func TestValidateCrashReportRejectsHostileDiagnosticContent(t *testing.T) {
	for name, hostile := range map[string]string{
		"authorization":        "Authorization: Bearer abc",
		"pat":                  "github_pat=secret",
		"raw classic pat":      "ghp_0123456789abcdefghijklmnopqrstuvwxyz",
		"raw fine-grained pat": "github_pat_0123456789abcdefghijklmnopqrstuvwxyz",
		"r2":                   "r2_secret=value",
		"signing":              "signing_key=value",
		"dom":                  "innerHTML=<form>",
		"screenshot":           "screenshot bytes",
		"fragment":             "https://example.test/path#private",
		"form":                 "form_value=private",
		"issue":                "issue_body=private",
		"agent":                "agent_prompt=private",
		"environment":          "child_env=private",
		"unix path":            "/home/alice/project/main.ts",
		"workspace path":       "/workspace/project/main.ts",
		"root path":            "/root/project/main.ts",
		"usr path":             "/usr/src/project/main.ts",
		"home path":            "~/project/main.ts",
		"windows path":         `C:\\Users\\alice\\project\\main.ts`,
		"windows slash path":   `C:/Users/alice/project/main.ts`,
		"unc path":             `\\\\server\\share\\project\\main.ts`,
		"shortcut":             "Ctrl+Shift+P",
	} {
		t.Run(name, func(t *testing.T) {
			request := validCrashReportRequest()
			request.RedactedSummary = hostile
			if err := validateCrashReport(request); err == nil {
				t.Fatal("hostile content was accepted")
			}
		})
	}
}

func TestValidateCrashReportAcceptsSafeSlashLabelsAndRemoteURLs(t *testing.T) {
	for _, safe := range []string{
		"React/Native renderer failed.",
		"iOS/18.6 runtime classification.",
		"https://example.test/assets/app.js:10:2",
	} {
		request := validCrashReportRequest()
		request.RedactedSummary = safe
		if err := validateCrashReport(request); err != nil {
			t.Fatalf("safe diagnostic %q was rejected: %v", safe, err)
		}
	}
}

func TestValidateCrashReportAcceptsTruthfulBrowserBuilds(t *testing.T) {
	request := validCrashReportRequest()
	request.ClientBuild.Platform = devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_BROWSER
	request.ClientBuild.OsVersion = "browser"
	request.ClientBuild.TauriRevision = ""
	request.ClientBuild.CefRevision = ""
	if err := validateCrashReport(request); err != nil {
		t.Fatalf("browser build was rejected: %v", err)
	}

	request.ClientBuild.TauriRevision = "4af26a3f7f8b692d62cca549bbacd93f5ce90b41"
	if err := validateCrashReport(request); err == nil {
		t.Fatal("browser build with a fabricated Tauri revision was accepted")
	}
}

func TestValidateCrashReportBoundsStackAndClassifications(t *testing.T) {
	request := validCrashReportRequest()
	request.ErrorCode = "not an enum"
	if err := validateCrashReport(request); err == nil {
		t.Fatal("free-form error code was accepted")
	}
	request = validCrashReportRequest()
	request.RedactedStackTrace = string(make([]byte, maximumStackLineBytes+1))
	if err := validateCrashReport(request); err == nil {
		t.Fatal("oversized stack line was accepted")
	}
}

func validCrashReportRequest() *devhudv1.SubmitCrashReportRequest {
	return &devhudv1.SubmitCrashReportRequest{
		ReportSchemaVersion: 1,
		ClientBuild: &devhudv1.ClientBuild{
			AppVersion: "1.0.0", BuildId: "2026.08.17.1",
			Platform:     devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_MACOS,
			Architecture: devhudv1.DiagnosticArchitecture_DIAGNOSTIC_ARCHITECTURE_ARM64,
			OsVersion:    "15.6", TauriRevision: "4af26a3f7f8b692d62cca549bbacd93f5ce90b41",
			CefRevision: "150.0.10+g8042e43+chromium-150.0.7871.101",
		},
		OccurredAt:           timestamppb.New(time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)),
		Component:            devhudv1.DiagnosticComponent_DIAGNOSTIC_COMPONENT_APP,
		Severity:             devhudv1.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_ERROR,
		ErrorCode:            "APP_UNEXPECTED_ERROR",
		RedactedSummary:      "The application operation failed.",
		RedactedStackTrace:   "render@<application>:1:1",
		ClientCorrelationId:  &devhudv1.UuidV7{Value: testClientCorrelation},
		DurationMilliseconds: 1250,
	}
}
