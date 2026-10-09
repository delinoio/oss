// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"slices"
)

type ForkPurpose string

const (
	IndependentFork ForkPurpose = ""
	SidechatFork    ForkPurpose = "SIDECHAT"
)

type SidechatPolicy string

const CodexReadOnlySidechatV1 SidechatPolicy = "codex-read-only-sidechat-v1"

func SidechatUnavailable() *Error {
	return Fail(Unsupported, "This operation cannot preserve the Sidechat's native read-only authority.", "Use its original supported Codex account and parent workspace; update the server and original Runner Device when required.")
}

func (c ExecutionConfiguration) ValidateSidechat() error {
	if c.SidechatPolicy == "" {
		return nil
	}
	if c.SidechatPolicy != CodexReadOnlySidechatV1 || c.Harness != Codex || (c.Subscription && c.SubscriptionService != SubscriptionChatGPT) || c.Options.Permission != PermissionReadOnly || c.Options.ApprovalPolicy != "never" || c.Options.ApprovalsReviewer.Effective() != CodexReviewerUser || c.Options.ApprovalReviewModel != "" || c.Options.SubagentModel != "" || c.Options.SubagentEffort != "" || c.Options.MaxConcurrency != 0 || c.SubagentModel != nil {
		return SidechatUnavailable()
	}
	return nil
}

// Preserve the original selection separately. This closed child overlay changes
// only authority: native writes, approval escalation and external/child tools
// are disabled by the pinned adapter, never by these instructions or a prompt.
func SidechatSnapshot(parent InitialExecution) (InitialExecution, error) {
	var child InitialExecution
	if parent.Configuration.SidechatPolicy != "" || parent.Configuration.Harness != Codex || (parent.Configuration.Subscription && parent.Configuration.SubscriptionService != SubscriptionChatGPT) || parent.Configuration.Validate() != nil {
		return child, SidechatUnavailable()
	}
	raw, err := json.Marshal(parent)
	if err != nil || Decode(raw, &child) != nil {
		return child, SidechatUnavailable()
	}
	c := &child.Configuration
	c.SidechatPolicy = CodexReadOnlySidechatV1
	c.ReviewerNativeModel = ""
	c.Options.ApprovalsReviewer = ""
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

// ManagedSidechatSupported composes the API read-only adapter and protected
// authentication adapter. Neither older capability independently grants access.
func ManagedSidechatSupported(capabilities []WorkerCapability) bool {
	return slices.Contains(capabilities, CodexReadOnlySidechatWorkerV1) && slices.Contains(capabilities, ManagedCodexSubscriptionsV1) && slices.Contains(capabilities, ManagedCodexSidechatV1)
}

// SubscriptionForkFinish is server-owned metadata in the original protected
// Finish receipt. Worker reports cannot manufacture this publication authority.
type SubscriptionForkFinish struct {
	JobRevision                                                 uint64 `json:"job_revision"`
	Job, Account, Machine, Instance, Device, Generation, Finish ID
}

const ManagedCodexForkV1 WorkerCapability = "managed-codex-fork-v1"

func ManagedForkSupported(capabilities []WorkerCapability) bool {
	return slices.Contains(capabilities, ManagedCodexForkV1) && slices.Contains(capabilities, ManagedCodexSubscriptionsV1)
}
func ManagedForkUnavailable() error {
	return Fail(Unsupported, "The original Runner Device does not support independent ChatGPT Fork.", "Update and reconnect the original Runner Device; retain the original account and settled history.")
}
func (i ForkJobInput) ManagedCapabilitySupported(capabilities []WorkerCapability) bool {
	if i.Purpose == SidechatFork {
		return ManagedSidechatSupported(capabilities)
	}
	return i.Purpose == IndependentFork && ManagedForkSupported(capabilities)
}
