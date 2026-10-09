package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type catalogReceipt struct {
	ID          domain.ID                 `json:"id"`
	Observation domain.CatalogObservation `json:"observation"`
}

// Bump this version when provider inventory cursor keys or ordering change so
// unexpired cursors from older binaries are rejected instead of reinterpreted.
const providerInventoryCursorScopeVersion = "provider-inventory-v3"

func (s *Service) ListProviderPresets(ctx context.Context, req *connect.Request[pb.ListProviderPresetsRequest]) (*connect.Response[pb.ListProviderPresetsResponse], error) {
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return tx.Authorize() }); err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	// Keep the legacy editable preset payload compatible with older desktop
	// clients. Preset provenance is carried by provider inventory activation,
	// while the provider defaults remain plain editable configuration here.
	legacyPresets := providers.Presets()
	for i := range legacyPresets {
		legacyPresets[i].Provider.PresetID = nil
		legacyPresets[i].Provider.APIFormats = nil
	}
	raw, _ := json.Marshal(legacyPresets)
	response := connect.NewResponse(&pb.ListProviderPresetsResponse{PresetsJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) ListProviderInventory(ctx context.Context, req *connect.Request[pb.ListProviderInventoryRequest]) (*connect.Response[pb.ListProviderInventoryResponse], error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Only an owner or paired client can inspect provider inventory.", "Use an authorized product client."), req.Header().Get(rpc.CorrelationHeader))
	}
	f := store.ProviderInventorySearch{Query: req.Msg.Query, EnabledOnly: req.Msg.EnabledOnly, Limit: int(req.Msg.PageSize)}
	if f.Limit == 0 {
		f.Limit = 50
	}
	raw, _ := json.Marshal(f)
	hash := sha256.Sum256(raw)
	scope := providerInventoryCursorScopeVersion + ":" + hex.EncodeToString(hash[:])
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
		}
		f.After, f.Epoch = string(cursor.After), cursor.Sequence
	}
	entries, more, epoch, err := s.Store.ProviderInventoryPage(ctx, orderedProviderPresets(), f)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	message := &pb.ListProviderInventoryResponse{Capabilities: []pb.ProviderInventoryCapability{
		pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_PROVIDER_ACTIVATION,
		pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACTIVE_API_MODEL_FILTER,
		pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_PROVIDER_FILTER,
		pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_TYPE_FILTER,
		pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_API_PROTOCOL_V1,
		pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_OAUTH_API_PROTOCOL_V1,
		pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_API_FORMAT_CHANGE_V1,
		pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_OPENROUTER_OAUTH_PKCE_V1,
	}}
	for i, entry := range entries {
		capabilities := len(message.Capabilities)
		wire := &pb.ProviderInventoryEntry{PresetId: wireProviderPreset(entry.PresetID), ProviderId: string(entry.ProviderID), DisplayName: entry.DisplayName, Enabled: entry.Enabled, TotalAccounts: entry.TotalAccounts, ConnectedAccounts: entry.ConnectedAccounts, AccountCountsAvailable: entry.AccountCountsAvailable}
		wire.ConnectionMethod = pb.ProviderConnectionMethod_PROVIDER_CONNECTION_METHOD_API_KEY
		if entry.Provider != nil {
			p, e := store.Decode[domain.Provider](*entry.Provider)
			if e != nil || p.Validate() != nil {
				return nil, rpc.Error(domain.Fail(domain.RecoveryRequired, "Provider connection ownership is invalid.", "Inspect the current saved provider."), req.Header().Get(rpc.CorrelationHeader))
			}
			if p.Authentication == domain.KeylessAuth {
				wire.ConnectionMethod = pb.ProviderConnectionMethod_PROVIDER_CONNECTION_METHOD_KEYLESS
			}
			if profile, e := s.oauthProfile(p); e == nil {
				wire.ConnectionMethod = pb.ProviderConnectionMethod_PROVIDER_CONNECTION_METHOD_OAUTH_PKCE
				if profile.preset == domain.PresetBaseten {
					wire.ConnectionMethod = pb.ProviderConnectionMethod_PROVIDER_CONNECTION_METHOD_OAUTH_DEVICE
				}
				if profile.preset != domain.PresetOpenRouter {
					found := false
					for _, capability := range message.Capabilities {
						if capability == pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_OAUTH_V1 {
							found = true
						}
					}
					if !found {
						message.Capabilities = append(message.Capabilities, pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_OAUTH_V1)
					}
				}
			}
			wire.Provider = rpc.Resource(*entry.Provider)
			for _, profile := range providers.APIFormats(p) {
				wire.ApiFormats = append(wire.ApiFormats, rpc.WireAPIFormat(profile))
			}
		} else if entry.PresetID != nil {
			if p, ok := providerPresetDefaults(*entry.PresetID); ok {
				for _, profile := range providers.APIFormats(p) {
					wire.ApiFormats = append(wire.ApiFormats, rpc.WireAPIFormat(profile))
				}
			}
			if p, ok := providerPresetDefaults(*entry.PresetID); ok && p.Authentication == domain.KeylessAuth {
				wire.ConnectionMethod = pb.ProviderConnectionMethod_PROVIDER_CONNECTION_METHOD_KEYLESS
			}
		}
		message.Entries = append(message.Entries, wire)
		message.NextPageToken = ""
		if more || i+1 < len(entries) {
			message.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: domain.ID(entry.CursorKey()), Sequence: epoch})
			if err != nil {
				return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
			}
		}
		fits, e := resourcePageFits(message)
		if e != nil {
			return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
		}
		if !fits {
			message.Entries = message.Entries[:len(message.Entries)-1]
			message.Capabilities = message.Capabilities[:capabilities]
			if len(message.Entries) == 0 {
				return nil, rpc.Error(resourcePageTooLarge(), req.Header().Get(rpc.CorrelationHeader))
			}
			more = true
			break
		}
	}
	if more && len(message.Entries) > 0 {
		message.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: domain.ID(entries[len(message.Entries)-1].CursorKey()), Sequence: epoch})
		if err != nil {
			return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
		}
	}
	response := connect.NewResponse(message)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func orderedProviderPresets() []domain.ProviderPreset { return providers.Presets() }

func wireProviderPreset(id *domain.ProviderPresetID) pb.ProviderPresetId {
	if id == nil {
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_UNSPECIFIED
	}
	switch *id {
	case domain.PresetVercel:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_VERCEL_AI_GATEWAY
	case domain.PresetOpenRouter:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_OPENROUTER
	case domain.PresetOpenAI:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_OPENAI
	case domain.PresetAnthropic:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_ANTHROPIC
	case domain.PresetXAI:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_XAI
	case domain.PresetDeepSeek:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_DEEPSEEK
	case domain.PresetOllama:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_OLLAMA
	case domain.PresetLMStudio:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_LM_STUDIO
	case domain.PresetGemini:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_GEMINI
	case domain.PresetGroq:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_GROQ
	case domain.PresetMistral:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_MISTRAL
	case domain.PresetTogetherAI:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_TOGETHER_AI
	case domain.PresetFireworksAI:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_FIREWORKS_AI
	case domain.PresetPerplexity:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_PERPLEXITY
	case domain.PresetCohere:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_COHERE
	case domain.PresetCerebras:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_CEREBRAS
	case domain.PresetNebius:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_NEBIUS
	case domain.PresetNovita:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_NOVITA
	case domain.PresetDeepInfra:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_DEEPINFRA
	case domain.PresetHuggingFace:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_HUGGING_FACE
	case domain.PresetVenice:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_VENICE
	case domain.PresetScaleway:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_SCALEWAY
	case domain.PresetBaseten:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_BASETEN
	case domain.PresetMoonshot:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_MOONSHOT
	case domain.PresetMoonshotCN:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_MOONSHOT_CN
	case domain.PresetMiniMax:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_MINIMAX
	case domain.PresetMiniMaxCN:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_MINIMAX_CN
	case domain.PresetSiliconFlow:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_SILICONFLOW
	case domain.PresetSiliconFlowCN:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_SILICONFLOW_CN
	case domain.PresetQianfan:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_QIANFAN
	case domain.PresetTencentTokenHub:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_TENCENT_TOKENHUB
	case domain.PresetTencentTokenHubInternational:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_TENCENT_TOKENHUB_INTERNATIONAL
	case domain.PresetAlibabaModelStudioInternational:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_ALIBABA_MODEL_STUDIO_INTERNATIONAL
	case domain.PresetAlibabaModelStudioHongKong:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_ALIBABA_MODEL_STUDIO_HONG_KONG
	case domain.PresetVLLM:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_VLLM
	default:
		return pb.ProviderPresetId_PROVIDER_PRESET_ID_UNSPECIFIED
	}
}
func (s *Service) DiscoverModels(ctx context.Context, req *connect.Request[pb.DiscoverModelsRequest]) (*connect.Response[pb.DiscoverModelsResponse], error) {
	return nil, rpc.Error(domain.Fail(domain.Unsupported, "Persistent Model discovery is retired.", "Use read-only endpoint suggestions for the selected original account."), req.Header().Get(rpc.CorrelationHeader))
}

func (s *Service) discoverModels(ctx context.Context, meta *pb.Mutation, correlation string) (store.Result, error) {
	return store.Result{}, domain.Fail(domain.Unsupported, "Persistent Model discovery is retired.", "Use read-only endpoint suggestions through the original account.")
}

type modelChange struct {
	record store.Record
	model  domain.Model
}

func modelAdvisoryBytes(model domain.Model) string {
	// Compare only evidence-bearing fields. Display preferences and explicitly
	// configured harness compatibility are not observations from the provider.
	raw, _ := json.Marshal(struct {
		ContextLimit     *uint64
		Input, Output    []string
		Tools, Reasoning *bool
	}{model.ContextLimit, model.InputModalities, model.OutputModalities, model.Tools, model.Reasoning})
	return string(raw)
}

func catalogPlan(tx *store.Tx, provider domain.ID, result accountInspection) ([]modelChange, error) {
	existing, err := tx.ModelsForProvider(provider)
	if err != nil {
		return nil, err
	}
	if err = store.CatalogBound(len(existing)); err != nil {
		return nil, err
	}
	byNative := map[string]store.Record{}
	for _, record := range existing {
		model, err := store.Decode[domain.Model](record)
		if err != nil {
			return nil, err
		}
		byNative[model.NativeID] = record
	}
	plan := []modelChange{}
	added := 0
	for _, item := range result.Models {
		record, exists := byNative[item.ID]
		var model domain.Model
		if exists {
			model, err = store.Decode[domain.Model](record)
			if err != nil {
				return nil, err
			}
			// Manual registrations and user-declared advisory data retain their
			// original values even when this provider now reports the same ID.
			if model.Manual || model.Discovery == nil || model.MetadataSource == domain.UserDeclared {
				continue
			}
		} else {
			suppressed, err := tx.ModelSuppressed(provider, item.ID)
			if err != nil {
				return nil, err
			}
			if suppressed {
				continue
			}
			record.ID = domain.NewID()
			model = domain.Model{ProviderID: provider, NativeID: item.ID, Name: item.Name, New: true, Harnesses: []domain.Harness{}, Discovery: &domain.ModelDiscovery{FirstSeenAt: result.ObservedAt, UpdatedAt: result.ObservedAt}}
			added++
		}
		before, _ := json.Marshal(model)
		model.ContextLimit = item.ContextLimit
		model.InputModalities = item.InputModalities
		model.OutputModalities = item.OutputModalities
		model.Tools = item.Tools
		model.Reasoning = item.Reasoning
		model.MetadataSource = domain.Unknown
		if item.ContextLimit != nil || len(item.InputModalities) > 0 || len(item.OutputModalities) > 0 || item.Tools != nil || item.Reasoning != nil {
			model.MetadataSource = domain.Known
		}
		after, _ := json.Marshal(model)
		if exists && bytes.Equal(before, after) {
			continue
		}
		model.Discovery.UpdatedAt = result.ObservedAt
		if err := model.Validate(); err != nil {
			return nil, err
		}
		plan = append(plan, modelChange{record, model})
	}
	if err := store.CatalogBound(len(existing) + added); err != nil {
		return nil, err
	}
	return plan, nil
}
func (s *Service) SearchModels(ctx context.Context, req *connect.Request[pb.SearchModelsRequest]) (*connect.Response[pb.SearchModelsResponse], error) {
	return nil, rpc.Error(domain.Fail(domain.Unsupported, "Persistent Model search is retired.", "Use source-native configuration and optional endpoint suggestions."), req.Header().Get(rpc.CorrelationHeader))
}
func (s *Service) ResolveModel(ctx context.Context, req *connect.Request[pb.ResolveModelRequest]) (*connect.Response[pb.ResolveModelResponse], error) {
	model, err := s.Store.ResolveModel(ctx, req.Msg.Selector, domain.ID(req.Msg.ProviderId))
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.ResolveModelResponse{Model: rpc.Resource(model)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
