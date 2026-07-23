// Package redact provides the repository-shared sensitive-data boundary for
// headers, log attributes, errors, and diagnostics.
package redact

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

const Replacement = "[REDACTED]"

var normalizedSensitiveKeys = []string{
	"authorization",
	"proxyauthorization",
	"xdelibaseforwardedusertoken",
	"token",
	"secret",
	"password",
	"passwd",
	"apikey",
	"cookie",
	"setcookie",
	"webhooksignature",
	"card",
	"pan",
	"cvv",
	"cvc",
	"email",
	"phone",
	"address",
}

var (
	authorizationPattern = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/=-]+`)
	jwtPattern           = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\b`)
	secretPairPattern    = regexp.MustCompile(`(?i)(authorization|token|secret|password|passwd|api[-_]?key|x-delibase-forwarded-user-token)(["']?\s*[:=]\s*["']?)[^"'\s,;&]+`)
	emailPattern         = regexp.MustCompile(`\b[A-Za-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+\b`)
	cardPattern          = regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`)
)

// IsSensitiveKey uses a conservative allow-deny boundary. It intentionally
// redacts billing PII and card-related names in addition to credentials.
func IsSensitiveKey(key string) bool {
	normalized := strings.Map(func(r rune) rune {
		switch r {
		case '-', '_', '.', ' ', '/':
			return -1
		default:
			return r
		}
	}, strings.ToLower(key))
	for _, sensitive := range normalizedSensitiveKeys {
		if strings.Contains(normalized, sensitive) {
			return true
		}
	}
	return false
}

// Headers returns a clone safe for logs and diagnostics.
func Headers(headers http.Header) http.Header {
	clone := headers.Clone()
	for key := range clone {
		if IsSensitiveKey(key) {
			clone[key] = []string{Replacement}
			continue
		}
		for index, value := range clone[key] {
			clone[key][index] = Text(value)
		}
	}
	return clone
}

// Text redacts common credential, JWT, raw-email, and card-number shapes. It
// is a final safety net; callers should prefer typed logging APIs that never
// accept these values in the first place.
func Text(value string) string {
	value = authorizationPattern.ReplaceAllString(value, Replacement)
	value = jwtPattern.ReplaceAllString(value, Replacement)
	value = secretPairPattern.ReplaceAllString(value, `$1$2`+Replacement)
	value = emailPattern.ReplaceAllString(value, Replacement)
	value = cardPattern.ReplaceAllString(value, Replacement)
	return value
}

// Error creates an unwrapped diagnostic error so unsafe values cannot be
// recovered by traversing an error chain.
func Error(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(Text(err.Error()))
}

// Value recursively sanitizes common diagnostic structures.
func Value(key string, value any) any {
	if IsSensitiveKey(key) {
		return Replacement
	}
	switch typed := value.(type) {
	case string:
		return Text(typed)
	case error:
		return Text(typed.Error())
	case http.Header:
		return Headers(typed)
	case map[string]any:
		safe := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			safe[childKey] = Value(childKey, childValue)
		}
		return safe
	case []any:
		safe := make([]any, len(typed))
		for index, childValue := range typed {
			safe[index] = Value(key, childValue)
		}
		return safe
	default:
		return typed
	}
}

// String is useful when a safe printable diagnostic is required.
func String(key string, value any) string {
	return fmt.Sprint(Value(key, value))
}
