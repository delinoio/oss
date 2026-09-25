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
			}
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
	limit := f.Uint64("limit", 50, "")
	page := f.String("page-token", "", "")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if *limit < 1 || *limit > 200 {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid model page size.", "Use --limit between 1 and 200.")
	}
	response, err := c.providers.SearchModels(ctx, request(c, &pb.SearchModelsRequest{Query: *query, ProviderId: *provider, IncludeHidden: *hidden, PageSize: uint32(*limit), PageToken: *page}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	return map[string]any{"models": resourcesJSON(response.Msg.Models), "providers": resourcesJSON(response.Msg.Providers), "next_page_token": response.Msg.NextPageToken}, nil
}
