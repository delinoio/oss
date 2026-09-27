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
	closure    func(context.Context, ClosureClaim) error
	stop       func(context.Context, StopClaim) error
	fileReply  func(context.Context, FilePermissionClaim) error
}

func OpenOwnedAPIWithFileTools(ctx context.Context, config APIExecutionConfig, creation func(context.Context, CreationClaim) error, input func(context.Context, InputClaim) error, closure func(context.Context, ClosureClaim) error, reply func(context.Context, FilePermissionClaim) error) (*OwnedAPI, error) {
	if reply == nil {
		return nil, apiConfigurationError()
	}
	api, err := OpenOwnedAPI(ctx, config, creation, input, closure)
	if err != nil {
		return nil, err
	}
	api.fileReply = reply
	return api, nil
}

func (a *OwnedAPI) RunFileTools(ctx context.Context, request domain.ID, input string, emit func(context.Context, InputObservation) error) (PromptResult, error) {
	if a == nil || a.connection == nil || a.fileReply == nil {
		return PromptResult{}, apiConfigurationError()
	}
	return a.connection.RunFileTools(ctx, request, input, a.input, emit)
}

func (a *OwnedAPI) ReplyFilePermission(ctx context.Context, request, arrival domain.ID, decision FilePermissionDecision) (FilePermissionDelivery, error) {
	if a == nil || a.connection == nil || a.fileReply == nil {
		return FilePermissionDelivery{}, apiConfigurationError()
	}
	return a.connection.ReplyFilePermission(ctx, request, arrival, decision, a.fileReply)
}

func (a *OwnedAPI) InspectFilePermission(arrival domain.ID) (FilePermissionDelivery, error) {
	if a == nil || a.connection == nil || a.connection.textControl() == nil {
		return FilePermissionDelivery{}, sessionUncertain()
	}
	return a.connection.textControl().inspectFileReply(arrival)
}

func OpenOwnedAPIWithStop(ctx context.Context, config APIExecutionConfig, creation func(context.Context, CreationClaim) error, input func(context.Context, InputClaim) error, closure func(context.Context, ClosureClaim) error, stop func(context.Context, StopClaim) error) (*OwnedAPI, error) {
	if stop == nil {
		return nil, apiConfigurationError()
	}
	api, err := OpenOwnedAPI(ctx, config, creation, input, closure)
	if err != nil {
		return nil, err
	}
	api.stop = stop
	return api, nil
}

func (a *OwnedAPI) StopText(ctx context.Context, request domain.ID) (StopObservation, error) {
	if a == nil || a.connection == nil || a.stop == nil {
		return StopObservation{}, apiConfigurationError()
	}
	return a.connection.StopText(ctx, request, a.stop)
}

func (a *OwnedAPI) InspectStop() (StopObservation, error) {
	if a == nil || a.connection == nil {
		return StopObservation{}, apiConfigurationError()
	}
	return a.connection.InspectStop()
}

func OpenOwnedAPI(ctx context.Context, config APIExecutionConfig, creation func(context.Context, CreationClaim) error, input func(context.Context, InputClaim) error, closure func(context.Context, ClosureClaim) error) (*OwnedAPI, error) {
	if creation == nil || input == nil || closure == nil {
		return nil, apiConfigurationError()
	}
	connection, err := openAPI(ctx, config)
	if err != nil {
		return nil, err
	}
	return &OwnedAPI{connection: connection, creation: creation, input: input, closure: closure}, nil
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

func (a *OwnedAPI) RunReadFiles(ctx context.Context, request domain.ID, input string, emit func(context.Context, InputObservation) error) (PromptResult, error) {
	if a == nil || a.connection == nil {
		return PromptResult{}, apiConfigurationError()
	}
	return a.connection.RunReadFiles(ctx, request, input, a.input, emit)
}

func (a *OwnedAPI) Close() error {
	if a == nil || a.connection == nil {
		return apiConfigurationError()
	}
	return a.connection.Close()
}

func (a *OwnedAPI) CloseText(ctx context.Context, request domain.ID) (TextClosure, error) {
	if a == nil || a.connection == nil {
		return TextClosure{}, apiConfigurationError()
	}
	return a.connection.CloseText(ctx, request, a.closure)
}
