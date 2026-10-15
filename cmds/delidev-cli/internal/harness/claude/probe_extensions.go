package claude

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Claude Code 2.1.287 sends one non-authoritative system/ui_invalidate startup
// frame before its initialize response and adds bounded fields to the response.
// These are private handshake metadata only: they never grant account, tool,
// remote-control or execution readiness, and every unknown shape fails closed.
const (
	probePreludeType     = "system"
	probePreludeSubtype  = "ui_invalidate"
	maxProbeIdentity     = 256
	maxProbeCapability   = 128
	maxProbeCapabilities = 64
	maxProbeStylesDir    = 4096
)

// probePrelude is the exact accepted startup frame schema. Keys must match
// these spellings exactly (see requireExactNativeKeys); domain.Decode rejects
// unknown and duplicate keys. The identity fields keep their raw JSON so that a
// declared null is distinguishable from an absent field.
type probePrelude struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	Event     json.RawMessage `json:"event,omitempty"`
	UUID      json.RawMessage `json:"uuid"`
	SessionID json.RawMessage `json:"session_id"`
}

// controlResponseEnvelope and controlResponseBody are the closed control-response
// schema (claude-agent-sdk ControlResponse). The pending lists are members of the
// inner envelope next to subtype, request_id and response, never of the
// initialize result. Raw messages preserve presence versus null.
type controlResponseEnvelope struct {
	Type     string          `json:"type"`
	Response json.RawMessage `json:"response"`
}

type controlResponseBody struct {
	Subtype                   string          `json:"subtype"`
	RequestID                 string          `json:"request_id"`
	Response                  json.RawMessage `json:"response,omitempty"`
	Error                     json.RawMessage `json:"error,omitempty"`
	PendingPermissionRequests json.RawMessage `json:"pending_permission_requests,omitempty"`
	PendingUserDialogRequests json.RawMessage `json:"pending_user_dialog_requests,omitempty"`
}

// decodeControlResponse strictly decodes one control_response frame: exact
// lowercase keys at every level, unique keys, a non-null inner envelope and
// empty pending lists when declared. Callers translate the generic failure.
func decodeControlResponse(raw []byte) (controlResponseBody, error) {
	var outer controlResponseEnvelope
	var body controlResponseBody
	if !requireExactNativeKeys(raw, &outer) || domain.Decode(raw, &outer) != nil || outer.Type != "control_response" || len(outer.Response) == 0 ||
		!requireExactNativeKeys(outer.Response, &body) || domain.Decode(outer.Response, &body) != nil || bytes.Equal(bytes.TrimSpace(outer.Response), []byte("null")) {
		return controlResponseBody{}, incompatible()
	}
	if err := validatePendingLists(body.PendingPermissionRequests, body.PendingUserDialogRequests); err != nil {
		return controlResponseBody{}, err
	}
	return body, nil
}

// validatePendingLists admits only declared empty arrays: a pending request
// would be unanswered native interaction state that the handshake cannot own.
func validatePendingLists(lists ...json.RawMessage) error {
	for _, pending := range lists {
		if len(pending) == 0 {
			continue
		}
		var items []json.RawMessage
		if json.Unmarshal(pending, &items) != nil || items == nil || len(items) != 0 {
			return incompatible()
		}
	}
	return nil
}

var rawMessageType = reflect.TypeOf(json.RawMessage(nil))

// requireExactNativeKeys reports whether every object key in raw, at every
// level described by target's struct types, matches a declared json tag with
// exact case. encoding/json otherwise matches keys case-insensitively and
// domain.Decode only detects duplicates with identical spelling, so differently
// cased aliases could shadow canonical keys. Shapes that do not parse as the
// expected container are left for the typed decode to reject.
func requireExactNativeKeys(raw []byte, target any) bool {
	return exactNativeShape(raw, reflect.TypeOf(target))
}

func exactNativeShape(raw []byte, shape reflect.Type) bool {
	for shape.Kind() == reflect.Pointer {
		shape = shape.Elem()
	}
	if shape == rawMessageType {
		return true
	}
	switch shape.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return true
		}
		allowed := make(map[string]reflect.Type, shape.NumField())
		for i := 0; i < shape.NumField(); i++ {
			name, _, _ := strings.Cut(shape.Field(i).Tag.Get("json"), ",")
			allowed[name] = shape.Field(i).Type
		}
		for name, value := range fields {
			fieldShape, declared := allowed[name]
			if !declared || !exactNativeShape(value, fieldShape) {
				return false
			}
		}
	case reflect.Slice:
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return true
		}
		for _, item := range items {
			if !exactNativeShape(item, shape.Elem()) {
				return false
			}
		}
	}
	return true
}

// initializeDescriptorLists names the only initialize-result members whose
// elements are descriptive handshake metadata. Their element objects are the
// sole extension points of the initialization profile; every other object keeps
// the closed exact-key rule.
var initializeDescriptorLists = map[string]reflect.Type{
	"commands": reflect.TypeOf(nativeCommand{}),
	"agents":   reflect.TypeOf(probeAgent{}),
	"models":   reflect.TypeOf(probeModel{}),
}

// projectInitializeDescriptors is the one shared policy for the unauthenticated
// probe, API/subscription and managed-token initializers. It first validates the
// whole result with the global strict decoder (UTF-8, frame bound, one document,
// unique keys and at most 64 nesting levels at every depth, including inside
// members that are later discarded). It then drops every undeclared member of
// each commands, agents and models element object and returns the projected
// bytes, which the unchanged exact-key check and typed decode consume. An
// undeclared key that differs from a declared key only by case is refused, so a
// discarded alias can never shadow a declared field. Nothing outside those
// element objects is relaxed, and dropped names and values are never logged or
// retained.
func projectInitializeDescriptors(raw []byte) ([]byte, error) {
	var document json.RawMessage
	if domain.Decode(raw, &document) != nil {
		return nil, incompatible()
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(raw, &result) != nil || result == nil {
		// Not an object: the typed decode rejects the shape.
		return raw, nil
	}
	for name, shape := range initializeDescriptorLists {
		list, declared := result[name]
		if !declared {
			continue
		}
		var items []json.RawMessage
		if json.Unmarshal(list, &items) != nil || items == nil {
			continue
		}
		for i, item := range items {
			projected, err := projectDescriptor(item, shape)
			if err != nil {
				return nil, err
			}
			items[i] = projected
		}
		result[name] = encodeRawArray(items)
	}
	projected, err := encodeRawObject(result)
	if err != nil || len(projected) > len(raw) {
		// The projection only removes bytes, so it can never exceed the
		// validated frame; growth means a key needed re-escaping.
		return nil, incompatible()
	}
	return projected, nil
}

// encodeRawObject joins members in sorted key order and keeps every value's
// original bytes. Re-encoding values through json.Marshal would escape the
// HTML characters and U+2028/U+2029 and could grow a frame that already passed
// the 1 MiB bound past it, so values are never re-encoded.
func encodeRawObject(members map[string]json.RawMessage) ([]byte, error) {
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	slices.Sort(names)
	var out bytes.Buffer
	out.WriteByte('{')
	for i, name := range names {
		if i > 0 {
			out.WriteByte(',')
		}
		var key bytes.Buffer
		encoder := json.NewEncoder(&key)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(name); err != nil {
			return nil, err
		}
		out.Write(bytes.TrimSuffix(key.Bytes(), []byte("\n")))
		out.WriteByte(':')
		out.Write(members[name])
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// encodeRawArray joins array elements without re-encoding them.
func encodeRawArray(items []json.RawMessage) json.RawMessage {
	var out bytes.Buffer
	out.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			out.WriteByte(',')
		}
		out.Write(item)
	}
	out.WriteByte(']')
	return out.Bytes()
}

func projectDescriptor(raw json.RawMessage, shape reflect.Type) (json.RawMessage, error) {
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil || members == nil {
		return raw, nil
	}
	declared := make(map[string]bool, shape.NumField())
	for i := 0; i < shape.NumField(); i++ {
		name, _, _ := strings.Cut(shape.Field(i).Tag.Get("json"), ",")
		declared[name] = true
	}
	for name := range members {
		if declared[name] {
			continue
		}
		for known := range declared {
			if strings.EqualFold(name, known) {
				return nil, incompatible()
			}
		}
		delete(members, name)
	}
	projected, err := encodeRawObject(members)
	if err != nil {
		return nil, incompatible()
	}
	return projected, nil
}

// probeFrameIsPrelude reports whether a frame claims the startup event
// envelope through its exact lowercase type key. Any other frame is validated as
// the initialize response, which rejects differently cased aliases.
func probeFrameIsPrelude(raw []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	var kind string
	return json.Unmarshal(fields["type"], &kind) == nil && kind == probePreludeType
}

// validateProbePrelude accepts only the closed ui_invalidate startup event. It
// never retains or logs the frame.
func validateProbePrelude(raw []byte) error {
	var prelude probePrelude
	if !requireExactNativeKeys(raw, &prelude) || domain.Decode(raw, &prelude) != nil || prelude.Type != probePreludeType || prelude.Subtype != probePreludeSubtype {
		return incompatible()
	}
	if len(prelude.Event) != 0 {
		var event string
		if json.Unmarshal(prelude.Event, &event) != nil || event != "ui.render" {
			return incompatible()
		}
	}
	for _, value := range []json.RawMessage{prelude.UUID, prelude.SessionID} {
		if len(value) == 0 {
			continue
		}
		var identity *string
		if json.Unmarshal(value, &identity) != nil || identity == nil || domain.Text(*identity, "native startup identity", maxProbeIdentity, true) != nil {
			return incompatible()
		}
	}
	return nil
}

// validateInitializeExtensions checks the 2.1.287 additions. Each field is
// optional so earlier verified profiles keep passing, but a declared field must
// have the exact safe shape; null, wrong types and unsafe values are rejected.
// The pending request lists live in the control-response envelope instead
// (see controlResponseBody).
func validateInitializeExtensions(result initializeResult) error {
	if len(result.AnalyticsDisabled) != 0 && !bytes.Equal(bytes.TrimSpace(result.AnalyticsDisabled), []byte("true")) {
		return incompatible()
	}
	// A managed token can advertise availability while every activation gate
	// remains disabled. Availability is metadata, never permission to connect.
	if len(result.RemoteControlAvailable) != 0 {
		available := bytes.TrimSpace(result.RemoteControlAvailable)
		managed := result.Account != nil && result.Account.TokenSource == managedTokenSource
		if !bytes.Equal(available, []byte("false")) && !(managed && bytes.Equal(available, []byte("true"))) {
			return incompatible()
		}
	}
	for _, gate := range []json.RawMessage{result.RemoteControlAutoConnectDefault} {
		if len(gate) != 0 && !bytes.Equal(bytes.TrimSpace(gate), []byte("false")) {
			return incompatible()
		}
	}
	if len(result.FeedbackMode) != 0 {
		if !validProbeFeedbackMode(result.FeedbackMode) {
			return incompatible()
		}
	}
	if len(result.SessionState) != 0 {
		var state string
		if json.Unmarshal(result.SessionState, &state) != nil || state != "idle" {
			return incompatible()
		}
	}
	if len(result.UserOutputStylesDir) != 0 {
		var dir string
		if json.Unmarshal(result.UserOutputStylesDir, &dir) != nil || domain.Text(dir, "native output styles directory", maxProbeStylesDir, true) != nil {
			return incompatible()
		}
	}
	if len(result.Capabilities) != 0 && !validProbeCapabilities(result.Capabilities) {
		return incompatible()
	}
	return nil
}

// validProbeFeedbackMode accepts the earlier text profile and the verified
// disabled object profile. Neither value grants feedback or execution authority.
func validProbeFeedbackMode(raw json.RawMessage) bool {
	var mode string
	if json.Unmarshal(raw, &mode) == nil {
		return mode == "disabled" || mode == "off"
	}
	var descriptor struct {
		Kind string `json:"kind"`
	}
	return requireExactNativeKeys(raw, &descriptor) && domain.Decode(raw, &descriptor) == nil && descriptor.Kind == "disabled"
}

// validProbeCapabilities admits a bounded list of unique descriptor names or a
// bounded flat object of scalar descriptors. Descriptors are discarded after
// validation and never become authority.
func validProbeCapabilities(raw json.RawMessage) bool {
	switch bytes.TrimSpace(raw)[0] {
	case '[':
		var names []string
		if json.Unmarshal(raw, &names) != nil || names == nil || len(names) > maxProbeCapabilities {
			return false
		}
		seen := map[string]bool{}
		for _, name := range names {
			if seen[name] || domain.Text(name, "native capability", maxProbeCapability, true) != nil {
				return false
			}
			seen[name] = true
		}
		return true
	case '{':
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || len(fields) > maxProbeCapabilities {
			return false
		}
		for key, value := range fields {
			if domain.Text(key, "native capability", maxProbeCapability, true) != nil {
				return false
			}
			var scalar any
			if json.Unmarshal(value, &scalar) != nil {
				return false
			}
			switch typed := scalar.(type) {
			case bool:
			case string:
				if domain.Text(typed, "native capability value", maxProbeCapability, false) != nil {
					return false
				}
			default:
				return false
			}
		}
		return true
	default:
		return false
	}
}
