// SPDX-License-Identifier: Apache-2.0
package domain

import "encoding/json"

type ForkPurpose string

const (
	IndependentFork ForkPurpose = ""
	SidechatFork    ForkPurpose = "SIDECHAT"
)

type SidechatPolicy string

const CodexReadOnlySidechatV1 SidechatPolicy = "codex-read-only-sidechat-v1"

func SidechatUnavailable() *Error {
	return Fail(Unsupported, "This operation cannot preserve the Sidechat's native read-only authority.", "Use its original supported Codex API account and parent workspace; update the server and original Runner Device when required.")
}

func (c ExecutionConfiguration) ValidateSidechat() error {
	if c.SidechatPolicy == "" {
		return nil
	}
	if c.SidechatPolicy != CodexReadOnlySidechatV1 || c.Harness != Codex || c.Subscription || c.Options.Permission != PermissionReadOnly || c.Options.ApprovalPolicy != "never" || c.Options.ApprovalReviewModel != "" || c.Options.SubagentModel != "" || c.Options.SubagentEffort != "" || c.Options.MaxConcurrency != 0 || c.SubagentModel != nil {
		return SidechatUnavailable()
	}
	return nil
}

// Preserve the original selection separately. This closed child overlay changes
// only authority: native writes, approval escalation and external/child tools
// are disabled by the pinned adapter, never by these instructions or a prompt.
func SidechatSnapshot(parent InitialExecution) (InitialExecution, error) {
	var child InitialExecution
	if parent.Configuration.SidechatPolicy != "" || parent.Configuration.Harness != Codex || parent.Configuration.Subscription || parent.Configuration.Validate() != nil {
		return child, SidechatUnavailable()
	}
	raw, err := json.Marshal(parent)
	if err != nil || Decode(raw, &child) != nil {
		return child, SidechatUnavailable()
	}
	c := &child.Configuration
	c.SidechatPolicy = CodexReadOnlySidechatV1
	c.Options.Permission, c.Options.ApprovalPolicy, c.Options.ApprovalReviewModel = PermissionReadOnly, "never", ""
	c.Options.SubagentModel, c.Options.SubagentEffort, c.Options.MaxConcurrency = "", "", 0
	c.SubagentModel = nil
	child.ConfigurationDigest, err = c.Digest()
	if err != nil || c.Validate() != nil {
		return InitialExecution{}, SidechatUnavailable()
	}
	return child, nil
}

func (i ForkJobInput) ChildSnapshot() (InitialExecution, error) {
	if i.Purpose == SidechatFork {
		return SidechatSnapshot(i.Snapshot)
	}
	if i.Purpose != IndependentFork {
		return InitialExecution{}, SidechatUnavailable()
	}
	return i.Snapshot, nil
}

func (i ForkJobInput) sourceWorkspace() WorkspaceType {
	// Full preparation validation belongs to the workspace owner. This bounded
	// projection only prevents a private fork assignment from selecting another
	// workspace type before that independent owner checks the complete manifest.
	var projection struct {
		Type WorkspaceType `json:"type"`
	}
	if json.Unmarshal(i.SourceAssignment.Preparation, &projection) != nil {
		return ""
	}
	return projection.Type
}

func (s Session) IsSidechat() bool {
	return s.Fork != nil && s.Fork.SidechatParentSnapshot != nil
}
