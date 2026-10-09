// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
)

func TestNativeOptionsKeepSavedSelectionsSeparateFromExecution(t *testing.T) {
	for _, harness := range []Harness{Codex, ClaudeCode, OpenCode, GrokBuild} {
		options := AgentOptions{Permission: PermissionDefault, ApprovalReviewModel: "retained-value"}
		agent := Agent{Name: "Retained", Harness: harness, Model: &InlineModel{ModelIdentity: ModelIdentity{ProviderID: NewID(), NativeID: "fixture"}, MetadataSource: Unknown}, Options: options}
		if err := agent.Validate(); err != nil {
			t.Fatal("unavailable option prevented saving", err)
		}
		config := ExecutionConfiguration{Harness: harness, Options: options}
		err := config.validateNativeOptions()
		if err == nil || SafeError(err).Code != Unsupported || !strings.Contains(SafeError(err).Message, "approval_review_model") || strings.Contains(SafeError(err).Message, options.ApprovalReviewModel) || config.Options != options {
			t.Fatal("missing adapter lost its exact field or saved value", err)
		}
	}
}

func TestNativeOptionSupportIsNotAnEffortOrConcurrencyAllowlist(t *testing.T) {
	for _, harness := range []Harness{Codex, ClaudeCode, OpenCode} {
		config := ExecutionConfiguration{Harness: harness, Effort: "Future-Effort", Options: AgentOptions{Permission: PermissionDefault}}
		if err := config.validateNativeOptions(); err != nil {
			t.Fatal("native effort was preemptively rejected", err)
		}
	}
	options := AgentOptions{Permission: PermissionFullAccess, SubagentEffort: "future-child-effort", MaxConcurrency: ^uint32(0), ServiceTier: "future-tier", ApprovalPolicy: "on-failure"}
	if err := ValidateCodexSubagentOptions(options); err != nil {
		t.Fatal(err)
	}
	if err := (ExecutionConfiguration{Harness: Codex, Options: options}).validateNativeOptions(); err != nil {
		t.Fatal(err)
	}
	if err := (ExecutionConfiguration{Harness: GrokBuild, Effort: "high", Options: AgentOptions{Permission: PermissionDefault}}).validateNativeOptions(); err == nil || !strings.Contains(SafeError(err).Message, "effort") {
		t.Fatal("missing applied-effort observation acquired support")
	}
}

func TestOpenCodeEffortObservationMustMatchImmutableSelection(t *testing.T) {
	effort := "Future-Effort"
	config := ExecutionConfiguration{Harness: OpenCode, NativeModel: "native-model", Effort: effort, Options: AgentOptions{Permission: PermissionDefault}}
	observed := ObservedExecutionSettings{Model: config.NativeModel, OpenCodeAgent: OpenCodeBuildAgent, Permission: PermissionDefault, Effort: &effort}
	if err := observed.Validate(config); err != nil {
		t.Fatal(err)
	}
	observed.Effort = nil
	if observed.Validate(config) == nil {
		t.Fatal("missing applied effort accepted")
	}
	other := "different"
	observed.Effort = &other
	if observed.Validate(config) == nil {
		t.Fatal("changed effort accepted")
	}
}

func TestUnavailableNativeOptionsIdentifyEveryRetainedSelection(t *testing.T) {
	for _, harness := range []Harness{Codex, ClaudeCode, OpenCode, GrokBuild} {
		for _, field := range []struct {
			name      string
			options   AgentOptions
			available Harness
		}{
			{"claude_permission", AgentOptions{Permission: PermissionDefault, ClaudePermission: ClaudePermissionAuto}, ClaudeCode},
			{"permission", AgentOptions{Permission: PermissionFullAccess}, Codex},
			{"approval_policy", AgentOptions{Permission: PermissionDefault, ApprovalPolicy: "on-failure"}, Codex},
			{"subagent_model", AgentOptions{Permission: PermissionDefault, SubagentModel: "retained-model"}, Codex},
			{"subagent_effort", AgentOptions{Permission: PermissionDefault, SubagentEffort: "future-effort"}, Codex},
			{"max_concurrency", AgentOptions{Permission: PermissionDefault, MaxConcurrency: 1024}, Codex},
			{"service_tier", AgentOptions{Permission: PermissionDefault, ServiceTier: "future-tier"}, Codex},
		} {
			t.Run(string(harness)+"/"+field.name, func(t *testing.T) {
				agent := Agent{Name: "Retained selections", Harness: harness, Model: &InlineModel{ModelIdentity: ModelIdentity{ProviderID: NewID(), NativeID: "fixture"}, MetadataSource: Unknown}, Options: field.options}
				if err := agent.Validate(); err != nil {
					t.Fatal("saved selection lost", err)
				}
				config := ExecutionConfiguration{Harness: harness, Options: field.options}
				err := config.validateNativeOptions()
				if harness == field.available {
					if err != nil {
						t.Fatal("available adapter rejected", err)
					}
				} else if err == nil || !strings.Contains(SafeError(err).Message, `"`+field.name+`"`) {
					t.Fatal("unavailable adapter lost its exact field", err)
				}
				if config.Options != field.options {
					t.Fatal("retained selection changed")
				}
			})
		}
	}
}

func TestUnavailableOptionDoesNotReplaceOriginalFormatValidation(t *testing.T) {
	configuration := managedSubscriptionExecutionConfiguration(t, PermissionDefault)
	configuration.Options.ApprovalReviewModel = "retained-reviewer"
	configuration.ModelID = "invalid-model-reference"
	if err := configuration.Validate(); err == nil || SafeError(err).Code != RecoveryRequired {
		t.Fatal("missing adapter displaced original reference validation", err)
	}
	configuration.ModelID = (ModelIdentity{ProviderID: configuration.ProviderID, SubscriptionService: configuration.SubscriptionService, NativeID: configuration.NativeModel}).Key()
	if err := configuration.Validate(); err == nil || SafeError(err).Code != Unsupported || !strings.Contains(SafeError(err).Message, "approval_review_model") {
		t.Fatal("well-formed retained selection lost its explicit adapter error", err)
	}
}
