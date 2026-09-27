package grok

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// APIExecutionConfig is an internal Worker integration boundary. Its scoped
// token requires independent server registration before production inference.
type APIExecutionConfig = apiConfig

// OwnedAPI binds the original durable claim callbacks for the entire runtime.
// There is no constructor for adopting a process or restoring send authority.
type OwnedAPI struct {
	connection *apiConnection
	creation   func(context.Context, CreationClaim) error
	input      func(context.Context, InputClaim) error
}

func OpenOwnedAPI(ctx context.Context, config APIExecutionConfig, creation func(context.Context, CreationClaim) error, input func(context.Context, InputClaim) error) (*OwnedAPI, error) {
	if creation == nil || input == nil {
		return nil, apiConfigurationError()
	}
	connection, err := openAPI(ctx, config)
	if err != nil {
		return nil, err
	}
	return &OwnedAPI{connection: connection, creation: creation, input: input}, nil
}

func (a *OwnedAPI) Create(ctx context.Context, request, product domain.ID) (domain.ID, error) {
	if a == nil || a.connection == nil {
		return "", apiConfigurationError()
	}
	return a.connection.Create(ctx, request, product, a.creation)
}

func (a *OwnedAPI) RunText(ctx context.Context, request domain.ID, input string, emit func(context.Context, InputObservation) error) (PromptResult, error) {
	if a == nil || a.connection == nil {
		return PromptResult{}, apiConfigurationError()
	}
	return a.connection.RunText(ctx, request, input, a.input, emit)
}

func (a *OwnedAPI) Close() error {
	if a == nil || a.connection == nil {
		return apiConfigurationError()
	}
	return a.connection.Close()
}
