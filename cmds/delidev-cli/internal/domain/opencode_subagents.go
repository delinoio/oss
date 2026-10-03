// SPDX-License-Identifier: Apache-2.0
package domain

import "encoding/json"

const (
	OpenCodeTaskSource            SubagentSource   = "opencode-task"
	OpenCodeChildContentSource    SubagentSource   = "opencode-child-content"
	OpenCodeChildHistorySource    SubagentSource   = "opencode-child-history"
	OpenCodeChildCleanupSource    SubagentSource   = "opencode-child-scope-cleanup"
	OpenCodeForegroundSubagentsV1 WorkerCapability = "opencode-foreground-subagents-v1"
)

// Original root tool ownership supplements the independently read native child
// parent. Neither task metadata nor this product reference alone creates a child.
type OpenCodeParentTool struct {
	ID        ID     `json:"id"`
	MessageID string `json:"message_id"`
	PartID    string `json:"part_id"`
	CallID    string `json:"call_id"`
}

func (r OpenCodeParentTool) Validate() error {
	if r.ID.Validate() != nil || NativeIdentity(r.MessageID).Validate(OpenCode, NativeMessageIdentity) != nil || NativeIdentity(r.PartID).Validate(OpenCode, NativePartIdentity) != nil || Text(r.CallID, "original task call", 1024, true) != nil {
		return invalidSubagent()
	}
	return nil
}

type OpenCodeTaskInput struct {
	Description string  `json:"description"`
	Prompt      string  `json:"prompt"`
	AgentType   string  `json:"subagent_type"`
	Command     *string `json:"command,omitempty"`
	Background  *bool   `json:"background,omitempty"`
}

func DecodeOpenCodeForegroundTask(raw []byte) (OpenCodeTaskInput, error) {
	var input OpenCodeTaskInput
	if Decode(raw, &input) != nil || Text(input.Description, "task description", MaxMessageText, true) != nil || Text(input.Prompt, "task prompt", MaxPromptBytes, true) != nil || Text(input.AgentType, "task agent", 256, true) != nil || input.Command != nil && Text(*input.Command, "task command", 1024, false) != nil || input.Background != nil && *input.Background {
		return input, invalidSubagent()
	}
	return input, nil
}

type OpenCodeTaskMetadata struct {
	Parent string `json:"parentSessionId"`
	Child  string `json:"sessionId"`
	Model  struct {
		Model    string `json:"modelID"`
		Provider string `json:"providerID"`
	} `json:"model"`
	Background  *bool `json:"background,omitempty"`
	Interrupted *bool `json:"interrupted,omitempty"`
	// The pinned common tool wrapper adds truncation metadata independently
	// of foreground task ownership. A locator remains inert native content.
	Truncated  *bool   `json:"truncated,omitempty"`
	OutputPath *string `json:"outputPath,omitempty"`
}

func DecodeOpenCodeTaskMetadata(raw []byte) (OpenCodeTaskMetadata, error) {
	var value OpenCodeTaskMetadata
	if Decode(raw, &value) != nil || NativeIdentity(value.Parent).Validate(OpenCode, NativeThreadIdentity) != nil || NativeIdentity(value.Child).Validate(OpenCode, NativeThreadIdentity) != nil || value.Child == value.Parent || Text(value.Model.Model, "task model", 256, true) != nil || Text(value.Model.Provider, "task provider", 256, true) != nil || value.Background != nil && *value.Background || value.OutputPath != nil && (value.Truncated == nil || !*value.Truncated || Text(*value.OutputPath, "native output locator", 32768, true) != nil) {
		return value, invalidSubagent()
	}
	return value, nil
}

func validateOpenCodeSubagent(o SubagentObservation) error {
	if o.OpenCodeTool == nil || o.OpenCodeTool.Validate() != nil || o.ParentToolID != o.OpenCodeTool.PartID || o.Tool != nil || o.Task != nil || len(o.Tools) > 0 || NativeIdentity(o.NativeID).Validate(OpenCode, NativeThreadIdentity) != nil || NativeIdentity(o.ParentID).Validate(OpenCode, NativeThreadIdentity) != nil {
		return invalidSubagent()
	}
	if o.Source == OpenCodeTaskSource && (o.Status != SubagentPending || o.Output != nil || o.ObservedModel != nil || o.Usage != nil) {
		return invalidSubagent()
	}
	if o.Source == OpenCodeChildCleanupSource {
		if o.OpenCodeCleanup == nil || o.OpenCodeCleanup.Validate() != nil || !o.OpenCodeCleanup.InterruptedObserved || o.Status != SubagentInterrupted {
			return invalidSubagent()
		}
	} else if o.OpenCodeCleanup != nil {
		return invalidSubagent()
	}
	return nil
}

func validateOpenCodeChildUsage(u SubagentUsage) error {
	var report struct {
		Input     *int64 `json:"input"`
		Output    *int64 `json:"output"`
		Reasoning *int64 `json:"reasoning"`
		Total     *int64 `json:"total,omitempty"`
		Cache     struct {
			Read  *int64 `json:"read"`
			Write *int64 `json:"write"`
		} `json:"cache"`
	}
	if u.Scope != SubagentResponseUsage || decodeUsageObject([]byte(u.NativeReport), &report) != nil {
		return invalidSubagent()
	}
	for _, count := range []*int64{report.Input, report.Output, report.Reasoning, report.Cache.Read, report.Cache.Write} {
		if count == nil || *count < 0 || uint64(*count) > maxNativeExactInteger {
			return invalidSubagent()
		}
	}
	if report.Total != nil && (*report.Total < 0 || uint64(*report.Total) > maxNativeExactInteger) || !sameSubagentCount(u.Input, report.Input) || !sameSubagentCount(u.Output, report.Output) || !sameSubagentCount(u.Total, report.Total) {
		return invalidSubagent()
	}
	return nil
}

// The native integer spelling is retained exactly; no child counters enter the
// normalized root ledger or become fabricated inclusive aggregates.
func OpenCodeChildUsageReport(input, output, reasoning, read, write uint64, total *uint64) string {
	raw, _ := json.Marshal(struct {
		Input     uint64  `json:"input"`
		Output    uint64  `json:"output"`
		Reasoning uint64  `json:"reasoning"`
		Total     *uint64 `json:"total,omitempty"`
		Cache     struct {
			Read  uint64 `json:"read"`
			Write uint64 `json:"write"`
		} `json:"cache"`
	}{input, output, reasoning, total, struct {
		Read  uint64 `json:"read"`
		Write uint64 `json:"write"`
	}{read, write}})
	return string(raw)
}
