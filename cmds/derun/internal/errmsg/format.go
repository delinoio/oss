package errmsg

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxStringValueRunes = 80
)

// Usage formats argument validation failures.
func Usage(reason, hint string, details map[string]any) string {
	message := fmt.Sprintf("invalid arguments: %s", sanitizeString(reason))
	message = WithDetails(message, details)
	if hint == "" {
		return message
	}
	return fmt.Sprintf("%s; hint: %s", message, sanitizeString(hint))
}

// Runtime formats runtime failures.
func Runtime(action string, err error, details map[string]any) string {
	message := fmt.Sprintf("failed to %s", sanitizeString(action))
	if err != nil {
		message = fmt.Sprintf("%s: %v", message, err)
	}
	effectiveDetails := ensureRuntimeDetails(err, details)
	return WithDetails(message, effectiveDetails)
}

// Parse formats parse failures while preserving "parse <field>" compatibility tokens.
func Parse(field string, err error, details map[string]any) error {
	message := fmt.Sprintf("parse %s", sanitizeString(field))
	if err != nil {
		message = fmt.Sprintf("%s: %v", message, err)
	}
	return Error(message, ensureRuntimeDetails(err, details))
}

// Required formats required field failures while preserving "<field> is required" compatibility tokens.
func Required(field, expected string, received any) error {
	message := fmt.Sprintf("%s is required", sanitizeString(field))
	if expected != "" {
		message = fmt.Sprintf("%s; expected %s", message, sanitizeString(expected))
	}
	return Error(message, ReceivedDetails(received))
}

// WithDetails appends deterministic single-line details.
func WithDetails(message string, details map[string]any) string {
	encoded := Details(details)
	if encoded == "" {
		return message
	}
	return fmt.Sprintf("%s; details: %s", message, encoded)
}

// Error creates an error with deterministic single-line details.
func Error(message string, details map[string]any) error {
	return errors.New(WithDetails(message, details))
}

// Wrap preserves wrapping semantics for sentinel errors while attaching details.
func Wrap(err error, details map[string]any) error {
	if err == nil {
		return nil
	}
	encoded := Details(details)
	if encoded == "" {
		return err
	}
	return fmt.Errorf("%w; details: %s", err, encoded)
}

// Details renders deterministic key/value details for error messages.
func Details(details map[string]any) string {
	if len(details) == 0 {
		return ""
	}
	keys := make([]string, 0, len(details))
	for key := range details {
		if strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, ValueSummary(details[key])))
	}
	return strings.Join(parts, ", ")
}

// Merge combines multiple detail maps.
func Merge(detailMaps ...map[string]any) map[string]any {
	merged := make(map[string]any)
	for _, detailMap := range detailMaps {
		for key, value := range detailMap {
			if strings.TrimSpace(key) == "" {
				continue
			}
			merged[key] = value
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// ReceivedDetails returns normalized details for user-provided values.
func ReceivedDetails(value any) map[string]any {
	return map[string]any{
		"received_type":  TypeName(value),
		"received_value": ValueSummary(value),
	}
}

// CommandDetails records safe command metadata without exposing all arguments.
func CommandDetails(command []string) map[string]any {
	commandName := "<empty>"
	if len(command) > 0 && strings.TrimSpace(command[0]) != "" {
		commandName = command[0]
	}
	argumentCount := 0
	if len(command) > 0 {
		argumentCount = len(command) - 1
	}
	return map[string]any{
		"command_name": commandName,
		"arg_count":    argumentCount,
	}
}

// TypeName returns a stable type name for details.
func TypeName(value any) string {
	if value == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T", value)
}

// ValueSummary converts a value into a bounded single-line representation.
func ValueSummary(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return sanitizeString(typed)
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case int8, int16, int32, int64:
		return fmt.Sprintf("%d", typed)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", typed)
	case float32:
		return strconv.FormatFloat(float64(typed), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case json.Number:
		return sanitizeString(typed.String())
	case error:
		return sanitizeString(typed.Error())
	case []byte:
		return fmt.Sprintf("[]byte(len=%d)", len(typed))
	}

	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Map:
		return fmt.Sprintf("%s(len=%d)", reflected.Type().String(), reflected.Len())
	case reflect.Slice, reflect.Array:
		return fmt.Sprintf("%s(len=%d)", reflected.Type().String(), reflected.Len())
	case reflect.Struct:
		return reflected.Type().String()
	case reflect.Pointer:
		if reflected.IsNil() {
			return "null"
		}
		return reflected.Type().String()
	}
	return sanitizeString(fmt.Sprintf("%v", value))
}

func ensureRuntimeDetails(err error, details map[string]any) map[string]any {
	if err == nil {
		return details
	}
	extra := map[string]any{
		"cause_type": TypeName(err),
	}
	return Merge(details, extra)
}

func sanitizeString(value string) string {
	replacer := strings.NewReplacer(
		"\r", "\\r",
		"\n", "\\n",
		"\t", "\\t",
	)
	sanitized := replacer.Replace(value)
	if utf8.RuneCountInString(sanitized) <= maxStringValueRunes {
		return sanitized
	}
	runes := []rune(sanitized)
	return string(runes[:maxStringValueRunes-3]) + "..."
}
