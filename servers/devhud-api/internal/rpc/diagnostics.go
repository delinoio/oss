package rpc

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"github.com/delinoio/oss/protos/gen/go/devhud/v1/devhudv1connect"
	"github.com/delinoio/oss/servers/devhud-api/internal/auth"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	maximumSummaryBytes        = 4 * 1024
	maximumStackBytes          = 32 * 1024
	maximumStackLines          = 64
	maximumStackLineBytes      = 512
	maximumRelatedCorrelations = 32
	maximumCrashDuration       = uint64(24 * 60 * 60 * 1000)
)

var (
	exactTauriRevision  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	safeErrorCode       = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	forbiddenDiagnostic = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(authorization|bearer[[:space:]]|access[_ -]?token|refresh[_ -]?token|personal[_ -]?access[_ -]?token|github[_ -]?pat|api[_ -]?key|password|cookie|session[_ -]?id|r2[_ -]?(secret|token|key)|signing[_ -]?(secret|key|value))`),
		regexp.MustCompile(`\b(ghp|github_pat)_[A-Za-z0-9_]+\b`),
		regexp.MustCompile(`(?i)(browser[._ -]?dom|outerhtml|innerhtml|screenshot|form[._ -]?value|issue[._ -]?body|agent[._ -]?(prompt|output)|child[._ -]?env|shortcut[._ -]?(key|keystroke))`),
		regexp.MustCompile(`(?i)https?://[^[:space:]]*#`),
		regexp.MustCompile(`(?i)(^|[[:space:]([{<"'=:])(?:[a-z]:[\\/][^[:space:]]*|\\\\[^[:space:]]+|~/[^[:space:]]+|/[^/[:space:]][^[:space:]]*)`),
		regexp.MustCompile(`(?i)(ctrl|control|cmd|command|meta|alt|option|shift)[[:space:]]*[+-][[:space:]]*[a-z0-9]`),
	}
)

type DiagnosticsService struct {
	repository domain.Repository
	clock      domain.Clock
	logger     *slog.Logger
}

func NewDiagnosticsService(repository domain.Repository, clock domain.Clock, logger *slog.Logger) *DiagnosticsService {
	return &DiagnosticsService{repository: repository, clock: clock, logger: logger}
}

func (s *DiagnosticsService) SubmitCrashReport(ctx context.Context, request *connect.Request[devhudv1.SubmitCrashReportRequest]) (*connect.Response[devhudv1.SubmitCrashReportResponse], error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, unauthenticatedError(ctx)
	}
	if err := validateCrashReport(request.Msg); err != nil {
		return nil, NewError(connect.CodeInvalidArgument, err.Error(), CorrelationID(ctx))
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(request.Msg)
	if err != nil {
		return nil, NewError(connect.CodeInvalidArgument, "crash report payload is invalid", CorrelationID(ctx))
	}
	digest := sha256.Sum256(payload)
	build := request.Msg.GetClientBuild()
	related := make([]string, 0, len(request.Msg.GetRelatedCorrelationIds()))
	for _, id := range request.Msg.GetRelatedCorrelationIds() {
		related = append(related, id.GetValue())
	}
	now := s.clock.Now()
	report, err := s.repository.SubmitCrashReport(ctx, user.ID, domain.CrashReport{
		RequestCorrelationID:  CorrelationID(ctx),
		ClientCorrelationID:   request.Msg.GetClientCorrelationId().GetValue(),
		PayloadSHA256:         digest[:],
		ReportSchemaVersion:   request.Msg.GetReportSchemaVersion(),
		AppVersion:            build.GetAppVersion(),
		BuildID:               build.GetBuildId(),
		Platform:              int16(build.GetPlatform()),
		Architecture:          int16(build.GetArchitecture()),
		OSVersion:             build.GetOsVersion(),
		TauriRevision:         build.GetTauriRevision(),
		CEFRevision:           build.GetCefRevision(),
		OccurredAt:            request.Msg.GetOccurredAt().AsTime(),
		Component:             int16(request.Msg.GetComponent()),
		Severity:              int16(request.Msg.GetSeverity()),
		ErrorCode:             request.Msg.GetErrorCode(),
		RedactedSummary:       request.Msg.GetRedactedSummary(),
		RedactedStackTrace:    request.Msg.GetRedactedStackTrace(),
		RelatedCorrelationIDs: related,
		DurationMilliseconds:  request.Msg.GetDurationMilliseconds(),
		AcceptedAt:            now,
		ExpiresAt:             now.Add(domain.CrashReportRetention),
	})
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, deletionCompletePermissionError(ctx)
		}
		if errors.Is(err, domain.ErrCorrelationConflict) {
			return nil, NewError(connect.CodeAlreadyExists, "client_correlation_id already identifies a different payload", CorrelationID(ctx))
		}
		var permission *domain.PermissionError
		if errors.As(err, &permission) {
			return nil, permissionError(ctx, permission)
		}
		s.logger.ErrorContext(ctx, "diagnostics repository operation failed",
			"correlation_id", CorrelationID(ctx),
			"client_correlation_id", request.Msg.GetClientCorrelationId().GetValue(),
			"procedure", devhudv1connect.DiagnosticsServiceSubmitCrashReportProcedure,
			"error_type", fmt.Sprintf("%T", err),
		)
		return nil, internalError(ctx)
	}
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("devhud.correlation_id", CorrelationID(ctx)),
		attribute.String("devhud.client_correlation_id", report.ClientCorrelationID),
		attribute.Int("devhud.diagnostic.platform", int(report.Platform)),
		attribute.Int("devhud.diagnostic.component", int(report.Component)),
	)
	s.logger.InfoContext(ctx, "crash report accepted",
		"correlation_id", CorrelationID(ctx),
		"client_correlation_id", report.ClientCorrelationID,
		"crash_report_id", report.ID,
		"platform", report.Platform,
		"component", report.Component,
		"severity", report.Severity,
		"duration_milliseconds", report.DurationMilliseconds,
		"expires_at", report.ExpiresAt,
	)
	return connect.NewResponse(&devhudv1.SubmitCrashReportResponse{
		Metadata:      metadata(CorrelationID(ctx)),
		CrashReportId: uuid(report.ID),
		AcceptedAt:    timestamppb.New(report.AcceptedAt),
		ExpiresAt:     timestamppb.New(report.ExpiresAt),
	}), nil
}

func validateCrashReport(request *devhudv1.SubmitCrashReportRequest) error {
	if request.GetReportSchemaVersion() != 1 {
		return errors.New("report_schema_version must be 1")
	}
	build := request.GetClientBuild()
	if build == nil {
		return errors.New("client_build is required")
	}
	if !validPlatform(build.GetPlatform()) || !validArchitecture(build.GetArchitecture()) {
		return errors.New("client_build classifications must be specified")
	}
	browser := build.GetPlatform() == devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_BROWSER
	for name, value := range map[string]string{
		"app_version": build.GetAppVersion(), "build_id": build.GetBuildId(),
		"os_version": build.GetOsVersion(), "tauri_revision": build.GetTauriRevision(),
		"cef_revision": build.GetCefRevision(),
	} {
		if err := validateDiagnosticText(name, value, 256, name == "cef_revision" || name == "tauri_revision" && browser); err != nil {
			return err
		}
	}
	if browser != (build.GetTauriRevision() == "") {
		return errors.New("tauri_revision must be exact on native hosts and empty in browsers")
	}
	if !browser && !exactTauriRevision.MatchString(build.GetTauriRevision()) {
		return errors.New("tauri_revision must be an exact lowercase source revision")
	}
	desktop := build.GetPlatform() >= devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_MACOS && build.GetPlatform() <= devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_LINUX
	if desktop == (build.GetCefRevision() == "") {
		return errors.New("cef_revision must be exact on desktop and empty on mobile or browser hosts")
	}
	if request.GetOccurredAt() == nil || request.GetOccurredAt().CheckValid() != nil {
		return errors.New("occurred_at must be a valid timestamp")
	}
	if !validComponent(request.GetComponent()) || !validSeverity(request.GetSeverity()) {
		return errors.New("diagnostic classifications must be specified")
	}
	if !safeErrorCode.MatchString(request.GetErrorCode()) {
		return errors.New("error_code must be a safe enum-style classification")
	}
	if err := validateDiagnosticText("error_code", request.GetErrorCode(), 256, false); err != nil {
		return err
	}
	if err := validateDiagnosticText("redacted_summary", request.GetRedactedSummary(), maximumSummaryBytes, true); err != nil {
		return err
	}
	if err := validateDiagnosticText("redacted_stack_trace", request.GetRedactedStackTrace(), maximumStackBytes, true); err != nil {
		return err
	}
	lines := strings.Split(request.GetRedactedStackTrace(), "\n")
	if len(lines) > maximumStackLines {
		return fmt.Errorf("redacted_stack_trace exceeds %d lines", maximumStackLines)
	}
	for _, line := range lines {
		if len(line) > maximumStackLineBytes {
			return fmt.Errorf("redacted_stack_trace line exceeds %d bytes", maximumStackLineBytes)
		}
	}
	if len(request.GetRelatedCorrelationIds()) > maximumRelatedCorrelations {
		return fmt.Errorf("related_correlation_ids exceeds %d entries", maximumRelatedCorrelations)
	}
	seen := make(map[string]struct{}, len(request.GetRelatedCorrelationIds())+1)
	if !validUUIDv7(request.GetClientCorrelationId()) {
		return errors.New("client_correlation_id must be a canonical UUID v7")
	}
	seen[request.GetClientCorrelationId().GetValue()] = struct{}{}
	for _, id := range request.GetRelatedCorrelationIds() {
		if !validUUIDv7(id) {
			return errors.New("related_correlation_ids must contain canonical UUID v7 values")
		}
		if _, duplicate := seen[id.GetValue()]; duplicate {
			return errors.New("correlation identifiers must be unique")
		}
		seen[id.GetValue()] = struct{}{}
	}
	if request.GetDurationMilliseconds() > maximumCrashDuration {
		return errors.New("duration_milliseconds exceeds 24 hours")
	}
	return nil
}

func validateDiagnosticText(name, value string, maximumBytes int, emptyAllowed bool) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	if !emptyAllowed && value == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if len(value) > maximumBytes {
		return fmt.Errorf("%s exceeds %d bytes", name, maximumBytes)
	}
	for _, pattern := range forbiddenDiagnostic {
		if pattern.MatchString(value) {
			return fmt.Errorf("%s contains prohibited diagnostic content", name)
		}
	}
	return nil
}

func validPlatform(value devhudv1.DiagnosticPlatform) bool {
	return value >= devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_MACOS && value <= devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_BROWSER
}

func validArchitecture(value devhudv1.DiagnosticArchitecture) bool {
	return value >= devhudv1.DiagnosticArchitecture_DIAGNOSTIC_ARCHITECTURE_X86_64 && value <= devhudv1.DiagnosticArchitecture_DIAGNOSTIC_ARCHITECTURE_ARMV7
}

func validComponent(value devhudv1.DiagnosticComponent) bool {
	return value >= devhudv1.DiagnosticComponent_DIAGNOSTIC_COMPONENT_APP && value <= devhudv1.DiagnosticComponent_DIAGNOSTIC_COMPONENT_NATIVE_SHELL
}

func validSeverity(value devhudv1.DiagnosticSeverity) bool {
	return value == devhudv1.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_ERROR || value == devhudv1.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_FATAL
}
