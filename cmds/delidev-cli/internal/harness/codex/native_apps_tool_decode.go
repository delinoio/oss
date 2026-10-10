// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// The pinned c138 app-server McpToolCall is decoded privately. Its original
// connector tuple is evidence for the owning caller, never selection authority.
// JSON-valued content stays inert; this decoder performs no resource or UI read.
type decodedNativeAppsTool struct {
	ID, Server, ToolName, AppID string
	Arguments, AppContext       json.RawMessage
	Identity                    []byte
	Status                      ToolStatus
	result                      *domain.NativeAppResult
	errorPresent                bool
	durationMS                  *int64
}

func nativeAppsObject(raw json.RawMessage, required []string, optional ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil || fields == nil {
		return nil, incompatible()
	}
	allowed := map[string]bool{}
	for _, key := range required {
		allowed[key] = true
		if _, ok := fields[key]; !ok {
			return nil, incompatible()
		}
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key := range fields {
		if !allowed[key] {
			return nil, incompatible()
		}
	}
	return fields, nil
}

func canonicalNativeAppsJSON(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&value) != nil {
		return nil, incompatible()
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, incompatible()
	}
	return canonical, nil
}

func nativeAppsNullableString(raw json.RawMessage, max int) bool {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true
	}
	var value string
	return json.Unmarshal(raw, &value) == nil && domain.Text(value, "private native App metadata", max, false) == nil
}

func decodeNativeAppsTool(raw json.RawMessage, completed bool) (*decodedNativeAppsTool, error) {
	fields, err := nativeAppsObject(raw, []string{"type", "id", "server", "tool", "status", "arguments", "appContext", "mcpAppUi", "pluginId", "readOnlyHint", "result", "error", "durationMs"}, "mcpAppResourceUri")
	if err != nil {
		return nil, err
	}
	var item struct {
		Type         string          `json:"type"`
		ID           string          `json:"id"`
		Server       string          `json:"server"`
		Tool         string          `json:"tool"`
		Status       ToolStatus      `json:"status"`
		Arguments    json.RawMessage `json:"arguments"`
		AppContext   json.RawMessage `json:"appContext"`
		ResourceURI  json.RawMessage `json:"mcpAppResourceUri"`
		UI           json.RawMessage `json:"mcpAppUi"`
		PluginID     json.RawMessage `json:"pluginId"`
		ReadOnlyHint json.RawMessage `json:"readOnlyHint"`
		Result       json.RawMessage `json:"result"`
		Error        json.RawMessage `json:"error"`
		DurationMS   *int64          `json:"durationMs"`
	}
	if domain.Decode(raw, &item) != nil || item.Type != "mcpToolCall" || item.Server != "codex_apps" || domain.Text(item.ID, "native App tool identity", 1024, true) != nil || domain.Text(item.Tool, "native App tool name", 1024, true) != nil {
		return nil, incompatible()
	}
	if completed && item.Status != ToolCompleted && item.Status != ToolFailed || !completed && item.Status != ToolRunning {
		return nil, incompatible()
	}
	contextFields, err := nativeAppsObject(item.AppContext, []string{"connectorId", "linkId", "resourceUri", "appName", "actionName"})
	if err != nil {
		return nil, err
	}
	var appID string
	if json.Unmarshal(contextFields["connectorId"], &appID) != nil || !domain.NativeAppIDValid(appID) {
		return nil, incompatible()
	}
	for _, key := range []string{"linkId", "resourceUri", "appName", "actionName"} {
		if !nativeAppsNullableString(contextFields[key], 4096) {
			return nil, incompatible()
		}
	}
	if len(item.ResourceURI) != 0 && !nativeAppsNullableString(item.ResourceURI, 4096) || !nativeAppsNullableString(item.PluginID, 1024) {
		return nil, incompatible()
	}
	if string(bytes.TrimSpace(item.ReadOnlyHint)) != "null" {
		var hint bool
		if json.Unmarshal(item.ReadOnlyHint, &hint) != nil {
			return nil, incompatible()
		}
	}
	if string(bytes.TrimSpace(item.UI)) != "null" {
		ui, err := nativeAppsObject(item.UI, []string{"resourceUri", "preferredModelDisplayMode"})
		if err != nil {
			return nil, err
		}
		var uri, mode string
		if json.Unmarshal(ui["resourceUri"], &uri) != nil || domain.Text(uri, "private native App UI resource", 4096, true) != nil || json.Unmarshal(ui["preferredModelDisplayMode"], &mode) != nil || mode != "inline" && mode != "fullscreen" {
			return nil, incompatible()
		}
	}
	arguments, err := canonicalNativeAppsJSON(item.Arguments)
	if err != nil || len(arguments) > 256<<10 {
		return nil, incompatible()
	}
	context, err := canonicalNativeAppsJSON(item.AppContext)
	if err != nil {
		return nil, err
	}
	decoded := &decodedNativeAppsTool{ID: item.ID, Server: item.Server, ToolName: item.Tool, AppID: appID, Arguments: arguments, AppContext: context, Status: item.Status, durationMS: item.DurationMS}
	if string(bytes.TrimSpace(item.Result)) != "null" {
		resultFields, err := nativeAppsObject(item.Result, []string{"content", "structuredContent", "_meta"})
		if err != nil {
			return nil, err
		}
		var content []json.RawMessage
		if domain.Decode(resultFields["content"], &content) != nil || content == nil || len(content) > 256 {
			return nil, incompatible()
		}
		decoded.result = &domain.NativeAppResult{Content: make([]string, 0, len(content))}
		for _, value := range content {
			canonical, err := canonicalNativeAppsJSON(value)
			if err != nil {
				return nil, err
			}
			decoded.result.Content = append(decoded.result.Content, string(canonical))
		}
		if string(bytes.TrimSpace(resultFields["structuredContent"])) != "null" {
			canonical, err := canonicalNativeAppsJSON(resultFields["structuredContent"])
			if err != nil {
				return nil, err
			}
			value := string(canonical)
			decoded.result.StructuredContent = &value
		}
		// _meta is an arbitrary source JSON value, private even when it contains
		// credentials, resource locations or embedded presentation instructions.
		if len(resultFields["_meta"]) > 256<<10 {
			return nil, incompatible()
		}
	}
	if string(bytes.TrimSpace(item.Error)) != "null" {
		errorFields, err := nativeAppsObject(item.Error, []string{"message"})
		if err != nil {
			return nil, err
		}
		var message string
		if json.Unmarshal(errorFields["message"], &message) != nil || domain.Text(message, "private native App error", 256<<10, false) != nil {
			return nil, incompatible()
		}
		decoded.errorPresent = true
	}
	if decoded.result != nil && decoded.errorPresent {
		return nil, incompatible()
	}
	// Validate cross-state and public inert-result bounds without assuming a name
	// from private metadata. The owner supplies the verified canonical name later.
	if _, err := decoded.projection("native App"); err != nil {
		return nil, err
	}
	identity := map[string]json.RawMessage{}
	for _, key := range []string{"id", "server", "tool", "arguments", "appContext", "mcpAppResourceUri", "mcpAppUi", "pluginId", "readOnlyHint"} {
		value, ok := fields[key]
		if !ok {
			value = json.RawMessage("null")
		}
		canonical, err := canonicalNativeAppsJSON(value)
		if err != nil {
			return nil, err
		}
		identity[key] = canonical
	}
	decoded.Identity, err = json.Marshal(identity)
	if err != nil {
		return nil, incompatible()
	}
	return decoded, nil
}

func (v *decodedNativeAppsTool) projection(canonicalName string) (*domain.NativeAppToolObservation, error) {
	if v == nil {
		return nil, incompatible()
	}
	observation := &domain.NativeAppToolObservation{AppID: v.AppID, Name: canonicalName, ToolName: v.ToolName, ArgumentsPresent: true, ErrorPresent: v.errorPresent, DurationMS: v.durationMS}
	if v.durationMS != nil {
		duration := *v.durationMS
		observation.DurationMS = &duration
	}
	if v.result != nil {
		result := &domain.NativeAppResult{Content: append([]string{}, v.result.Content...)}
		if v.result.StructuredContent != nil {
			value := *v.result.StructuredContent
			result.StructuredContent = &value
		}
		observation.Result = result
	}
	var status domain.ToolStatus
	switch v.Status {
	case ToolRunning:
		status = domain.ToolRunning
	case ToolCompleted:
		status = domain.ToolCompleted
	case ToolFailed:
		status = domain.ToolFailed
	default:
		return nil, incompatible()
	}
	if observation.Validate(status) != nil {
		return nil, incompatible()
	}
	return observation, nil
}

// Settled history validates original shape and bounded inert projection only.
// Selection, provenance, effect admission and native cleanup belong to callers.
func settledNativeAppsTool(raw json.RawMessage) bool {
	_, err := decodeNativeAppsTool(raw, true)
	return err == nil
}
