// SPDX-License-Identifier: Apache-2.0
package domain

// ExecutionSubagentModel pins a canonical child model independently of later
// catalog edits. It borrows the parent's selected account, never child routing.
type ExecutionSubagentModel struct {
	ModelID       ID     `json:"model_key"`
	ModelRevision uint64 `json:"model_revision"`
	NativeModel   string `json:"native_model"`
}

func ValidateCodexSubagentOptions(options AgentOptions) error {
	for _, field := range []struct{ value, name string }{{options.SubagentModel, "subagent model"}, {options.SubagentEffort, "subagent effort"}} {
		if err := Text(field.value, field.name, 256, false); err != nil {
			return err
		}
	}
	return nil
}

// Observed settings remain independent of requested defaults. Existing omitted
// child-model profiles retain their original observation-only behavior. An
// explicit canonical child selection admits only that model and the root.
func (c ExecutionConfiguration) ValidateCodexChildModels(children []SubagentObservation) error {
	if c.Harness != Codex || c.Options.SubagentModel == "" {
		return nil
	}
	if c.SubagentModel == nil || c.SubagentModel.NativeModel != c.Options.SubagentModel {
		return Fail(RecoveryRequired, "The original child model snapshot is unavailable.", "Preserve the original assignment; do not infer authority from today's Agent.")
	}
	for _, child := range children {
		for _, model := range []*string{child.RequestedModel, child.ObservedModel} {
			if model != nil && *model != c.NativeModel && *model != c.SubagentModel.NativeModel {
				return Fail(PermissionDenied, "The native child model is outside this execution's authorization.", "Use only the original parent and child model under the same selected account.")
			}
		}
	}
	return nil
}
