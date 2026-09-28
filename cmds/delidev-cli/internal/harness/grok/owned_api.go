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
	connection    *apiConnection
	creation      func(context.Context, CreationClaim) error
	input         func(context.Context, InputClaim) error
	closure       func(context.Context, ClosureClaim) error
	stop          func(context.Context, StopClaim) error
	fileReply     func(context.Context, FilePermissionClaim) error
	questionReply func(context.Context, QuestionClaim) error
	mode          func(context.Context, ModeClaim) error
	planReply     func(context.Context, PlanClaim) error
}

type PlanningRecorders struct {
	Creation func(context.Context, CreationClaim) error
	Mode     func(context.Context, ModeClaim) error
	Input    func(context.Context, InputClaim) error
	File     func(context.Context, FilePermissionClaim) error
	Question func(context.Context, QuestionClaim) error
	Plan     func(context.Context, PlanClaim) error
}

func OpenOwnedAPIWithPlanning(ctx context.Context, config APIExecutionConfig, record PlanningRecorders) (*OwnedAPI, error) {
	if record.Creation == nil || record.Mode == nil || record.Input == nil || record.File == nil || record.Question == nil || record.Plan == nil {
		return nil, apiConfigurationError()
	}
	connection, err := openAPI(ctx, config)
	if err != nil {
		return nil, err
	}
	return &OwnedAPI{connection: connection, creation: record.Creation, mode: record.Mode, input: record.Input, fileReply: record.File, questionReply: record.Question, planReply: record.Plan}, nil
}

func (a *OwnedAPI) RunPlanning(ctx context.Context, request domain.ID, input string, emit func(context.Context, InputObservation) error) (PromptResult, error) {
	if a == nil || a.connection == nil || a.fileReply == nil || a.questionReply == nil || a.planReply == nil || a.mode == nil {
		return PromptResult{}, apiConfigurationError()
	}
	return a.connection.runInput(ctx, request, input, a.input, emit, planningInput)
}

func (a *OwnedAPI) ReplyPlan(ctx context.Context, request, arrival domain.ID, outcome PlanOutcome) (PlanDelivery, error) {
	if a == nil || a.connection == nil || a.planReply == nil {
		return PlanDelivery{}, apiConfigurationError()
	}
	return a.connection.ReplyPlan(ctx, request, arrival, outcome, a.planReply)
}

func (a *OwnedAPI) InspectPlan(arrival domain.ID) (PlanDelivery, error) {
	if a == nil || a.connection == nil || a.connection.textControl() == nil {
		return PlanDelivery{}, sessionUncertain()
	}
	return a.connection.textControl().inspectPlanReply(arrival)
}

func OpenOwnedAPIWithPlanQuestions(ctx context.Context, config APIExecutionConfig, creation func(context.Context, CreationClaim) error, mode func(context.Context, ModeClaim) error, input func(context.Context, InputClaim) error, reply func(context.Context, QuestionClaim) error) (*OwnedAPI, error) {
	if config.Mode != domain.PlanMode || creation == nil || mode == nil || input == nil || reply == nil {
		return nil, apiConfigurationError()
	}
	connection, err := openAPI(ctx, config)
	if err != nil {
		return nil, err
	}
	return &OwnedAPI{connection: connection, creation: creation, mode: mode, input: input, questionReply: reply}, nil
}

func (a *OwnedAPI) SelectPlan(ctx context.Context, request domain.ID) (ModeClaim, error) {
	if a == nil || a.connection == nil || a.mode == nil {
		return ModeClaim{}, apiConfigurationError()
	}
	return a.connection.SelectPlan(ctx, request, a.mode)
}

func (a *OwnedAPI) RunPlanQuestions(ctx context.Context, request domain.ID, input string, emit func(context.Context, InputObservation) error) (PromptResult, error) {
	if a == nil || a.connection == nil || a.mode == nil || a.questionReply == nil {
		return PromptResult{}, apiConfigurationError()
	}
	return a.connection.runInput(ctx, request, input, a.input, emit, planQuestionInput)
}

func OpenOwnedAPIWithTools(ctx context.Context, config APIExecutionConfig, creation func(context.Context, CreationClaim) error, input func(context.Context, InputClaim) error, closure func(context.Context, ClosureClaim) error, fileReply func(context.Context, FilePermissionClaim) error, questionReply func(context.Context, QuestionClaim) error) (*OwnedAPI, error) {
	if fileReply == nil || questionReply == nil {
		return nil, apiConfigurationError()
	}
	api, err := OpenOwnedAPIWithFileTools(ctx, config, creation, input, closure, fileReply)
	if err != nil {
		return nil, err
	}
	api.questionReply = questionReply
	return api, nil
}

func (a *OwnedAPI) RunTools(ctx context.Context, request domain.ID, input string, emit func(context.Context, InputObservation) error) (PromptResult, error) {
	if a == nil || a.connection == nil || a.fileReply == nil || a.questionReply == nil {
		return PromptResult{}, apiConfigurationError()
	}
	return a.connection.RunTools(ctx, request, input, a.input, emit)
}

func OpenOwnedAPIWithQuestions(ctx context.Context, config APIExecutionConfig, creation func(context.Context, CreationClaim) error, input func(context.Context, InputClaim) error, closure func(context.Context, ClosureClaim) error, reply func(context.Context, QuestionClaim) error) (*OwnedAPI, error) {
	if reply == nil {
		return nil, apiConfigurationError()
	}
	api, err := OpenOwnedAPI(ctx, config, creation, input, closure)
	if err != nil {
		return nil, err
	}
	api.questionReply = reply
	return api, nil
}

func (a *OwnedAPI) RunQuestions(ctx context.Context, request domain.ID, input string, emit func(context.Context, InputObservation) error) (PromptResult, error) {
	if a == nil || a.connection == nil || a.questionReply == nil {
		return PromptResult{}, apiConfigurationError()
	}
	return a.connection.RunQuestions(ctx, request, input, a.input, emit)
}

func (a *OwnedAPI) ReplyQuestion(ctx context.Context, request, arrival domain.ID, answer QuestionAnswer) (QuestionDelivery, error) {
	if a == nil || a.connection == nil || a.questionReply == nil {
		return QuestionDelivery{}, apiConfigurationError()
	}
	return a.connection.ReplyQuestion(ctx, request, arrival, answer, a.questionReply)
}

func (a *OwnedAPI) InspectQuestion(arrival domain.ID) (QuestionDelivery, error) {
	if a == nil || a.connection == nil || a.connection.textControl() == nil {
		return QuestionDelivery{}, sessionUncertain()
	}
	return a.connection.textControl().inspectQuestionReply(arrival)
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
	if creation == nil || input == nil || closure == nil || config.Mode != "" && config.Mode != domain.ExecuteMode {
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
