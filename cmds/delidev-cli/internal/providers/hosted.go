// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func hostedProfile(id domain.ProviderPresetID) profile {
	switch id {
	case domain.PresetGemini:
		return geminiModels
	case domain.PresetMistral:
		return mistralModels
	case domain.PresetTogetherAI:
		return togetherModels
	case domain.PresetFireworksAI:
		return fireworksModels
	case domain.PresetCohere:
		return cohereModels
	case domain.PresetQianfan:
		return qianfanModels
	case domain.PresetSiliconFlow, domain.PresetSiliconFlowCN:
		return siliconFlowModels
	case domain.PresetAlibabaModelStudioInternational, domain.PresetAlibabaModelStudioHongKong:
		return alibabaModels
	case domain.PresetBaseten:
		return basetenModels
	case domain.PresetNovita:
		return novitaModels
	case domain.PresetDeepInfra:
		return deepInfraModels
	case domain.PresetHuggingFace:
		return huggingFaceModels
	case domain.PresetVenice:
		return veniceModels
	default:
		return authenticatedModels
	}
}

// Only compile-time selected authorities can cross from an inference base to
// the documented private/native listing endpoints. No response URL is followed.
func privateVerification(p profile) string {
	switch p {
	case novitaModels:
		return "https://api.novita.ai/openapi/v1/billing/balance/detail"
	case deepInfraModels:
		return "https://api.deepinfra.com/v1/me"
	case huggingFaceModels:
		return "https://huggingface.co/api/whoami-v2"
	case veniceModels:
		return "https://api.venice.ai/api/v1/api_keys/rate_limits"
	}
	return ""
}

func hostedTarget(p profile, base url.URL, page int, token string) (url.URL, inspectionHeader) {
	header := standardHeader
	query := url.Values{}
	base.Path += "/models"
	switch p {
	case geminiModels:
		base.Path = "/v1beta/models"
		header = geminiHeader
		query.Set("pageSize", "1000")
		if token != "" {
			query.Set("pageToken", token)
		}
	case fireworksModels:
		base.Path = "/v1/accounts/fireworks/models"
		query.Set("pageSize", "200")
		if token != "" {
			query.Set("pageToken", token)
		}
	case cohereModels:
		base.Path = "/v1/models"
		query.Set("endpoint", "chat")
		query.Set("page_size", "1000")
		if token != "" {
			query.Set("page_token", token)
		}
	case alibabaModels:
		base.Path = "/api/v1/models"
		query.Set("capabilities", "TG")
		query.Set("page_size", "100")
		query.Set("page_no", strconv.Itoa(page+1))
	case basetenModels:
		fixed, _ := url.Parse("https://api.baseten.co/v1/model_apis")
		base = *fixed
		query.Set("limit", "100")
		if token != "" {
			query.Set("cursor", token)
		}
	case deepInfraModels:
		base.Path = "/v1/models"
	case veniceModels:
		query.Set("type", "text")
	case siliconFlowModels:
		query.Set("sub_type", "chat")
	}
	base.RawQuery = query.Encode()
	return base, header
}

func inspectHosted(ctx context.Context, client *http.Client, provider domain.Provider, key []byte, p profile, base url.URL) Observation {
	o := Observation{Authentication: AuthenticationUnknown, ObservedAt: time.Now().UTC().Truncate(time.Millisecond)}
	remaining := 4 * maxBody
	if verification := privateVerification(p); verification != "" {
		target, _ := url.Parse(verification)
		raw, failure := getURL(ctx, client, provider, key, *target, standardHeader)
		if failure.Failure != NoFailure {
			return failure.withTime(o.ObservedAt).withDiagnostic(CredentialCheckStage, RequestFailedReason)
		}
		remaining -= len(raw)
		problem := verifyHostedCredential(raw, p)
		clear(raw)
		if problem != NoFailure {
			o.Failure = problem
			o.HTTPStatus = http.StatusOK
			return o.withDiagnostic(CredentialCheckStage, CredentialResponseReason)
		}
		o.Authentication = CredentialAccepted
	}
	models := []Model{}
	seenModels := map[string]bool{}
	seenTokens := map[string]bool{}
	token := ""
	received := 0
	var total *uint32
	for page := 0; page < maxPages; page++ {
		target, header := hostedTarget(p, base, page, token)
		raw, failure := getURL(ctx, client, provider, key, target, header)
		if failure.Failure != NoFailure {
			failure.Authentication = o.Authentication
			return failure.withTime(o.ObservedAt).withDiagnostic(ModelCatalogStage, RequestFailedReason)
		}
		remaining -= len(raw)
		o.HTTPStatus = http.StatusOK
		if remaining < 0 {
			clear(raw)
			o.Failure = ResponseTooLarge
			return o.withDiagnostic(ModelCatalogStage, CatalogLimitReason)
		}
		parsed, err := parseHostedPage(raw, p, page, received, key)
		clear(raw)
		if err != nil {
			o.Failure = InvalidResponse
			return o.withDiagnostic(ModelCatalogStage, hostedParseReason(err))
		}
		received += parsed.received
		if received > maxModels {
			o.Failure = ResponseTooLarge
			return o.withDiagnostic(ModelCatalogStage, CatalogLimitReason)
		}
		if parsed.total != nil {
			if total != nil && *total != *parsed.total {
				o.Failure = InvalidResponse
				return o.withDiagnostic(ModelCatalogStage, CatalogPaginationReason)
			}
			value := *parsed.total
			total = &value
		}
		for _, id := range parsed.identities {
			if seenModels[id] {
				o.Failure = InvalidResponse
				return o.withDiagnostic(ModelCatalogStage, DuplicateModelReason)
			}
			seenModels[id] = true
		}
		models = append(models, parsed.models...)
		// Filtered entries also consume the complete inventory budget. A token is
		// source data only, never a URL or authority, and every cycle is rejected.
		if parsed.next == "" {
			slices.SortFunc(models, func(a, b Model) int { return strings.Compare(a.ID, b.ID) })
			o.Models = models
			o.Authentication = CredentialAccepted
			return o
		}
		if seenTokens[parsed.next] || !boundedToken(parsed.next, key) || parsed.received == 0 {
			o.Failure = InvalidResponse
			return o.withDiagnostic(ModelCatalogStage, CatalogPaginationReason)
		}
		seenTokens[parsed.next] = true
		token = parsed.next
	}
	o.Failure = ResponseTooLarge
	return o.withDiagnostic(ModelCatalogStage, CatalogLimitReason)
}

// Classify parser failures without retaining or logging provider-controlled text.
func hostedParseReason(err error) InspectionReason {
	if err == nil {
		return ""
	}
	switch err.Error() {
	case "duplicate model identity":
		return DuplicateModelReason
	case "invalid pagination token", "invalid model page metadata", "missing model cursor", "incomplete model page", "unsupported model pagination":
		return CatalogPaginationReason
	case "oversized model page":
		return CatalogLimitReason
	case "invalid model array", "trailing model array", "invalid models success", "invalid model list":
		return ModelResponseReason
	default:
		return modelParseReason(err)
	}
}

func verifyHostedCredential(raw []byte, p profile) Failure {
	fields, err := object(raw)
	if err != nil {
		return InvalidResponse
	}
	switch p {
	case novitaModels:
		for _, name := range []string{"availableBalance", "cashBalance", "creditLimit", "pendingCharges", "outstandingInvoices"} {
			var value string
			if json.Unmarshal(fields[name], &value) != nil || !decimalString(value) {
				return InvalidResponse
			}
		}
	case deepInfraModels:
		if !privateString(fields["uid"]) {
			return InvalidResponse
		}
	case huggingFaceModels:
		if !privateString(fields["id"]) || !privateString(fields["name"]) {
			return InvalidResponse
		}
		var kind string
		if json.Unmarshal(fields["type"], &kind) != nil || !slices.Contains([]string{"user", "org", "app"}, kind) {
			return InvalidResponse
		}
		auth, err := object(fields["auth"])
		if err != nil {
			return InvalidResponse
		}
		if json.Unmarshal(auth["type"], &kind) != nil || !slices.Contains([]string{"access_token", "app_token", "app_token_as_user"}, kind) {
			return InvalidResponse
		}
	case veniceModels:
		data, err := object(fields["data"])
		if err != nil {
			return InvalidResponse
		}
		permitted, err := requiredBool(data["accessPermitted"])
		if err != nil {
			return InvalidResponse
		}
		if !permitted {
			return AccessDenied
		}
	default:
		return InvalidResponse
	}
	// All identity, financial and optional token fields are discarded. A private
	// authentication response never supplies usage, quota or pricing evidence.
	return NoFailure
}

func privateString(raw []byte) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && len(value) > 0 && len(value) <= 256 && utf8.ValidString(value) && strings.TrimSpace(value) != "" && strings.IndexFunc(value, unicode.IsControl) < 0
}
func decimalString(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	if value[0] == '-' {
		value = value[1:]
	}
	if len(value) == 0 {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}
func boundedToken(value string, key []byte) bool {
	return len(value) > 0 && len(value) <= 4096 && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0 && !containsKey(value, key)
}
func requiredBool(raw []byte) (bool, error) {
	var v bool
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &v) != nil {
		return false, errors.New("invalid boolean")
	}
	return v, nil
}
func optionalBool(raw []byte) (*bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	v, e := requiredBool(raw)
	if e != nil {
		return nil, e
	}
	return &v, nil
}
func requiredText(raw []byte) (string, error) {
	var value string
	if json.Unmarshal(raw, &value) != nil || len(value) == 0 || len(value) > 256 || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("invalid text")
	}
	return value, nil
}
func tokenField(raw []byte, key []byte) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || value != "" && !boundedToken(value, key) {
		return "", errors.New("invalid pagination token")
	}
	return value, nil
}
func stringList(raw []byte) ([]string, error) {
	var values []string
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil || values == nil || len(values) > 256 {
		return nil, errors.New("invalid string list")
	}
	seen := map[string]bool{}
	for _, value := range values {
		if len(value) == 0 || len(value) > 256 || strings.IndexFunc(value, unicode.IsControl) >= 0 || seen[value] {
			return nil, errors.New("invalid string list")
		}
		seen[value] = true
	}
	return values, nil
}

type hostedPage struct {
	models     []Model
	identities []string
	received   int
	next       string
	total      *uint32
}

func parseHostedPage(raw []byte, p profile, page, offset int, key []byte) (hostedPage, error) {
	result := hostedPage{}
	var fields map[string]json.RawMessage
	var values []json.RawMessage
	arrayField := "data"
	idField, nameField, contextField := "id", "name", "context_length"
	bound := maxModels
	switch p {
	case togetherModels:
		// The official Together response is an array, still using the same strict
		// UTF-8/duplicate/nesting/trailing-document validation as object profiles.
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if !utf8.Valid(raw) || uniqueValue(decoder, 0) != nil {
			return result, errors.New("invalid model array")
		}
		if _, err := decoder.Token(); err != io.EOF {
			return result, errors.New("trailing model array")
		}
		if json.Unmarshal(raw, &values) != nil || values == nil {
			return result, errors.New("invalid model array")
		}
		nameField = "display_name"
	case geminiModels:
		arrayField, idField, nameField, contextField = "models", "name", "displayName", "inputTokenLimit"
		bound = 1000
	case fireworksModels:
		arrayField, idField, nameField, contextField = "models", "name", "displayName", "contextLength"
		bound = 200
	case cohereModels:
		arrayField, idField, nameField = "models", "name", ""
		bound = 1000
	case basetenModels:
		arrayField, idField, nameField = "items", "name", "display_name"
		bound = 100
	case alibabaModels:
		arrayField, idField = "models", "model"
		bound = 100
	}
	if p != togetherModels {
		var err error
		fields, err = object(raw)
		if err != nil {
			return result, err
		}
		if p == alibabaModels {
			success, err := requiredBool(fields["success"])
			if err != nil || !success {
				return result, errors.New("invalid models success")
			}
			fields, err = object(fields["output"])
			if err != nil {
				return result, err
			}
			var total, pageNumber, pageSize uint32
			if json.Unmarshal(fields["total"], &total) != nil || string(fields["total"]) == "null" || total > maxModels || json.Unmarshal(fields["page_no"], &pageNumber) != nil || int(pageNumber) != page+1 || json.Unmarshal(fields["page_size"], &pageSize) != nil || pageSize != 100 {
				return result, errors.New("invalid model page metadata")
			}
			result.total = &total
		}
		if json.Unmarshal(fields[arrayField], &values) != nil || values == nil {
			return result, errors.New("invalid model list")
		}
	}
	if len(values) > bound {
		return result, errors.New("oversized model page")
	}
	result.received = len(values)
	seen := map[string]bool{}
	for _, value := range values {
		item, err := object(value)
		if err != nil {
			return result, err
		}
		model, err := normalizeModel(item, idField, nameField, contextField, key)
		if err != nil {
			return result, err
		}
		keep := true
		switch p {
		case geminiModels:
			if !strings.HasPrefix(model.ID, "models/") {
				return result, errors.New("invalid Gemini model name")
			}
			model.ID = strings.TrimPrefix(model.ID, "models/")
			if len(item["displayName"]) == 0 || string(item["displayName"]) == "null" {
				model.Name = model.ID
			}
			if !modelID(model.ID) || containsKey(model.ID, key) {
				return result, errors.New("invalid Gemini inference ID")
			}
			methods, err := stringList(item["supportedGenerationMethods"])
			if err != nil {
				return result, err
			}
			keep = slices.Contains(methods, "generateContent")
			model.Reasoning, err = optionalBool(item["thinking"])
			if err != nil {
				return result, err
			}
		case togetherModels, qianfanModels:
			kind, err := requiredText(item["type"])
			if err != nil {
				return result, err
			}
			keep = kind == "chat"
			if p == qianfanModels {
				keep = keep && slices.Contains(model.OutputModalities, "text")
			}
		case fireworksModels:
			state, err := requiredText(item["state"])
			if err != nil {
				return result, err
			}
			kind, err := requiredText(item["kind"])
			if err != nil {
				return result, err
			}
			serverless, err := optionalBool(item["supportsServerless"])
			if err != nil {
				return result, err
			}
			keep = state == "READY" && kind == "HF_BASE_MODEL" && serverless != nil && *serverless
			model.Tools, err = optionalBool(item["supportsTools"])
			if err != nil {
				return result, err
			}
			image, err := optionalBool(item["supportsImageInput"])
			if err != nil {
				return result, err
			}
			if image != nil {
				model.InputModalities = []string{"text"}
				if *image {
					model.InputModalities = append(model.InputModalities, "image")
				}
			}
		case cohereModels:
			deprecated, err := optionalBool(item["is_deprecated"])
			if err != nil {
				return result, err
			}
			keep = deprecated == nil || !*deprecated
			if endpoints, ok := item["endpoints"]; ok && string(endpoints) != "null" {
				values, err := stringList(endpoints)
				if err != nil {
					return result, err
				}
				keep = keep && slices.Contains(values, "chat")
			}
		case mistralModels:
			if capabilities, ok := item["capabilities"]; ok && string(capabilities) != "null" {
				fields, err := object(capabilities)
				if err != nil {
					return result, err
				}
				chat, err := optionalBool(fields["completion_chat"])
				if err != nil {
					return result, err
				}
				keep = chat == nil || *chat
				model.Tools, err = optionalBool(fields["function_calling"])
				if err != nil {
					return result, err
				}
			}
		case alibabaModels:
			if info, ok := item["model_info"]; ok && string(info) != "null" {
				fields, err := object(info)
				if err != nil {
					return result, err
				}
				normalized, err := normalizeModel(map[string]json.RawMessage{"id": item["model"], "context_length": fields["context_window"]}, "id", "", "context_length", key)
				if err != nil {
					return result, err
				}
				model.ContextLimit = normalized.ContextLimit
			}
			if features, ok := item["features"]; ok && string(features) != "null" {
				values, err := stringList(features)
				if err != nil {
					return result, err
				}
				tools := slices.Contains(values, "function-calling")
				model.Tools = &tools
			}
		case deepInfraModels:
			if metadata, ok := item["metadata"]; ok && string(metadata) != "null" {
				fields, err := object(metadata)
				if err != nil {
					return result, err
				}
				normalized, err := normalizeModel(map[string]json.RawMessage{"id": item["id"], "context_length": fields["context_length"]}, "id", "", "context_length", key)
				if err != nil {
					return result, err
				}
				model.ContextLimit = normalized.ContextLimit
			}
		}
		// Duplicate identities fail even when one of the copies was filtered out.
		if seen[model.ID] {
			return result, errors.New("duplicate model identity")
		}
		seen[model.ID] = true
		result.identities = append(result.identities, model.ID)
		if keep {
			result.models = append(result.models, model)
		}
	}
	var err error
	switch p {
	case geminiModels, fireworksModels:
		result.next, err = tokenField(fields["nextPageToken"], key)
	case cohereModels:
		result.next, err = tokenField(fields["next_page_token"], key)
	case basetenModels:
		pagination, e := object(fields["pagination"])
		if e != nil {
			return result, e
		}
		more, e := requiredBool(pagination["has_more"])
		if e != nil {
			return result, e
		}
		next, e := tokenField(pagination["cursor"], key)
		if e != nil {
			return result, e
		}
		if more {
			if next == "" {
				return result, errors.New("missing model cursor")
			}
			result.next = next
		}
	case alibabaModels:
		reached := offset + len(values)
		if int(*result.total) < reached || int(*result.total) > reached && len(values) != 100 {
			return result, errors.New("incomplete model page")
		}
		if int(*result.total) > reached {
			result.next = strconv.Itoa(page + 2)
		}
	default:
		if len(fields["has_more"]) > 0 {
			more, e := requiredBool(fields["has_more"])
			if e != nil || more {
				return result, errors.New("unsupported model pagination")
			}
		}
	}
	return result, err
}
