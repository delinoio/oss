// SPDX-License-Identifier: Apache-2.0
package domain

import "encoding/json"

const NativeAppsTool ToolKind = "native-apps"

// NativeAppResult preserves the pinned native JSON-value result surface as
// bounded inert text in original content order. A string grants no URL fetch,
// resource read, embedded UI, media decoding or connector execution authority.
// Rich private descriptor metadata and raw errors remain in native history.
type NativeAppResult struct {
	Content           []string `json:"content"`
	StructuredContent *string  `json:"structured_content,omitempty"`
}
type NativeAppToolObservation struct {
	AppID            string           `json:"app_id"`
	Name             string           `json:"name"`
	ToolName         string           `json:"tool_name"`
	ArgumentsPresent bool             `json:"arguments_present"`
	Result           *NativeAppResult `json:"result,omitempty"`
	ErrorPresent     bool             `json:"error_present"`
	DurationMS       *int64           `json:"duration_ms,omitempty"`
}

func (v NativeAppToolObservation) Validate(status ToolStatus) error {
	if !NativeAppIDValid(v.AppID) || Text(v.Name, "native app name", 1024, true) != nil || Text(v.ToolName, "native App tool name", 1024, true) != nil || (v.DurationMS != nil && *v.DurationMS < 0) {
		return invalidTool()
	}
	switch status {
	case ToolRunning:
		if v.Result != nil || v.ErrorPresent || v.DurationMS != nil {
			return invalidTool()
		}
	case ToolCompleted:
		if v.Result == nil || v.ErrorPresent {
			return invalidTool()
		}
	case ToolFailed:
		if v.Result == nil && !v.ErrorPresent {
			return invalidTool()
		}
	default:
		return invalidTool()
	}
	if v.Result != nil {
		if v.Result.Content == nil || len(v.Result.Content) > 256 {
			return invalidTool()
		}
		bytes := 0
		for _, value := range v.Result.Content {
			bytes += len(value)
			if Text(value, "inert native App result", 384<<10, false) != nil || !json.Valid([]byte(value)) {
				return invalidTool()
			}
		}
		if value := v.Result.StructuredContent; value != nil {
			bytes += len(*value)
			if Text(*value, "inert native App result", 384<<10, false) != nil || !json.Valid([]byte(*value)) {
				return invalidTool()
			}
		}
		if bytes > 384<<10 {
			return invalidTool()
		}
	}
	return nil
}

// Completion retains the original public identity without acquiring authority
// from a different connector, arguments record or renamed runtime tool.
func SameNativeAppTool(start, terminal *NativeAppToolObservation) bool {
	return start != nil && terminal != nil && start.AppID == terminal.AppID && start.Name == terminal.Name && start.ToolName == terminal.ToolName && start.ArgumentsPresent == terminal.ArgumentsPresent
}
