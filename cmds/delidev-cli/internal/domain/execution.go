package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

const MaxAppliedInstructions = 256 << 10

type AppliedTemplate struct {
	ID       ID     `json:"id"`
	Revision uint64 `json:"revision"`
	Contents string `json:"contents"`
}

// ExecutionConfiguration contains only the user's non-secret configuration.
// Native defaults remain unspecified here; observed effective settings belong
// to the native execution record and cannot rewrite this accepted selection.
type ExecutionConfiguration struct {
	NativeDefaults *NativeHarnessDefaultProof `json:"native_defaults,omitempty"`
	BranchPrefix   *BranchPrefixSelection     `json:"branch_prefix,omitempty"`
	MCPSelections  *MCPSelectionList          `json:"mcp_selections,omitempty"`

	ReviewerNativeModel string                  `json:"reviewer_native_model,omitempty"`
	ImageInputDeclared  bool                    `json:"image_input_declared,omitempty"`
	SidechatPolicy      SidechatPolicy          `json:"sidechat_policy,omitempty"`
	AgentID             ID                      `json:"agent_id"`
	AgentRevision       uint64                  `json:"agent_revision"`
	Harness             Harness                 `json:"harness"`
	ModelID             ID                      `json:"model_id"`
	ModelRevision       uint64                  `json:"model_revision"`
	ProviderID          ID                      `json:"provider_id,omitempty"`
	SubscriptionService SubscriptionService     `json:"subscription_service,omitempty"`
	NativeModel         string                  `json:"native_model"`
	SubagentModel       *ExecutionSubagentModel `json:"subagent_model,omitempty"`
	Effort              string                  `json:"effort,omitempty"`
	Options             AgentOptions            `json:"options"`
	Accounts            []WeightedAccount       `json:"accounts"`
	Routing             RoutingPolicy           `json:"routing"`
	Templates           []AppliedTemplate       `json:"templates"`
	Instructions        string                  `json:"instructions"`
	OpenCodeContext     *OpenCodeModelContext   `json:"opencode_context,omitempty"`
	GrokContext         *GrokModelContext       `json:"grok_context,omitempty"`
	Subscription        bool                    `json:"subscription,omitempty"`
}

func ResolveExecutionConfiguration(agentID ID, agentRevision uint64, agent Agent, modelRevision uint64, model Model, defaultPolicy RoutingPolicy, templates []AppliedTemplate) (ExecutionConfiguration, error) {
	var result ExecutionConfiguration
	if agent.ReconfigurationRequired {
		return result, SubscriptionReconfigurationRequired()
	}
	if err := agentID.Validate(); err != nil {
		return result, err
	}
	if agentRevision == 0 || modelRevision == 0 {
		return result, Fail(InvalidArgument, "Execution configuration needs persisted revisions.", "Resolve the current Agent Worker and canonical model immediately before dispatch.")
	}
	if err := agent.Validate(); err != nil {
		return result, err
	}
	if err := model.Validate(); err != nil {
		return result, err
	}
	if !slices.Contains(model.Harnesses, agent.Harness) {
		return result, Fail(Unsupported, "The selected model no longer supports this harness.", "Select a compatible model before first execution.")
	}
	policy := defaultPolicy
	if agent.Routing != nil {
		policy = *agent.Routing
	}
	if !policy.Valid() {
		return result, Fail(InvalidArgument, "Invalid effective routing policy.", "Configure one of the supported policies.")
	}
	if len(templates) != len(agent.Templates) {
		return result, Fail(InvalidArgument, "The applied template set is incomplete.", "Resolve every ordered template before dispatch.")
	}
	parts := make([]string, len(templates))
	size := 0
	for i, template := range templates {
		if template.ID != agent.Templates[i] || template.Revision == 0 {
			return result, Fail(InvalidArgument, "The applied template order or revision changed.", "Resolve the exact ordered template references.")
		}
		if err := Text(template.Contents, "template contents", 128<<10, true); err != nil {
			return result, err
		}
		size += len(template.Contents)
		if i > 0 {
			size += 2
		}
		if size > MaxAppliedInstructions {
			return result, Fail(ResourceExhausted, "Combined instructions exceed the native adapter bound.", "Reduce the ordered templates before first execution; no contents were truncated.")
		}
		parts[i] = template.Contents
	}
	result = ExecutionConfiguration{MCPSelections: agent.MCPSelections, NativeDefaults: agent.NativeDefaults, ImageInputDeclared: agent.Harness == Codex && slices.Contains(model.InputModalities, "image"), AgentID: agentID, AgentRevision: agentRevision, Harness: agent.Harness, ModelID: agent.ModelID, ModelRevision: modelRevision, ProviderID: model.ProviderID, SubscriptionService: model.SubscriptionService, Subscription: model.SourceKind == SubscriptionModel, NativeModel: model.NativeID, Effort: agent.Effort, Options: agent.Options, Accounts: slices.Clone(agent.Accounts), Routing: policy, Templates: slices.Clone(templates), Instructions: strings.Join(parts, "\n\n")}
	if agent.Options.ApprovalsReviewer == CodexReviewerAuto && !result.Subscription {
		result.ReviewerNativeModel = CodexReviewerNativeModel
	}
	if agent.Harness == GrokBuild && model.ContextLimit != nil {
		result.GrokContext = &GrokModelContext{Tokens: *model.ContextLimit, Source: model.MetadataSource}
		if err := result.GrokContext.Validate(); err != nil {
			return ExecutionConfiguration{}, err
		}
	}
	if agent.Harness == OpenCode && model.ContextLimit != nil && (model.MetadataSource == Known || model.MetadataSource == UserDeclared) {
		result.OpenCodeContext = &OpenCodeModelContext{Tokens: *model.ContextLimit, Source: model.MetadataSource, Policy: OpenCodeNativeContextV1}
		if err := result.OpenCodeContext.Validate(); err != nil {
			return ExecutionConfiguration{}, err
		}
	}
	return result, nil
}

func (c ExecutionConfiguration) Digest() (string, error) {
	raw, err := json.Marshal(c)
	if err != nil || len(raw) > 1<<20 {
		return "", Fail(ResourceExhausted, "The execution snapshot exceeds its storage bound.", "Reduce the configuration before first execution; the snapshot was not accepted.")
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func (c ExecutionConfiguration) Validate() error {
	if c.NativeDefaults != nil && (c.Harness != Codex || c.NativeDefaults.Validate() != nil) {
		return NativeDefaultsUnavailable()
	}
	if c.MCPSelections != nil {
		if e := c.MCPSelections.Validate(); e != nil {
			return e
		}
	}
	if c.BranchPrefix != nil {
		if err := c.BranchPrefix.Validate(); err != nil {
			return err
		}
		if _, err := c.NativeInstructions(ExecuteMode); err != nil {
			return err
		}
	}
	if err := c.ValidateSidechat(); err != nil {
		return err
	}
	if c.Harness == Codex && ValidateCodexSubagentOptions(c.Options) != nil {
		return ValidateCodexSubagentOptions(c.Options)
	}
	if c.SubagentModel != nil && (c.Harness != Codex || c.SubagentModel.ModelID.Validate() != nil || c.SubagentModel.ModelRevision == 0 || c.SubagentModel.NativeModel != c.Options.SubagentModel || Text(c.SubagentModel.NativeModel, "saved child model", 256, true) != nil) {
		return Fail(RecoveryRequired, "Invalid retained child model identity.", "Preserve the immutable original child model snapshot.")
	}
	if c.SubscriptionService != "" && (!c.Subscription || !c.SubscriptionService.Valid() || c.SubscriptionService.Harness() != c.Harness || c.ProviderID != "") {
		return Fail(RecoveryRequired, "Invalid retained subscription identity.", "Preserve the original snapshot; create a new explicitly configured session.")
	}
	if c.Subscription && !c.IsOpenCodeGo() && c.Harness != Codex && (c.Harness != ClaudeCode || c.SubscriptionService != SubscriptionClaude) {
		return Fail(Unsupported, "This harness has no subscription execution profile.", "Select a supported native subscription profile.")
	}
	if c.OpenCodeContext != nil && (c.Harness != OpenCode || c.OpenCodeContext.Validate() != nil) {
		return Fail(RecoveryRequired, "The retained OpenCode context metadata is invalid.", "Preserve the original model selection and metadata provenance.")
	}
	if c.GrokContext != nil && (c.Harness != GrokBuild || c.GrokContext.Validate() != nil) {
		return Fail(RecoveryRequired, "The retained Grok model context is invalid.", "Preserve the original model selection and its metadata provenance.")
	}
	ids := make([]ID, len(c.Templates))
	for i, template := range c.Templates {
		ids[i] = template.ID
	}
	agent := Agent{Name: "Retained configuration", Harness: c.Harness, ModelID: c.ModelID, Effort: c.Effort, Options: c.Options, Accounts: c.Accounts, Routing: &c.Routing, Templates: ids}
	model := Model{Name: "Retained model", NativeID: c.NativeModel, ProviderID: c.ProviderID, Harnesses: []Harness{c.Harness}, MetadataSource: Unknown}
	if c.ImageInputDeclared {
		if c.Harness != Codex {
			return UnsupportedImageInput()
		}
		model.InputModalities = []string{"image"}
	}
	if c.SubscriptionService != "" {
		model.SourceKind, model.SubscriptionService = SubscriptionModel, c.SubscriptionService
	}
	resolved, err := ResolveExecutionConfiguration(c.AgentID, c.AgentRevision, agent, c.ModelRevision, model, c.Routing, c.Templates)
	if err != nil {
		return err
	}
	if resolved.Instructions != c.Instructions {
		return Fail(RecoveryRequired, "Retained instructions do not match their ordered templates.", "Reconcile the immutable first-execution configuration.")
	}
	return c.ValidateNativeOptions()
}

type NativeReferenceKind string

const (
	NativeResponseReference     NativeReferenceKind = "response"
	NativeConversationReference NativeReferenceKind = "conversation"
)

// InitialExecution binds configuration, the first claimed input and the actual
// routing observation. Current account/connection changes are separate from the
// immutable original selection and require their own future lifecycle control.
type InitialExecution struct {
	ID                  ID                     `json:"id"`
	InputID             ID                     `json:"input_id"`
	Configuration       ExecutionConfiguration `json:"configuration"`
	ConfigurationDigest string                 `json:"configuration_digest"`
	InitialAccountID    ID                     `json:"initial_account_id"`
	ConnectionID        ID                     `json:"connection_id"`
	Route               Route                  `json:"route"`
	AcceptedAt          time.Time              `json:"accepted_at"`
}

type AgentRouting struct {
	AgentID ID           `json:"agent_id"`
	State   RoutingState `json:"state"`
}
