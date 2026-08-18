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

func TestSubmitCrashReportLogsSanitizedRepositoryCauses(t *testing.T) {
	tests := []struct {
		name     string
		reason   error
		category repositoryErrorCategory
		sqlState string
	}{
		{name: "constraint", reason: testSQLStateError{state: "23505"}, category: repositoryErrorPostgreSQL, sqlState: "23505"},
		{name: "transaction", reason: testSQLStateError{state: "40001"}, category: repositoryErrorPostgreSQL, sqlState: "40001"},
		{name: "connectivity", reason: testNetworkError{}, category: repositoryErrorNetwork},
		{name: "deadline", reason: context.DeadlineExceeded, category: repositoryErrorDeadline},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			repository := &serviceRepository{submitCrashReport: func(context.Context, string, domain.CrashReport) (domain.CrashReport, error) {
				return domain.CrashReport{}, test.reason
			}}

			_, err := NewDiagnosticsService(repository, serviceClock{}, logger).SubmitCrashReport(
				authenticatedContext(), connect.NewRequest(validCrashReportRequest()),
			)
			if connect.CodeOf(err) != connect.CodeInternal {
				t.Fatalf("repository failure code = %v", connect.CodeOf(err))
			}
			output := logs.String()
			if !strings.Contains(output, `"error_category":"`+string(test.category)+`"`) {
				t.Fatalf("repository failure category is missing: %s", output)
			}
			if test.sqlState != "" && !strings.Contains(output, `"sqlstate":"`+test.sqlState+`"`) {
				t.Fatalf("repository SQLSTATE is missing: %s", output)
			}
			if strings.Contains(output, "sensitive repository detail") {
				t.Fatalf("repository failure leaked error text: %s", output)
			}
		})
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
		"file URL":             "file:///home/alice/project/app.ts:10",
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

func TestSubmitCrashReportRejectsUnlabeledCredentialsBeforePersistence(t *testing.T) {
	credentials := map[string]string{
		"AWS access key": "AKIA0123456789ABCDEF",
		"JWT":            "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature",
		"private key":    "-----BEGIN PRIVATE KEY-----",
	}
	locations := map[string]func(*devhudv1.SubmitCrashReportRequest, string){
		"build": func(request *devhudv1.SubmitCrashReportRequest, credential string) {
			request.ClientBuild.OsVersion = credential
		},
		"stack": func(request *devhudv1.SubmitCrashReportRequest, credential string) {
			request.RedactedStackTrace = credential
		},
		"summary": func(request *devhudv1.SubmitCrashReportRequest, credential string) {
			request.RedactedSummary = credential
		},
	}

	for credentialName, credential := range credentials {
		for locationName, mutate := range locations {
			t.Run(credentialName+"/"+locationName, func(t *testing.T) {
				repositoryCalled := false
				repository := &serviceRepository{submitCrashReport: func(context.Context, string, domain.CrashReport) (domain.CrashReport, error) {
					repositoryCalled = true
					return domain.CrashReport{}, nil
				}}
				request := validCrashReportRequest()
				mutate(request, credential)

				_, err := NewDiagnosticsService(repository, serviceClock{}, testServiceLogger()).SubmitCrashReport(
					authenticatedContext(), connect.NewRequest(request),
				)
				if connect.CodeOf(err) != connect.CodeInvalidArgument {
					t.Fatalf("credential diagnostic code = %v", connect.CodeOf(err))
				}
				if repositoryCalled {
					t.Fatal("repository was called for a diagnostic containing an unlabeled credential")
				}
			})
		}
	}
}

func TestSubmitCrashReportRejectsNULBeforePersistence(t *testing.T) {
	repositoryCalled := false
	repository := &serviceRepository{submitCrashReport: func(context.Context, string, domain.CrashReport) (domain.CrashReport, error) {
		repositoryCalled = true
		return domain.CrashReport{}, nil
	}}
	service := NewDiagnosticsService(repository, serviceClock{}, testServiceLogger())
	request := validCrashReportRequest()
	request.RedactedSummary = "classified\x00summary"

	_, err := service.SubmitCrashReport(authenticatedContext(), connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("NUL diagnostic code = %v", connect.CodeOf(err))
	}
	if repositoryCalled {
		t.Fatal("repository was called for a diagnostic containing NUL")
	}
}

func TestValidateCrashReportRejectsNULInEveryPersistedTextGroup(t *testing.T) {
	for name, mutate := range map[string]func(*devhudv1.SubmitCrashReportRequest){
		"build": func(request *devhudv1.SubmitCrashReportRequest) {
			request.ClientBuild.OsVersion = "15\x00.6"
		},
		"summary": func(request *devhudv1.SubmitCrashReportRequest) {
			request.RedactedSummary = "classified\x00summary"
		},
		"stack": func(request *devhudv1.SubmitCrashReportRequest) {
			request.RedactedStackTrace = "render\x00frame"
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := validCrashReportRequest()
			mutate(request)
			if err := validateCrashReport(request); err == nil {
				t.Fatal("NUL diagnostic text was accepted")
			}
		})
	}
}

func TestValidateCrashReportAcceptsSafeSlashLabelsAndRemoteURLs(t *testing.T) {
	for _, safe := range []string{
		"React/Native renderer failed.",
		"iOS/18.6 runtime classification.",
		"https://example.test/assets/app.js:10:2",
		"Password validation failed because the field was empty.",
		"Cookie parsing failed after session expiry.",
		"ERROR_CODE=E_UPLOAD RETRY_COUNT=3 TOKEN_COUNT=2",
	} {
		request := validCrashReportRequest()
		request.RedactedSummary = safe
		if err := validateCrashReport(request); err != nil {
			t.Fatalf("safe diagnostic %q was rejected: %v", safe, err)
		}
	}
	request := validCrashReportRequest()
	request.RedactedStackTrace = "at PasswordValidator.parse"
	if err := validateCrashReport(request); err != nil {
		t.Fatalf("safe credential-related identifier was rejected: %v", err)
	}
}

func TestValidateCrashReportRequiresPinnedCEFRevision(t *testing.T) {
	request := validCrashReportRequest()
	request.ClientBuild.CefRevision = "x"
	if err := validateCrashReport(request); err == nil {
		t.Fatal("arbitrary desktop CEF revision was accepted")
	}
	request.ClientBuild.CefRevision = exactCEFRevision
	if err := validateCrashReport(request); err != nil {
		t.Fatalf("pinned desktop CEF revision was rejected: %v", err)
	}
}

func TestValidateCrashReportRequiresPinnedTauriRevision(t *testing.T) {
	request := validCrashReportRequest()
	request.ClientBuild.TauriRevision = strings.Repeat("a", 40)
	if err := validateCrashReport(request); err == nil {
		t.Fatal("arbitrary native Tauri revision was accepted")
	}
	request.ClientBuild.TauriRevision = exactTauriRevision
	if err := validateCrashReport(request); err != nil {
		t.Fatalf("pinned native Tauri revision was rejected: %v", err)
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
	request.ClientBuild.Architecture = devhudv1.DiagnosticArchitecture_DIAGNOSTIC_ARCHITECTURE_UNSPECIFIED
	if err := validateCrashReport(request); err != nil {
		t.Fatalf("browser build with unknown architecture was rejected: %v", err)
	}

	request.ClientBuild.TauriRevision = "4af26a3f7f8b692d62cca549bbacd93f5ce90b41"
	if err := validateCrashReport(request); err == nil {
		t.Fatal("browser build with a fabricated Tauri revision was accepted")
	}
	request = validCrashReportRequest()
	request.ClientBuild.Architecture = devhudv1.DiagnosticArchitecture_DIAGNOSTIC_ARCHITECTURE_UNSPECIFIED
	if err := validateCrashReport(request); err == nil {
		t.Fatal("native build with unknown architecture was accepted")
	}
}

func TestValidateCrashReportRestrictsARMV7ToAndroid(t *testing.T) {
	tests := []struct {
		name     string
		platform devhudv1.DiagnosticPlatform
		accept   bool
	}{
		{name: "macOS", platform: devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_MACOS},
		{name: "Windows", platform: devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_WINDOWS},
		{name: "Linux", platform: devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_LINUX},
		{name: "iOS", platform: devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_IOS},
		{name: "Android", platform: devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_ANDROID, accept: true},
		{name: "browser", platform: devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_BROWSER},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validCrashReportRequest()
			request.ClientBuild.Platform = test.platform
			request.ClientBuild.Architecture = devhudv1.DiagnosticArchitecture_DIAGNOSTIC_ARCHITECTURE_ARMV7
			if test.platform == devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_IOS || test.platform == devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_ANDROID {
				request.ClientBuild.CefRevision = ""
			}
			if test.platform == devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_BROWSER {
				request.ClientBuild.TauriRevision = ""
				request.ClientBuild.CefRevision = ""
			}

			err := validateCrashReport(request)
			if test.accept && err != nil {
				t.Fatalf("Android armv7 build was rejected: %v", err)
			}
			if !test.accept && err == nil {
				t.Fatalf("%s armv7 build was accepted", test.name)
			}
		})
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

func TestValidateCrashReportAcceptsEnumCodesThatNameRedactedContent(t *testing.T) {
	for _, code := range []string{"SCREENSHOT_CAPTURE_FAILED", "BROWSER_DOM_REDACTED"} {
		request := validCrashReportRequest()
		request.ErrorCode = code
		if err := validateCrashReport(request); err != nil {
			t.Fatalf("valid error code %q was rejected: %v", code, err)
		}
	}
}

type testSQLStateError struct {
	state string
}

func (errorValue testSQLStateError) Error() string {
	return "sensitive repository detail"
}

func (errorValue testSQLStateError) SQLState() string {
	return errorValue.state
}

type testNetworkError struct{}

func (testNetworkError) Error() string {
	return "sensitive repository detail"
}

func (testNetworkError) Timeout() bool {
	return false
}

func (testNetworkError) Temporary() bool {
	return true
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
