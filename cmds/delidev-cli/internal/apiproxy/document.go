package apiproxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

var errInvalidDocument = errors.New("invalid proxy document")
var errSecret = errors.New("protected value in proxy response")

type secretGuard struct{ values []string }

func newSecretGuard(values ...[]byte) secretGuard {
	g := secretGuard{}
	for _, value := range values {
		if len(value) == 0 {
			continue
		}
		g.values = append(g.values, string(value), base64.StdEncoding.EncodeToString(value), base64.RawStdEncoding.EncodeToString(value), base64.URLEncoding.EncodeToString(value), base64.RawURLEncoding.EncodeToString(value))
	}
	return g
}
func (g secretGuard) contains(value string) bool {
	for _, secret := range g.values {
		if strings.Contains(value, secret) {
			return true
		}
	}
	return false
}

// Walk before projecting fields: encoding/json alone accepts duplicate keys and
// replacement characters for malformed input. Retained protocol bytes remain
// unchanged, but all JSON string keys/values are checked after escape decoding.
func document(raw []byte, guard secretGuard) (map[string]json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, errInvalidDocument
	}
	if guard.contains(string(raw)) {
		return nil, errSecret
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 64 {
			return errInvalidDocument
		}
		token, err := d.Token()
		if err != nil {
			return errInvalidDocument
		}
		if value, ok := token.(string); ok && guard.contains(value) {
			return errSecret
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				token, err := d.Token()
				if err != nil {
					return errInvalidDocument
				}
				key, ok := token.(string)
				if !ok || seen[key] {
					return errInvalidDocument
				}
				seen[key] = true
				if guard.contains(key) {
					return errSecret
				}
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return errInvalidDocument
			}
		case '[':
			for d.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return errInvalidDocument
			}
		default:
			return errInvalidDocument
		}
		return nil
	}
	if err := walk(0); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errInvalidDocument
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, errInvalidDocument
	}
	return object, nil
}

func validateRequest(ctx context.Context, raw []byte, lease *Lease, op Operation) (bool, error) {
	object, err := document(raw, secretGuard{})
	if err != nil {
		return false, domain.Fail(domain.InvalidArgument, "The native API request is not unambiguous UTF-8 JSON.", "Send one bounded JSON object with unique fields.")
	}
	for key := range object {
		lower := strings.ToLower(key)
		if key != lower && (lower == "model" || lower == "stream" || lower == "previous_response_id" || lower == "conversation" || lower == "models" || lower == "route" || lower == "fallbacks") {
			return false, domain.Fail(domain.InvalidArgument, "Ambiguous native API control field.", "Use exact lowercase native API field names.")
		}
	}
	var model string
	if json.Unmarshal(object["model"], &model) != nil || model != lease.Scope.NativeModel {
		return false, domain.Fail(domain.PermissionDenied, "The request model is outside this execution's authorization.", "Use the execution's configured canonical model.")
	}
	if _, exists := object["models"]; exists {
		return false, domain.Fail(domain.PermissionDenied, "Alternate model routing is not authorized.", "Use only the fixed execution model.")
	}
	if _, exists := object["route"]; exists {
		return false, domain.Fail(domain.PermissionDenied, "Request-selected fallback routing is not authorized.", "Use only the fixed execution provider and model.")
	}
	// Anthropic's native fallbacks chain can choose a different model while
	// leaving the top-level model unchanged. The execution owns exactly one
	// model, so even an empty/null chain cannot introduce routing authority.
	if _, exists := object["fallbacks"]; exists {
		return false, domain.Fail(domain.PermissionDenied, "Native fallback models are outside this execution's authorization.", "Use only the fixed execution model.")
	}
	stream := false
	if raw, ok := object["stream"]; ok {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &stream) != nil {
			return false, domain.Fail(domain.InvalidArgument, "Invalid native stream selection.", "Use a boolean stream field.")
		}
	}
	if stream && (op == ResponseCompact || op == MessageCountTokens) {
		return false, domain.Fail(domain.Unsupported, "This native API operation does not stream.", "Use the operation's compatible native request.")
	}
	if op == ResponseCreate {
		for name, kind := range map[string]ReferenceKind{"previous_response_id": ResponseReference, "conversation": ConversationReference} {
			value, ok := object[name]
			value = bytes.TrimSpace(value)
			if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				continue
			}
			var id string
			if name == "conversation" && len(value) > 0 && value[0] == '{' {
				var ref map[string]json.RawMessage
				if json.Unmarshal(value, &ref) != nil || json.Unmarshal(ref["id"], &id) != nil {
					return false, domain.Fail(domain.InvalidArgument, "Invalid native conversation reference.", "Use an owned native conversation identity.")
				}
			} else if json.Unmarshal(value, &id) != nil {
				return false, domain.Fail(domain.InvalidArgument, "Invalid native state reference.", "Use an owned native state identity.")
			}
			if err := domain.Text(id, "native state reference", 256, true); err != nil {
				return false, err
			}
			if lease.AuthorizeReference == nil {
				return false, domain.Fail(domain.Unsupported, "Native state-reference ownership is unavailable for this execution.", "Use the native adapter's supported continuation path.")
			}
			if err := lease.AuthorizeReference(ctx, kind, id); err != nil {
				return false, err
			}
		}
	}
	return stream, nil
}
