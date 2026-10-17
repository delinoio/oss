// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// The installed native image tool selects this separate tool model. Mapping its
// provider-relative spelling does not change the conversation model or account.
const codexImageModel = "gpt-image-2"
const openRouterImageModel = "openai/gpt-image-2"

func openRouterToolError() error {
	return domain.Fail(domain.Unsupported, "The native tool request is outside the selected OpenRouter adapter.", "Keep the original account, tool model and bounded native request; no alternate provider or model is selected.")
}

func prepareOpenRouterToolRequest(raw []byte, scope Scope, operation Operation) ([]byte, error) {
	if !scope.OpenRouterTools || !OpenRouterToolProvider(scope.Provider) || !operation.openRouterTool() {
		return nil, openRouterToolError()
	}
	if _, err := document(raw, secretGuard{}); err != nil {
		return nil, openRouterToolError()
	}
	if operation == StandaloneSearch {
		var request struct {
			ID              string                     `json:"id"`
			Model           string                     `json:"model"`
			Input           json.RawMessage            `json:"input"`
			Commands        map[string]json.RawMessage `json:"commands"`
			Settings        json.RawMessage            `json:"settings"`
			Reasoning       json.RawMessage            `json:"reasoning"`
			MaxOutputTokens *uint64                    `json:"max_output_tokens"`
		}
		if domain.DecodeBounded(raw, &request, maxBody) != nil || request.Model != scope.NativeModel || domain.Text(request.ID, "native search session", 256, true) != nil || len(request.Commands) == 0 || len(request.Commands) > 8 {
			return nil, openRouterToolError()
		}
		for command := range request.Commands {
			if !slices.Contains([]string{"search_query", "image_query", "open", "click", "find", "calculator", "time", "response_length"}, command) {
				return nil, openRouterToolError()
			}
		}
		// The dedicated adapter makes one same-account, same-model Responses
		// request. Its server tools execute remotely; the ordinary Responses relay
		// remains byte-preserving and receives no injected tool definitions.
		commands, _ := json.Marshal(request.Commands)
		input := "Execute these web commands using the supplied search and fetch tools. Return the actual results with source URLs. Resolve references against the supplied recent context. Do not claim an operation succeeded without a tool result.\nCommands:\n" + string(commands)
		if externalJSON(request.Input) {
			input += "\nRecent context:\n" + string(request.Input)
		}
		projected := map[string]any{"model": scope.NativeModel, "input": input, "stream": false, "store": false, "tools": []any{map[string]string{"type": "openrouter:web_search"}, map[string]string{"type": "openrouter:web_fetch"}}, "tool_choice": "required"}
		if request.MaxOutputTokens != nil {
			if *request.MaxOutputTokens == 0 || *request.MaxOutputTokens > 32768 {
				return nil, openRouterToolError()
			}
			projected["max_output_tokens"] = *request.MaxOutputTokens
		}
		if externalJSON(request.Reasoning) {
			projected["reasoning"] = request.Reasoning
		}
		return marshalOpenRouterToolRequest(projected)
	}
	var request struct {
		Model      string  `json:"model"`
		Prompt     string  `json:"prompt"`
		Background *string `json:"background"`
		Quality    *string `json:"quality"`
		Size       *string `json:"size"`
		N          *uint64 `json:"n"`
		Images     []struct {
			URL string `json:"image_url"`
		} `json:"images"`
	}
	if domain.DecodeBounded(raw, &request, maxBody) != nil || request.Model != codexImageModel || domain.Text(request.Prompt, "native image prompt", domain.MaxPromptBytes, true) != nil || request.N != nil && *request.N != 1 || request.Background != nil && !slices.Contains([]string{"opaque", "transparent", "auto"}, *request.Background) || request.Quality != nil && !slices.Contains([]string{"auto", "low", "medium", "high"}, *request.Quality) || request.Size != nil && !slices.Contains([]string{"auto", "1024x1024", "1024x1536", "1536x1024"}, *request.Size) {
		return nil, openRouterToolError()
	}
	if operation == ImageCreate && request.Images != nil || operation == ImageEdit && (len(request.Images) == 0 || len(request.Images) > 5) {
		return nil, openRouterToolError()
	}
	projected := map[string]any{"model": openRouterImageModel, "prompt": request.Prompt, "n": 1, "output_format": "png", "stream": false}
	if request.Background != nil {
		projected["background"] = *request.Background
	}
	if request.Quality != nil {
		projected["quality"] = *request.Quality
	}
	if request.Size != nil && *request.Size != "auto" {
		projected["size"] = *request.Size
	}
	if operation == ImageEdit {
		images := make([]any, 0, len(request.Images))
		for _, image := range request.Images {
			// Only original inline native bytes may reach the image router. A
			// remote locator or file ID must never create a new retrieval owner.
			media, data, ok := strings.Cut(image.URL, ";base64,")
			if !ok || !strings.HasPrefix(media, "data:") {
				return nil, openRouterToolError()
			}
			typeID := domain.ImageMediaType(strings.TrimPrefix(media, "data:"))
			decoded, err := base64.StdEncoding.Strict().DecodeString(data)
			if err != nil || domain.ValidateImageContent(decoded, typeID) != nil {
				clear(decoded)
				return nil, openRouterToolError()
			}
			clear(decoded)
			images = append(images, map[string]any{"type": "image_url", "image_url": map[string]string{"url": image.URL}})
		}
		projected["input_references"] = images
	}
	return marshalOpenRouterToolRequest(projected)
}

// Projection embeds native JSON context in a JSON string, which can expand
// escaped characters. Apply the outbound bound after serialization and before
// provider credential access, rather than relying on the inbound frame size.
func marshalOpenRouterToolRequest(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxBody {
		clear(raw)
		return nil, openRouterToolError()
	}
	return raw, nil
}

func externalJSON(raw json.RawMessage) bool { return len(raw) != 0 && string(raw) != "null" }

func projectOpenRouterToolResponse(raw []byte, operation Operation) ([]byte, error) {
	if operation == StandaloneSearch {
		var response struct {
			Status string `json:"status"`
			Output []struct {
				Type       string          `json:"type"`
				Status     string          `json:"status"`
				Error      json.RawMessage `json:"error"`
				HTTPStatus *int            `json:"httpStatus"`
				Content    json.RawMessage `json:"content"`
			} `json:"output"`
		}
		if json.Unmarshal(raw, &response) != nil || response.Status != "completed" {
			return nil, openRouterToolError()
		}
		var text strings.Builder
		completedTool := false
		for _, item := range response.Output {
			// Accept only completed native or router web operations from the
			// authenticated response output. Assistant prose and function-call
			// suggestions do not establish that a remote tool executed.
			if slices.Contains([]string{"web_search_call", "openrouter:web_search", "openrouter:web_fetch"}, item.Type) {
				if item.Status != "completed" || externalJSON(item.Error) || item.HTTPStatus != nil && *item.HTTPStatus >= 400 {
					return nil, openRouterToolError()
				}
				completedTool = true
			}
			if item.Type == "message" {
				var content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if json.Unmarshal(item.Content, &content) != nil {
					return nil, openRouterToolError()
				}
				for _, part := range content {
					if part.Type == "output_text" {
						text.WriteString(part.Text)
						text.WriteByte('\n')
					}
				}
			}
		}
		if !completedTool || text.Len() == 0 || domain.Text(text.String(), "native web output", maxBody, true) != nil {
			return nil, openRouterToolError()
		}
		return json.Marshal(map[string]any{"output": text.String(), "results": nil, "encrypted_output": nil})
	}
	var response struct {
		Created *uint64 `json:"created"`
		Data    []struct {
			Base64    string  `json:"b64_json"`
			MediaType *string `json:"media_type"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Created == nil || len(response.Data) != 1 || response.Data[0].MediaType != nil && *response.Data[0].MediaType != "image/png" {
		return nil, openRouterToolError()
	}
	bytes, err := base64.StdEncoding.Strict().DecodeString(response.Data[0].Base64)
	if err != nil || domain.ValidateImageContent(bytes, domain.ImagePNG) != nil {
		clear(bytes)
		return nil, openRouterToolError()
	}
	clear(bytes)
	return json.Marshal(map[string]any{"created": *response.Created, "data": []any{map[string]string{"b64_json": response.Data[0].Base64}}})
}

// Retain only closed shape facts when a provider cannot satisfy the native
// tool contract. Provider text, commands, arguments and locators stay private.
func openRouterToolResponseShape(raw []byte) (status, incomplete string, messages, textParts, toolCalls int) {
	var response struct {
		Status     string `json:"status"`
		Incomplete struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
			} `json:"content"`
		} `json:"output"`
	}
	_ = json.Unmarshal(raw, &response)
	status = "unknown"
	if slices.Contains([]string{"completed", "incomplete", "in_progress", "failed", "queued", "cancelled"}, response.Status) {
		status = response.Status
	}
	incomplete = "unknown"
	if slices.Contains([]string{"max_output_tokens", "content_filter"}, response.Incomplete.Reason) {
		incomplete = response.Incomplete.Reason
	}
	for _, item := range response.Output {
		if item.Type == "message" {
			messages++
		}
		if slices.Contains([]string{"function_call", "web_search_call", "web_fetch_call", "server_tool_call"}, item.Type) {
			toolCalls++
		}
		for _, part := range item.Content {
			if part.Type == "output_text" {
				textParts++
			}
		}
	}
	return
}
