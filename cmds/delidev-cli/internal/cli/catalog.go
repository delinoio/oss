package cli

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func providerCatalog(ctx context.Context, c client, o options, args []string) (any, error) {
	operation := args[0]
	f := flags("provider " + operation)
	if operation == "inventory" {
		query := f.String("query", "", "search provider names")
		enabled := f.Bool("enabled-only", false, "include only enabled API providers")
		limit := f.Uint64("limit", 50, "page size")
		page := f.String("page-token", "", "page token")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *limit < 1 || *limit > 200 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid provider page size.", "Use --limit between 1 and 200.")
		}
		response, err := c.providers.ListProviderInventory(ctx, request(c, &pb.ListProviderInventoryRequest{Query: *query, EnabledOnly: *enabled, PageSize: uint32(*limit), PageToken: *page}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"entries": response.Msg.Entries, "capabilities": response.Msg.Capabilities, "next_page_token": response.Msg.NextPageToken}, nil
	}
	if operation == "discover" {
		account := f.String("account-id", "", "")
		revision := f.Uint64("revision", 0, "")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *account == "" || *revision == 0 {
			return nil, domain.Fail(domain.MissingInput, "Discovery requires an account and its current revision.", "Provide --account-id and --revision from account status.")
		}
		response, err := c.providers.DiscoverModels(ctx, request(c, &pb.DiscoverModelsRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *account, ExpectedRevision: *revision}}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		var observation domain.CatalogObservation
		if err := domain.Decode(response.Msg.ObservationJson, &observation); err != nil {
			return nil, err
		}
		value := map[string]any{"account": resourceJSON(response.Msg.Account), "observation": observation, "replayed": response.Msg.Replayed}
		if observation.Problem != nil {
			return value, observation.Problem
		}
		return value, nil
	}
	var preset, name *string
	if operation == "create" {
		preset = f.String("preset", "", "")
		name = f.String("name", "", "")
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	response, err := c.providers.ListProviderPresets(ctx, request(c, &pb.ListProviderPresetsRequest{}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	var presets []domain.ProviderPreset
	if err := domain.Decode(response.Msg.PresetsJson, &presets); err != nil {
		return nil, err
	}
	if operation == "presets" {
		return map[string]any{"presets": presets}, nil
	}
	for _, item := range presets {
		if item.ID == domain.ProviderPresetID(*preset) {
			provider := item.Provider
			if *name != "" {
				provider.Name = *name
				provider.PresetID = nil
			}
			provider.SetEnabled(true)
			raw, _ := json.Marshal(provider)
			created, err := c.configuration.SaveConfiguration(ctx, request(c, &pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID)}, Kind: pb.EntityKind_ENTITY_KIND_PROVIDER, SchemaVersion: 1, DocumentJson: raw}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			return map[string]any{"resource": resourceJSON(created.Msg.Resource), "replayed": created.Msg.Replayed}, nil
		}
	}
	return nil, domain.Fail(domain.InvalidArgument, "Unknown provider preset.", "List available identifiers with provider presets, or create a custom provider with --input.")
}
func modelCatalog(ctx context.Context, c client, args []string) (any, error) {
	operation := args[0]
	f := flags("model " + operation)
	provider := f.String("provider-id", "", "")
	if operation == "resolve" {
		selector := f.String("selector", "", "")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *selector == "" {
			return nil, domain.Fail(domain.MissingInput, "A model selector is required.", "Provide --selector with a canonical UUID, CLI alias or unambiguous native ID.")
		}
		response, err := c.providers.ResolveModel(ctx, request(c, &pb.ResolveModelRequest{Selector: *selector, ProviderId: *provider}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return resourceJSON(response.Msg.Model), nil
	}
	query := f.String("query", "", "")
	hidden := f.Bool("include-hidden", false, "")
	enabledProviders := f.Bool("enabled-providers-only", false, "filter to active API providers")
	limit := f.Uint64("limit", 50, "")
	page := f.String("page-token", "", "")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if *limit < 1 || *limit > 200 {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid model page size.", "Use --limit between 1 and 200.")
	}
	if *enabledProviders {
		if err := requireProviderInventoryCapability(ctx, c, pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACTIVE_API_MODEL_FILTER); err != nil {
			return nil, err
		}
	}
	response, err := c.providers.SearchModels(ctx, request(c, &pb.SearchModelsRequest{Query: *query, ProviderId: *provider, IncludeHidden: *hidden, PageSize: uint32(*limit), PageToken: *page, EnabledProvidersOnly: *enabledProviders}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	return map[string]any{"models": resourcesJSON(response.Msg.Models), "providers": resourcesJSON(response.Msg.Providers), "next_page_token": response.Msg.NextPageToken}, nil
}

func requireProviderInventoryCapability(ctx context.Context, c client, capability pb.ProviderInventoryCapability) error {
	response, err := c.providers.ListProviderInventory(ctx, request(c, &pb.ListProviderInventoryRequest{PageSize: 1}))
	if err != nil {
		return rpc.ClientError(err)
	}
	for _, supported := range response.Msg.Capabilities {
		if supported == capability {
			return nil
		}
	}
	return domain.Fail(domain.Unsupported, "The server does not support this provider inventory capability.", "Update the DeliDev server before using this provider-filtered command.")
}

func listWithProviderFilter(ctx context.Context, c client, filter *pb.Filter, providerID string) (*pb.ListResourcesResponse, error) {
	if providerID != "" {
		if err := requireProviderInventoryCapability(ctx, c, pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_PROVIDER_FILTER); err != nil {
			return nil, err
		}
	}
	response, err := c.resources.ListResources(ctx, request(c, &pb.ListResourcesRequest{Filter: filter, ProviderId: providerID}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	return response.Msg, nil
}
