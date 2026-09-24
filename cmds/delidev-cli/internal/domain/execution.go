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
	AgentID       ID                `json:"agent_id"`
	AgentRevision uint64            `json:"agent_revision"`
	Harness       Harness           `json:"harness"`
	ModelID       ID                `json:"model_id"`
	ModelRevision uint64            `json:"model_revision"`
	ProviderID    ID                `json:"provider_id"`
	NativeModel   string            `json:"native_model"`
	Effort        string            `json:"effort,omitempty"`
	Options       AgentOptions      `json:"options"`
	Accounts      []WeightedAccount `json:"accounts"`
	Routing       RoutingPolicy     `json:"routing"`
	Templates     []AppliedTemplate `json:"templates"`
	Instructions  string            `json:"instructions"`
}

func ResolveExecutionConfiguration(agentID ID, agentRevision uint64, agent Agent, modelRevision uint64, model Model, defaultPolicy RoutingPolicy, templates []AppliedTemplate) (ExecutionConfiguration, error) {
	var result ExecutionConfiguration
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
	result = ExecutionConfiguration{AgentID: agentID, AgentRevision: agentRevision, Harness: agent.Harness, ModelID: agent.ModelID, ModelRevision: modelRevision, ProviderID: model.ProviderID, NativeModel: model.NativeID, Effort: agent.Effort, Options: agent.Options, Accounts: slices.Clone(agent.Accounts), Routing: policy, Templates: slices.Clone(templates), Instructions: strings.Join(parts, "\n\n")}
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
	ids := make([]ID, len(c.Templates))
	for i, template := range c.Templates {
		ids[i] = template.ID
	}
	agent := Agent{Name: "Retained configuration", Harness: c.Harness, ModelID: c.ModelID, Effort: c.Effort, Options: c.Options, Accounts: c.Accounts, Routing: &c.Routing, Templates: ids}
	model := Model{Name: "Retained model", NativeID: c.NativeModel, ProviderID: c.ProviderID, Harnesses: []Harness{c.Harness}, MetadataSource: Unknown}
	resolved, err := ResolveExecutionConfiguration(c.AgentID, c.AgentRevision, agent, c.ModelRevision, model, c.Routing, c.Templates)
	if err != nil {
		return err
	}
	if resolved.Instructions != c.Instructions {
		return Fail(RecoveryRequired, "Retained instructions do not match their ordered templates.", "Reconcile the immutable first-execution configuration.")
	}
	return nil
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
