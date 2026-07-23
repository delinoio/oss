// Package redact provides the repository-shared sensitive-data boundary for
// headers, log attributes, errors, and diagnostics.
package redact

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strings"
)

const Replacement = "[REDACTED]"

// maxValueDepth bounds recursive traversal of cyclic diagnostic containers.
const maxValueDepth = 32

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
	return valueAtDepth(key, value, 0)
}

func valueAtDepth(key string, value any, depth int) any {
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
		if depth >= maxValueDepth {
			return Replacement
		}
		safe := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			safe[childKey] = valueAtDepth(childKey, childValue, depth+1)
		}
		return safe
	case []any:
		if depth >= maxValueDepth {
			return Replacement
		}
		safe := make([]any, len(typed))
		for index, childValue := range typed {
			safe[index] = valueAtDepth(key, childValue, depth+1)
		}
		return safe
	default:
		reflected := reflect.ValueOf(typed)
		switch reflected.Kind() {
		case reflect.String:
			return Text(reflected.String())
		case reflect.Map:
			if reflected.Type().Key().Kind() != reflect.String {
				return typed
			}
			if reflected.IsNil() {
				return nil
			}
			if depth >= maxValueDepth {
				return Replacement
			}
			safe := make(map[string]any, reflected.Len())
			iterator := reflected.MapRange()
			for iterator.Next() {
				childKey := iterator.Key().String()
				childValue := iterator.Value()
				if !childValue.CanInterface() {
					safe[childKey] = Replacement
					continue
				}
				safe[childKey] = valueAtDepth(childKey, childValue.Interface(), depth+1)
			}
			return safe
		case reflect.Array, reflect.Slice:
			if reflected.Kind() == reflect.Slice && reflected.IsNil() {
				return nil
			}
			if depth >= maxValueDepth {
				return Replacement
			}
			safe := make([]any, reflected.Len())
			for index := range reflected.Len() {
				childValue := reflected.Index(index)
				if !childValue.CanInterface() {
					safe[index] = Replacement
					continue
				}
				safe[index] = valueAtDepth(key, childValue.Interface(), depth+1)
			}
			return safe
		default:
			return typed
		}
	}
}

// String is useful when a safe printable diagnostic is required.
func String(key string, value any) string {
	return fmt.Sprint(Value(key, value))
}
