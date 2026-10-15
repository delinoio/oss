package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

type openCodeResumeCall struct {
	calls    int
	agent    opencode.PrimaryAgent
	explicit bool
	request  domain.ID
}

func (c *openCodeResumeCall) opener(result *opencode.OwnedAPI, err error) openCodeResumedAPIOpener {
	return func(_ context.Context, _ opencode.APIExecutionConfig, _ string, _ []byte, _ opencode.CheckpointReference, request domain.ID, agent opencode.PrimaryAgent, explicit bool) (*opencode.OwnedAPI, error) {
		c.calls++
		c.agent, c.explicit, c.request = agent, explicit, request
		return result, err
	}
}

func openCodeResumeInput(t *testing.T, mode domain.SessionMode, intent domain.ExecutionIntent) domain.ExecutionJobInput {
	t.Helper()
	providerID, accountID := domain.NewID(), domain.NewID()
	identity := domain.ModelIdentity{ProviderID: providerID, NativeID: "private-model"}
	model := domain.Model{Name: "Fixture", NativeID: identity.NativeID, ProviderID: providerID, Harnesses: []domain.Harness{domain.OpenCode}, MetadataSource: domain.UserDeclared}
	agent := domain.Agent{Name: "Fixture", Harness: domain.OpenCode, ModelID: identity.Key(), Model: &domain.InlineModel{ModelIdentity: identity, Name: model.Name, MetadataSource: model.MetadataSource}, Accounts: []domain.WeightedAccount{{ID: accountID, Weight: 1}}, Options: domain.AgentOptions{Permission: domain.PermissionDefault}}
	configuration, err := domain.ResolveExecutionConfiguration(domain.NewID(), 1, agent, 1, model, domain.Priority, nil)
	if err != nil {
		t.Fatal(err)
	}
	return domain.ExecutionJobInput{
		ThreadRequestID: domain.NewID(),
		Configuration:   configuration,
		Continuation:    &domain.ExecutionContinuation{InputMode: mode, Intent: intent},
	}
}

func TestOpenCodeNativeAPIReturnsOriginalCheckpointResumeError(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail error
		code domain.Code
	}{
		{"uncertain", domain.Fail(domain.RecoveryRequired, "Uncertain.", "Retain history."), domain.RecoveryRequired},
		{"incompatible", domain.Fail(domain.Unsupported, "Incompatible.", "Retain history."), domain.Unsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var call openCodeResumeCall
			input := openCodeResumeInput(t, domain.ExecuteMode, domain.ContinueExplicitly)
			api, err := openCodeNativeAPI(context.Background(), opencode.APIExecutionConfig{}, "home", nil, &openCodeExecutionCheckpoint{}, input, call.opener(nil, tc.fail))
			if api != nil || !errors.Is(err, tc.fail) || domain.SafeError(err).Code != tc.code {
				t.Fatalf("resume error was not returned unchanged: api=%v err=%v", api, err)
			}
			if call.calls != 1 || call.agent != opencode.BuildAgent || !call.explicit || call.request != input.ThreadRequestID {
				t.Fatalf("unexpected resume call: %+v", call)
			}
		})
	}
}

func TestOpenCodeNativeAPIKeepsPrimaryAgentFailure(t *testing.T) {
	var call openCodeResumeCall
	input := openCodeResumeInput(t, "unknown", domain.ContinueExplicitly)
	api, err := openCodeNativeAPI(context.Background(), opencode.APIExecutionConfig{}, "home", nil, &openCodeExecutionCheckpoint{}, input, call.opener(nil, nil))
	if api != nil || err == nil || domain.SafeError(err).Code != domain.InvalidArgument || call.calls != 0 {
		t.Fatalf("primary agent failure changed: api=%v err=%v calls=%d", api, err, call.calls)
	}
}

func TestOpenCodeNativeAPIResumesCheckpointAndFork(t *testing.T) {
	var call openCodeResumeCall
	input := openCodeResumeInput(t, domain.PlanMode, "")
	api, err := openCodeNativeAPI(context.Background(), opencode.APIExecutionConfig{}, "home", nil, &openCodeExecutionCheckpoint{}, input, call.opener(nil, nil))
	if err != nil || api != nil || call.calls != 1 || call.agent != opencode.PlanAgent || call.explicit {
		t.Fatalf("checkpoint resume changed: err=%v calls=%d agent=%q explicit=%v", err, call.calls, call.agent, call.explicit)
	}
	call = openCodeResumeCall{}
	fork := domain.ExecutionJobInput{ThreadRequestID: domain.NewID()}
	api, err = openCodeNativeAPI(context.Background(), opencode.APIExecutionConfig{}, "home", &openCodeForkCheckpoint{}, nil, fork, call.opener(nil, nil))
	if err != nil || api != nil || call.calls != 1 || call.agent != opencode.BuildAgent || call.explicit || call.request != fork.ThreadRequestID {
		t.Fatalf("fork resume changed: err=%v call=%+v", err, call)
	}
}
