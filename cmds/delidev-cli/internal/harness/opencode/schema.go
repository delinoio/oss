package opencode

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func object(raw []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil || fields == nil {
		return nil, incompatible()
	}
	return fields, nil
}
func scalar(raw json.RawMessage, want string) bool {
	var value *string
	return json.Unmarshal(raw, &value) == nil && value != nil && *value == want
}
func validateHealth(raw []byte) error {
	fields, err := object(raw)
	if err != nil || len(fields) != 2 || !scalar(fields["version"], SupportedVersion) {
		return incompatible()
	}
	var healthy *bool
	if json.Unmarshal(fields["healthy"], &healthy) != nil || healthy == nil || !*healthy {
		return incompatible()
	}
	return nil
}
func validateEmptyConfig(raw []byte) error {
	fields, err := object(raw)
	if err != nil || len(fields) != 0 {
		return incompatible()
	}
	return nil
}

type operationProfile struct {
	path    string
	method  string
	id      string
	status  string
	content string
}

var operations = []operationProfile{
	{"/global/health", "get", "global.health", "200", "application/json"},
	{"/global/config", "get", "global.config.get", "200", "application/json"},
	{"/global/event", "get", "global.event", "200", "text/event-stream"},
	{"/session", "get", "session.list", "200", "application/json"},
	{"/session", "post", "session.create", "200", "application/json"},
	{"/session/status", "get", "session.status", "200", "application/json"},
	{"/session/{sessionID}", "get", "session.get", "200", "application/json"},
	{"/session/{sessionID}/message", "get", "session.messages", "200", "application/json"},
	{"/session/{sessionID}/prompt_async", "post", "session.prompt_async", "204", ""},
	{"/session/{sessionID}/fork", "post", "session.fork", "200", "application/json"},
	{"/session/{sessionID}/abort", "post", "session.abort", "200", "application/json"},
	{"/question", "get", "question.list", "200", "application/json"},
	{"/question/{requestID}/reply", "post", "question.reply", "200", "application/json"},
	{"/question/{requestID}/reject", "post", "question.reject", "200", "application/json"},
	{"/permission", "get", "permission.list", "200", "application/json"},
	{"/permission/{requestID}/reply", "post", "permission.reply", "200", "application/json"},
	{"/event", "get", "event.subscribe", "200", "text/event-stream"},
}

// This checks static operation advertisements only. Session semantics, event
// schemas, applied execution settings and actual API calls need their separate
// adapters; neither OpenAPI security metadata nor this document proves auth.
func validateSchema(raw []byte) error {
	return validateSchemaOperations(raw, operations)
}

func validateSchemaOperations(raw []byte, required []operationProfile) error {
	schema, err := object(raw)
	if err != nil || len(schema) != 6 || !scalar(schema["openapi"], "3.1.0") {
		return incompatible()
	}
	info, err := object(schema["info"])
	if err != nil || len(info) != 3 || !scalar(info["title"], "opencode") || !scalar(info["version"], "1.0.0") || !scalar(info["description"], "opencode api") {
		return incompatible()
	}
	if _, err := object(schema["components"]); err != nil {
		return incompatible()
	}
	for _, key := range []string{"security", "tags"} {
		var list []json.RawMessage
		if json.Unmarshal(schema[key], &list) != nil || list == nil {
			return incompatible()
		}
		if key == "security" && len(list) != 0 {
			return incompatible()
		}
	}
	paths, err := object(schema["paths"])
	if err != nil || len(paths) > 256 {
		return incompatible()
	}
	for _, expected := range required {
		path, err := object(paths[expected.path])
		if err != nil {
			return incompatible()
		}
		op, err := object(path[expected.method])
		if err != nil || !scalar(op["operationId"], expected.id) {
			return incompatible()
		}
		responses, err := object(op["responses"])
		if err != nil {
			return incompatible()
		}
		response, err := object(responses[expected.status])
		if err != nil {
			return incompatible()
		}
		if expected.content == "" {
			if _, present := response["content"]; present {
				return incompatible()
			}
		} else {
			content, err := object(response["content"])
			if err != nil || len(content) != 1 {
				return incompatible()
			}
			media, err := object(content[expected.content])
			if err != nil {
				return incompatible()
			}
			if _, err := object(media["schema"]); err != nil {
				return incompatible()
			}
		}
	}
	return nil
}
