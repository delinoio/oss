package upload

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maximumAdministratorReasonBytes = 4096

var (
	credentialParameterNamePattern = regexp.MustCompile(`(?i)^(code|password|passwd|pwd|secret|token|client[_.-]?secret|(access|refresh|id)[_.-]?token|api[_.-]?key|private[_.-]?key|authorization|cookie|set-cookie|x-amz-(credential|signature))$`)
	urlPattern                     = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*:[^\s<>"']+`)
	trailingURLPunctuationPattern  = regexp.MustCompile(`[)\]}>.,;]+$`)
	absoluteLocalPathPattern       = regexp.MustCompile(`(^|[\s([{<"'=:])([A-Za-z]:[\\/][^\s]*|\\\\[^\s]+|~/[^\s]+|/[^/\s][^\s]*)`)
	explicitRelativePathPattern    = regexp.MustCompile(`(^|[\s([{<"'=:])\.{1,2}[\\/]([\p{L}\p{N}_@.-]+[\\/])*[\p{L}\p{N}_@.-]+(:[0-9]+){0,2}($|[\s\p{P}])`)
	fileRelativePathPattern        = regexp.MustCompile(`(^|[\s([{<"'=:])([\p{L}\p{N}_@.-]+[\\/])+(\.[\p{L}\p{N}_@.-]+|[\p{L}\p{N}_@.-]+\.[\p{L}][\p{L}\p{N}]*|Dockerfile|Makefile)(:[0-9]+){0,2}($|[\s\p{P}])`)
	lineRelativePathPattern        = regexp.MustCompile(`(^|[\s([{<"'=:])([\p{L}\p{N}_@.-]+[\\/])+[\p{L}\p{N}_@.-]+:[0-9]+(:[0-9]+)?($|[\s\p{P}])`)
	fileLinePattern                = regexp.MustCompile(`(^|[\s([{<"'=:])[\p{L}\p{N}_@.-]+\.[\p{L}][\p{L}\p{N}]*:[0-9]+(:[0-9]+)?($|[\s\p{P}])`)
	sensitiveTextPatterns          = []*regexp.Regexp{
		regexp.MustCompile(`(?i)file://[^\s]*`),
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`\b(ghp|github_pat)_[A-Za-z0-9_]+\b`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
		regexp.MustCompile(`(?i)\bAuthorization\s*:\s*(Basic|Bearer)\s+\S+`),
		regexp.MustCompile(`(?i)\b([\p{L}\p{N}]+_)*(password|passwd|pwd|secret(_access_key)?|token|client[_.-]?secret|(access|refresh|id)[_.-]?token|api[_.-]?key|private[_.-]?key|authorization|cookie|set-cookie)\b["']?\s*[:=]\s*\S+`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	}
)

func validateAdministratorReason(reason string) error {
	return validateAdministratorReasonForPublicAssets(reason, nil)
}

func validateAdministratorReasonForPublicAssets(reason string, publicAssetBaseURL *url.URL) error {
	if reason == "" || len(reason) > maximumAdministratorReasonBytes || !utf8.ValidString(reason) {
		return errors.New("administrator reason must contain 1 to 4096 bytes of well-formed UTF-8")
	}
	if containsSensitiveText(reason) || containsLocalPath(reason) || containsSensitiveURL(reason, publicAssetBaseURL) {
		if publicAssetBaseURL != nil {
			return errors.New("administrator reason contains credential, public asset locator, or local-path content")
		}
		return errors.New("administrator reason contains credential or local-path content")
	}
	return nil
}

func ValidateAdministratorReason(reason string, publicAssetBaseURL *url.URL) error {
	return validateAdministratorReasonForPublicAssets(strings.TrimSpace(reason), publicAssetBaseURL)
}

func containsSensitiveText(value string) bool {
	for _, pattern := range sensitiveTextPatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func containsLocalPath(value string) bool {
	if matchesLocalPath(value) {
		return true
	}
	decoded, err := url.PathUnescape(value)
	return err == nil && decoded != value && matchesLocalPath(decoded)
}

func matchesLocalPath(value string) bool {
	return absoluteLocalPathPattern.MatchString(value) ||
		explicitRelativePathPattern.MatchString(value) ||
		fileRelativePathPattern.MatchString(value) ||
		lineRelativePathPattern.MatchString(value) ||
		fileLinePattern.MatchString(value)
}

func containsSensitiveURL(value string, publicAssetBaseURL *url.URL) bool {
	if matchesSensitiveURL(value, publicAssetBaseURL) {
		return true
	}
	decoded, err := url.PathUnescape(value)
	return err == nil && decoded != value && matchesSensitiveURL(decoded, publicAssetBaseURL)
}

func matchesSensitiveURL(value string, publicAssetBaseURL *url.URL) bool {
	for _, match := range urlPattern.FindAllString(value, -1) {
		candidate := trailingURLPunctuationPattern.ReplaceAllString(match, "")
		if _, err := url.PathUnescape(candidate); err != nil {
			return true
		}
		parsed, err := url.Parse(candidate)
		if err != nil {
			continue
		}
		if strings.EqualFold(parsed.Scheme, "file") || parsed.User != nil {
			return true
		}
		if publicAssetBaseURL != nil && isPublicAssetLocator(parsed, publicAssetBaseURL) {
			return true
		}
		if containsSensitiveParameters(parsed.RawQuery, publicAssetBaseURL) || containsSensitiveParameters(parsed.Fragment, publicAssetBaseURL) {
			return true
		}
	}
	return false
}

func isPublicAssetLocator(candidate, publicAssetBaseURL *url.URL) bool {
	if !strings.EqualFold(candidate.Scheme, publicAssetBaseURL.Scheme) ||
		!strings.EqualFold(candidate.Hostname(), publicAssetBaseURL.Hostname()) ||
		effectivePort(candidate) != effectivePort(publicAssetBaseURL) {
		return false
	}
	basePath, baseErr := url.PathUnescape(publicAssetBaseURL.EscapedPath())
	candidatePath, candidateErr := url.PathUnescape(candidate.EscapedPath())
	if baseErr != nil || candidateErr != nil {
		return true
	}
	basePath = strings.TrimRight(basePath, "/")
	return basePath == "" || candidatePath == basePath || strings.HasPrefix(candidatePath, basePath+"/")
}

func effectivePort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	switch strings.ToLower(value.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func containsSensitiveParameters(parameters string, publicAssetBaseURL *url.URL) bool {
	for _, parameter := range strings.Split(parameters, "&") {
		name, value, found := strings.Cut(parameter, "=")
		if !found {
			name, value, _ = strings.Cut(parameter, ":")
		}
		decodedName, nameErr := url.QueryUnescape(name)
		decodedValue, valueErr := url.QueryUnescape(value)
		if nameErr != nil || valueErr != nil || credentialParameterNamePattern.MatchString(decodedName) {
			return true
		}
		if containsSensitiveText(decodedValue) || containsLocalPath(decodedValue) || containsSensitiveURL(decodedValue, publicAssetBaseURL) {
			return true
		}
	}
	return false
}
