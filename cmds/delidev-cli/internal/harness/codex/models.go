// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strconv"
)

// ModelList collects one complete native snapshot. Cursors never leave this
// owned runtime, and the caller must join Close before publishing the result.
func (c *Client) ModelList(ctx context.Context, hidden bool) ([]domain.NativeModel, error) {
	if c.mode != ProbeProtocol || c.api != nil || c.modelObservation == "" {
		return nil, domain.NativeModelFailure()
	}
	return c.readModelList(ctx, hidden)
}

// Execution uses this bounded read only for explicitly requested child model
// or effort compatibility. It performs no remote model registration, inference
// or account selection and cannot widen the server's immutable model grant.
func (c *Client) readModelList(ctx context.Context, hidden bool) ([]domain.NativeModel, error) {
	var cursor *string
	cursors := map[string]bool{}
	identities := map[string]bool{}
	models := []domain.NativeModel{}
	bytes := 0
	for pages := 0; pages <= domain.MaxNativeModels; pages++ {
		response, err := c.wire.Call(ctx, domain.NewID(), "model/list", struct {
			Cursor        *string `json:"cursor"`
			Limit         uint32  `json:"limit"`
			IncludeHidden bool    `json:"includeHidden"`
		}{cursor, 200, hidden})
		if err != nil {
			return nil, handshakeError(c.wire, err)
		}
		if response.ErrorCode != nil {
			return nil, domain.NativeModelFailure()
		}
		var page struct {
			Data []struct {
				ID              string          `json:"id"`
				Model           string          `json:"model"`
				Upgrade         *string         `json:"upgrade"`
				UpgradeInfo     json.RawMessage `json:"upgradeInfo"`
				AvailabilityNUX json.RawMessage `json:"availabilityNux"`
				DisplayName     string          `json:"displayName"`
				Description     string          `json:"description"`
				ModelSpecialty  *string         `json:"modelSpecialty"`
				Hidden          bool            `json:"hidden"`
				Reasoning       []struct {
					Effort      string `json:"reasoningEffort"`
					Description string `json:"description"`
				} `json:"supportedReasoningEfforts"`
				DefaultReasoning    string   `json:"defaultReasoningEffort"`
				Modalities          []string `json:"inputModalities"`
				SupportsPersonality bool     `json:"supportsPersonality"`
				MultiAgentVersion   *string  `json:"multiAgentVersion"`
				SpeedTiers          []string `json:"additionalSpeedTiers"`
				ServiceTiers        []struct {
					ID          string `json:"id"`
					Name        string `json:"name"`
					Description string `json:"description"`
				} `json:"serviceTiers"`
				DefaultServiceTier *string `json:"defaultServiceTier"`
				IsDefault          bool    `json:"isDefault"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if domain.Decode(response.Result, &page) != nil || page.Data == nil || len(page.Data) > 200 {
			return nil, modelListFailure("shape")
		}
		bytes += len(response.Result)
		if bytes > domain.MaxNativeModelBytes || len(models)+len(page.Data) > domain.MaxNativeModels {
			return nil, modelListFailure("bounds")
		}
		for _, entry := range page.Data {
			model := domain.NativeModel{ID: entry.ID, Model: entry.Model, DisplayName: entry.DisplayName, Description: entry.Description, Hidden: entry.Hidden, Reasoning: []domain.NativeReasoningEffort{}, DefaultReasoning: domain.NativeReasoningEffort(entry.DefaultReasoning), Modalities: entry.Modalities, ServiceTiers: []string{}, DefaultServiceTier: entry.DefaultServiceTier}
			for _, effort := range entry.Reasoning {
				model.Reasoning = append(model.Reasoning, domain.NativeReasoningEffort(effort.Effort))
			}
			for _, tier := range entry.ServiceTiers {
				model.ServiceTiers = append(model.ServiceTiers, tier.ID)
			}
			if err := model.Validate(); err != nil {
				return nil, err
			}
			if identities[model.ID] || (!hidden && model.Hidden) {
				return nil, modelListFailure("identity")
			}
			identities[model.ID] = true
			models = append(models, model)
		}
		if page.NextCursor == nil {
			return models, nil
		}
		if domain.Text(*page.NextCursor, "native cursor", 4096, true) != nil || cursors[*page.NextCursor] || len(page.Data) == 0 {
			return nil, modelListFailure("cursor")
		}
		cursors[*page.NextCursor] = true
		cursor = page.NextCursor
	}
	return nil, domain.NativeModelFailure()
}

func modelListFailure(phase string) error {
	return domain.Fail(domain.Unsupported, "The native model observation failed its "+phase+" check.", "Refresh the selected Codex installation; no models were registered.")
}

func configureModelObservation(config *Config) (string, error) {
	if !config.ModelObservation {
		return "", nil
	}
	if config.Mode != ProbeProtocol || config.API != nil {
		return "", domain.NativeModelFailure()
	}
	// A fresh provider namespace cannot merge ambient command authentication.
	// The pinned native manager skips remote model refresh for this keyless
	// Responses provider. Its loopback sentinel grants no external authority.
	name := "delidev_models_" + string(config.Process.OwnerID)
	config.Process.Args = append(config.Process.Args, "-c", "model_provider="+strconv.Quote(name), "-c", "model_providers."+name+`={name="DeliDev model observation",base_url="http://127.0.0.1:1",wire_api="responses",requires_openai_auth=false,supports_websockets=false}`)
	return name, nil
}

func (c *Client) verifyModelObservation(ctx context.Context) error {
	if c.modelObservation == "" {
		return nil
	}
	response, err := c.wire.Call(ctx, domain.NewID(), "config/read", struct {
		IncludeLayers bool `json:"includeLayers"`
	}{false})
	if err != nil {
		return handshakeError(c.wire, err)
	}
	if response.ErrorCode != nil {
		return domain.NativeModelFailure()
	}
	var result struct {
		Config  map[string]json.RawMessage `json:"config"`
		Origins json.RawMessage            `json:"origins"`
		Layers  json.RawMessage            `json:"layers"`
	}
	if domain.Decode(response.Result, &result) != nil {
		return domain.NativeModelFailure()
	}
	var selected, store string
	if json.Unmarshal(result.Config["model_provider"], &selected) != nil || selected != c.modelObservation || json.Unmarshal(result.Config["cli_auth_credentials_store"], &store) != nil || store != "ephemeral" {
		return domain.NativeModelFailure()
	}
	var providers map[string]map[string]json.RawMessage
	if json.Unmarshal(result.Config["model_providers"], &providers) != nil {
		return domain.NativeModelFailure()
	}
	provider := providers[c.modelObservation]
	for key, value := range provider {
		if string(value) == "null" {
			continue
		}
		switch key {
		case "name":
			if string(value) != `"DeliDev model observation"` {
				return domain.NativeModelFailure()
			}
		case "base_url":
			if string(value) != `"http://127.0.0.1:1"` {
				return domain.NativeModelFailure()
			}
		case "wire_api":
			if string(value) != `"responses"` {
				return domain.NativeModelFailure()
			}
		case "requires_openai_auth", "supports_websockets":
			if string(value) != "false" {
				return domain.NativeModelFailure()
			}
		case "request_max_retries", "stream_max_retries", "stream_idle_timeout_ms", "websocket_connect_timeout_ms", "supports_standalone_web_search":
		default:
			return domain.NativeModelFailure()
		}
	}
	for _, required := range []string{"name", "base_url", "wire_api", "requires_openai_auth", "supports_websockets"} {
		if _, ok := provider[required]; !ok {
			return domain.NativeModelFailure()
		}
	}
	return nil
}
