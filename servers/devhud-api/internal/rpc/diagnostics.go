package rpc

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
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
	maximumSummaryBytes             = 4 * 1024
	maximumStackBytes               = 32 * 1024
	maximumStackLines               = 64
	maximumStackLineBytes           = 512
	maximumRelatedCorrelations      = 32
	maximumCrashDuration            = uint64(24 * 60 * 60 * 1000)
	maximumDiagnosticDecodings      = 8
	maximumDiagnosticParameterScans = 16
	maximumDiagnosticScanBytes      = 2 * maximumStackBytes
	exactTauriRevision              = "4af26a3f7f8b692d62cca549bbacd93f5ce90b41"
	exactCEFRevision                = "150.0.10+g8042e43+chromium-150.0.7871.101"
)

var (
	safeErrorCode           = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	safeSQLState            = regexp.MustCompile(`^[0-9A-Z]{5}$`)
	diagnosticURL           = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*:[^\s<>"']+`)
	diagnosticURLParameters = regexp.MustCompile(`[?#][^\s<>"']+`)
	trailingURLPunctuation  = regexp.MustCompile(`[)\]}>.,;]+$`)
	percentEncodedOctets    = regexp.MustCompile(`(?i)(%[0-9a-f]{2})+`)
	encodedWindowsDrivePath = regexp.MustCompile(`(?i)^[A-Za-z]:(%2f|%5c)`)
	credentialParameterName = regexp.MustCompile(`(?i)^(code|oauth[_.-]?code|credentials?|password|passwd|pwd|secret|token|client[_.-]?secret|(access|refresh|id)[_.-]?token|(r2[_.-]?)?access[_.-]?key[_.-]?id|api[_.-]?key|private[_.-]?key|authorization|cookie|set-cookie|x-amz-(credential|signature))$`)
	diagnosticAssignment    = regexp.MustCompile(`(?i)(^|[[:space:]]|[(\[{,;&])["']?([A-Za-z][A-Za-z0-9_.-]{0,63})["']?[[:space:]]*[:=][[:space:]]*[^[:space:]&;]+`)
	forbiddenLocalPath      = regexp.MustCompile(`(?i)(^([[:space:]\p{P}])?|[^:][[:space:]\p{P}=]|:[[:space:]]+)([a-z]:[\\/][^[:space:]]*|\\\\[^[:space:]]+|~/[^[:space:]]+|/[^/[:space:]][^[:space:]]*)`)
	forbiddenDiagnostic     = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bbearer[[:space:]]+[^[:space:]]+`),
		regexp.MustCompile(`(?i)\b([[:alnum:]]+_)*(password|passwd|pwd|pat|secret(_access_key)?|token|client[_.-]?secret|(access|refresh|id)[_.-]?token|access[_.-]?key[_.-]?id|api[_.-]?key|private[_.-]?key|authorization|cookie|set-cookie|session[_.-]?id|signing[_.-]?(secret|key|value))\b["']?[[:space:]]*[:=][[:space:]]*[^[:space:]]+`),
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`\b(ghp|github_pat)_[A-Za-z0-9_]+\b`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`(?i)(browser[._ -]?dom|outerhtml|innerhtml|screenshot|form[._ -]?value|issue[._ -]?body|agent[._ -]?(prompt|output)|child[._ -]?env|(request|response)[._ -]?(headers?|bod(y|ies))|shortcut[._ -]?(key|keystroke))`),
		regexp.MustCompile(`(?i)https?://[^[:space:]]*#`),
		regexp.MustCompile(`(?i)\bfile://[^[:space:]]*`),
		forbiddenLocalPath,
		regexp.MustCompile(`(?i)(ctrl|control|cmd|command|meta|alt|option|shift)[[:space:]]*[+-][[:space:]]*[a-z0-9]`),
	}
)

type repositoryErrorCategory string

const (
	repositoryErrorPostgreSQL repositoryErrorCategory = "postgresql"
	repositoryErrorDeadline   repositoryErrorCategory = "deadline"
	repositoryErrorCanceled   repositoryErrorCategory = "canceled"
	repositoryErrorNetwork    repositoryErrorCategory = "network"
	repositoryErrorUnknown    repositoryErrorCategory = "unknown"
)

type sqlStateError interface {
	SQLState() string
}

type diagnosticScanBudget struct {
	remainingBytes      int
	remainingParameters int
}

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
		if errors.Is(err, domain.ErrCrashReportQuota) {
			return nil, NewError(connect.CodeResourceExhausted, "crash report quota exhausted", CorrelationID(ctx), &devhudv1.QuotaFailure{
				Quota:    devhudv1.QuotaKind_QUOTA_KIND_CRASH_REPORTS,
				Limit:    domain.CrashReportMaximumRetainedPerUser,
				Observed: domain.CrashReportMaximumRetainedPerUser + 1,
			})
		}
		var permission *domain.PermissionError
		if errors.As(err, &permission) {
			return nil, permissionError(ctx, permission)
		}
		attributes := []any{
			"correlation_id", CorrelationID(ctx),
			"client_correlation_id", request.Msg.GetClientCorrelationId().GetValue(),
			"procedure", devhudv1connect.DiagnosticsServiceSubmitCrashReportProcedure,
		}
		attributes = append(attributes, diagnosticsRepositoryErrorAttributes(err)...)
		s.logger.ErrorContext(ctx, "diagnostics repository operation failed", attributes...)
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
	if !validPlatform(build.GetPlatform()) {
		return errors.New("client_build classifications must be specified")
	}
	browser := build.GetPlatform() == devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_BROWSER
	if !validArchitecture(build.GetArchitecture(), build.GetPlatform()) {
		return errors.New("client_build architecture is not supported for platform")
	}
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
	if !browser && build.GetTauriRevision() != exactTauriRevision {
		return errors.New("tauri_revision must be the supported native revision")
	}
	desktop := build.GetPlatform() >= devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_MACOS && build.GetPlatform() <= devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_LINUX
	if desktop && build.GetCefRevision() != exactCEFRevision || !desktop && build.GetCefRevision() != "" {
		return errors.New("cef_revision must be the supported desktop revision or empty on mobile and browser hosts")
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

func diagnosticsRepositoryErrorAttributes(err error) []any {
	sqlState := diagnosticsRepositorySQLState(err)
	attributes := []any{
		"error_type", fmt.Sprintf("%T", err),
		"error_category", diagnosticsRepositoryErrorCategory(err, sqlState != ""),
	}
	if sqlState != "" {
		attributes = append(attributes, "sqlstate", sqlState)
	}
	return attributes
}

func diagnosticsRepositoryErrorCategory(err error, hasSQLState bool) repositoryErrorCategory {
	if errors.Is(err, context.DeadlineExceeded) {
		return repositoryErrorDeadline
	}
	if errors.Is(err, context.Canceled) {
		return repositoryErrorCanceled
	}
	if hasSQLState {
		return repositoryErrorPostgreSQL
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return repositoryErrorNetwork
	}
	return repositoryErrorUnknown
}

func diagnosticsRepositorySQLState(err error) string {
	var sqlState sqlStateError
	if !errors.As(err, &sqlState) {
		return ""
	}
	value := sqlState.SQLState()
	if safeSQLState.MatchString(value) {
		return value
	}
	return ""
}

func validateDiagnosticText(name, value string, maximumBytes int, emptyAllowed bool) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%s must not contain NUL bytes", name)
	}
	if !emptyAllowed && value == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if len(value) > maximumBytes {
		return fmt.Errorf("%s exceeds %d bytes", name, maximumBytes)
	}
	if containsForbiddenDiagnosticContent(value) {
		return fmt.Errorf("%s contains prohibited diagnostic content", name)
	}
	return nil
}

func containsForbiddenDiagnosticContent(value string) bool {
	budget := diagnosticScanBudget{remainingBytes: maximumDiagnosticScanBytes, remainingParameters: maximumDiagnosticParameterScans}
	return containsForbiddenDiagnosticContentWithBudget(value, &budget)
}

func containsForbiddenDiagnosticContentWithBudget(value string, budget *diagnosticScanBudget) bool {
	decodings := 0
	for {
		if len(value) > budget.remainingBytes {
			return true
		}
		budget.remainingBytes -= len(value)
		if containsForbiddenDiagnosticContentAtCurrentEncoding(value, budget) {
			return true
		}
		decoded := decodePercentEncodedOctets(value)
		if decoded == value {
			return false
		}
		if decodings == maximumDiagnosticDecodings {
			return true
		}
		value = decoded
		decodings++
	}
}

func containsForbiddenDiagnosticContentAtCurrentEncoding(value string, budget *diagnosticScanBudget) bool {
	for _, pattern := range forbiddenDiagnostic {
		if pattern.MatchString(value) {
			return true
		}
	}
	return containsForbiddenCredentialAssignment(value) || containsForbiddenEncodedLocalPath(value) || containsForbiddenDiagnosticURL(value, budget)
}

func containsForbiddenCredentialAssignment(value string) bool {
	for _, candidate := range []string{value, decodePercentEncodedOctets(value)} {
		for _, match := range diagnosticAssignment.FindAllStringSubmatch(candidate, -1) {
			if credentialParameterName.MatchString(match[2]) {
				return true
			}
		}
	}
	return false
}

func containsForbiddenEncodedLocalPath(value string) bool {
	previousEnd := 0
	for _, match := range diagnosticURL.FindAllStringIndex(value, -1) {
		if containsForbiddenDecodedLocalPath(value[previousEnd:match[0]]) {
			return true
		}
		candidate := value[match[0]:match[1]]
		if encodedWindowsDrivePath.MatchString(candidate) && containsForbiddenDecodedLocalPath(candidate) {
			return true
		}
		previousEnd = match[1]
	}
	return containsForbiddenDecodedLocalPath(value[previousEnd:])
}

func containsForbiddenDecodedLocalPath(value string) bool {
	return forbiddenLocalPath.MatchString(decodePercentEncodedOctets(value))
}

func decodePercentEncodedOctets(value string) string {
	return percentEncodedOctets.ReplaceAllStringFunc(value, func(encoded string) string {
		if unescaped, err := url.PathUnescape(encoded); err == nil {
			return unescaped
		}
		return encoded
	})
}

func containsForbiddenDiagnosticURL(value string, budget *diagnosticScanBudget) bool {
	if containsForbiddenDiagnosticURLAtCurrentEncoding(value, budget) {
		return true
	}
	decoded := decodePercentEncodedOctets(value)
	return decoded != value && containsForbiddenDiagnosticURLAtCurrentEncoding(decoded, budget)
}

func containsForbiddenDiagnosticURLAtCurrentEncoding(value string, budget *diagnosticScanBudget) bool {
	previousEnd := 0
	for _, match := range diagnosticURL.FindAllStringIndex(value, -1) {
		if containsForbiddenRelativeDiagnosticParameters(value[previousEnd:match[0]], budget) ||
			containsForbiddenParsedDiagnosticURL(value[match[0]:match[1]], budget) {
			return true
		}
		previousEnd = match[1]
	}
	return containsForbiddenRelativeDiagnosticParameters(value[previousEnd:], budget)
}

func containsForbiddenRelativeDiagnosticParameters(value string, budget *diagnosticScanBudget) bool {
	for _, match := range diagnosticURLParameters.FindAllString(value, -1) {
		parameters := trailingURLPunctuation.ReplaceAllString(match[1:], "")
		if containsForbiddenDiagnosticParameters(parameters, budget) {
			return true
		}
	}
	return false
}

func containsForbiddenParsedDiagnosticURL(value string, budget *diagnosticScanBudget) bool {
	candidate := trailingURLPunctuation.ReplaceAllString(value, "")
	if _, err := url.PathUnescape(candidate); err != nil {
		return true
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return true
	}
	lowerCandidate := strings.ToLower(candidate)
	if parsed.User != nil || parsed.Host == "" || parsed.Opaque != "" ||
		(!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) ||
		(!strings.HasPrefix(lowerCandidate, "http://") && !strings.HasPrefix(lowerCandidate, "https://")) {
		return true
	}
	return containsForbiddenDiagnosticParameters(parsed.RawQuery, budget) || containsForbiddenDiagnosticParameters(parsed.Fragment, budget)
}

func containsForbiddenDiagnosticParameters(parameters string, budget *diagnosticScanBudget) bool {
	if len(parameters) > budget.remainingBytes {
		return true
	}
	budget.remainingBytes -= len(parameters)
	for _, parameter := range strings.FieldsFunc(parameters, func(separator rune) bool {
		return separator == '&' || separator == ';'
	}) {
		if budget.remainingParameters == 0 {
			return true
		}
		budget.remainingParameters--
		name, value, found := strings.Cut(parameter, "=")
		if !found {
			name, value, _ = strings.Cut(parameter, ":")
		}
		decodedName, nameErr := url.QueryUnescape(name)
		decodedValue, valueErr := url.QueryUnescape(value)
		if nameErr != nil || valueErr != nil || credentialParameterName.MatchString(decodedName) {
			return true
		}
		if containsForbiddenDiagnosticContentWithBudget(decodedValue, budget) ||
			decodedValue != value && containsForbiddenDiagnosticParameters(decodedValue, budget) {
			return true
		}
	}
	return false
}

func validPlatform(value devhudv1.DiagnosticPlatform) bool {
	return value >= devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_MACOS && value <= devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_BROWSER
}

func validArchitecture(value devhudv1.DiagnosticArchitecture, platform devhudv1.DiagnosticPlatform) bool {
	return value == devhudv1.DiagnosticArchitecture_DIAGNOSTIC_ARCHITECTURE_X86_64 ||
		value == devhudv1.DiagnosticArchitecture_DIAGNOSTIC_ARCHITECTURE_ARM64 ||
		value == devhudv1.DiagnosticArchitecture_DIAGNOSTIC_ARCHITECTURE_ARMV7 && platform == devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_ANDROID ||
		value == devhudv1.DiagnosticArchitecture_DIAGNOSTIC_ARCHITECTURE_UNSPECIFIED && platform == devhudv1.DiagnosticPlatform_DIAGNOSTIC_PLATFORM_BROWSER
}

func validComponent(value devhudv1.DiagnosticComponent) bool {
	return value >= devhudv1.DiagnosticComponent_DIAGNOSTIC_COMPONENT_APP && value <= devhudv1.DiagnosticComponent_DIAGNOSTIC_COMPONENT_NATIVE_SHELL
}

func validSeverity(value devhudv1.DiagnosticSeverity) bool {
	return value == devhudv1.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_ERROR || value == devhudv1.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_FATAL
}
