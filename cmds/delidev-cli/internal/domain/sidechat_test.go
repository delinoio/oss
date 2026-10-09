// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSidechatSnapshotPreservesSelectionAndFreezesNativeAuthority(t *testing.T) {
	id := NewID()
	options := AgentOptions{ApprovalsReviewer: CodexReviewerAuto, Permission: PermissionWorkspaceWrite, ApprovalPolicy: "on-request", SubagentModel: "child", SubagentEffort: "low", MaxConcurrency: 4, ServiceTier: "fast"}
	c, err := resolveInlineFixture(NewID(), 3, Agent{Name: "Parent", Harness: Codex, ModelID: NewID(), Effort: "high", Options: options, Accounts: []WeightedAccount{{ID: NewID(), Weight: 1}}, Templates: []ID{id}}, 7, Model{Name: "Parent", NativeID: "native-parent", ProviderID: NewID(), Harnesses: []Harness{Codex}, MetadataSource: UserDeclared}, Priority, []AppliedTemplate{{ID: id, Revision: 5, Contents: "Original parent instructions\n"}})
	if err != nil {
		t.Fatal(err)
	}
	c.SubagentModel = &ExecutionSubagentModel{ModelID: (ModelIdentity{ProviderID: c.ProviderID, SubscriptionService: c.SubscriptionService, NativeID: "child"}).Key(), ModelRevision: 6, NativeModel: "child"}
	digest, _ := c.Digest()
	parent := InitialExecution{ID: NewID(), InputID: NewID(), Configuration: c, ConfigurationDigest: digest, InitialAccountID: NewID(), ConnectionID: NewID(), AcceptedAt: time.Now().UTC()}
	before, _ := json.Marshal(parent)
	child, err := SidechatSnapshot(parent)
	if err != nil || child.Configuration.Validate() != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(parent)
	if string(before) != string(after) {
		t.Fatal("child overlay changed parent snapshot")
	}
	if child.ID != parent.ID || child.InputID != parent.InputID || child.InitialAccountID != parent.InitialAccountID || child.ConnectionID != parent.ConnectionID || child.Configuration.NativeModel != c.NativeModel || child.Configuration.Effort != c.Effort || child.Configuration.Instructions != c.Instructions || child.Configuration.Options.ServiceTier != "fast" || child.ConfigurationDigest == parent.ConfigurationDigest {
		t.Fatal("Sidechat changed original model, account, route or instructions")
	}
	if child.Configuration.Options.ApprovalsReviewer.Effective() != CodexReviewerUser || child.Configuration.ReviewerNativeModel != "" || child.Configuration.Options.Permission != PermissionReadOnly || child.Configuration.Options.ApprovalPolicy != "never" || child.Configuration.Options.SubagentModel != "" || child.Configuration.SubagentModel != nil {
		t.Fatal("overlay retained expanded native authority")
	}
	parent.Configuration.Templates[0].Contents = "Later edit"
	parent.Configuration.Accounts[0].Weight = 9
	if child.Configuration.Templates[0].Contents != c.Instructions || child.Configuration.Accounts[0].Weight != 1 {
		t.Fatal("snapshot references later parent mutation")
	}
	for _, change := range []string{"write", "approval", "children", "unknown"} {
		bad := child.Configuration
		switch change {
		case "write":
			bad.Options.Permission = PermissionWorkspaceWrite
		case "approval":
			bad.Options.ApprovalPolicy = "on-request"
		case "children":
			bad.Options.MaxConcurrency = 1
		case "unknown":
			bad.SidechatPolicy = "unknown"
		}
		if bad.Validate() == nil {
			t.Fatal("expanded Sidechat authority accepted", change)
		}
	}
	if _, err := SidechatSnapshot(child); err == nil {
		t.Fatal("nested Sidechat inherited authority")
	}
}

func TestManagedSidechatRetainsChatGPTSelectorAndReadOnlyOverlay(t *testing.T) {
	config := managedSubscriptionExecutionConfiguration(t, PermissionWorkspaceWrite)
	config.ProviderID, config.SubscriptionService = "", SubscriptionChatGPT
	digest, err := config.Digest()
	if err != nil {
		t.Fatal(err)
	}
	parent := InitialExecution{ID: NewID(), InputID: NewID(), Configuration: config, ConfigurationDigest: digest, InitialAccountID: NewID(), ConnectionID: NewID(), AcceptedAt: time.Now().UTC()}
	before, _ := json.Marshal(parent)
	child, err := SidechatSnapshot(parent)
	if err != nil || !child.Configuration.Subscription || child.Configuration.SubscriptionService != SubscriptionChatGPT || child.InitialAccountID != parent.InitialAccountID || child.ConnectionID != parent.ConnectionID || child.Configuration.Options.Permission != PermissionReadOnly || child.Configuration.Options.ApprovalPolicy != "never" {
		t.Fatal("managed selection or overlay changed", err)
	}
	after, _ := json.Marshal(parent)
	if string(before) != string(after) {
		t.Fatal("managed overlay rewrote parent")
	}
	for _, service := range []SubscriptionService{SubscriptionClaude, SubscriptionGrok, ""} {
		bad := parent
		bad.Configuration.SubscriptionService = service
		if _, err := SidechatSnapshot(bad); err == nil {
			t.Fatal("unsupported managed service", service)
		}
	}
}
