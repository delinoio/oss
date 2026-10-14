// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
)

func managedTitleInput() AuxiliaryTitleInput {
	return AuxiliaryTitleInput{
		Version: 3, SessionID: NewID(), OperationID: NewID(), NameGeneration: 1,
		OriginalJobID: NewID(), OriginalExecutionID: NewID(), MachineID: NewID(),
		OriginalDeviceID: NewID(), OriginalInstanceID: NewID(), AgentID: NewID(),
		Harness: Codex, NativeVersion: "0.159.2", AccountID: NewID(), ConnectionID: NewID(),
		ModelID: (ModelIdentity{SubscriptionService: SubscriptionChatGPT, NativeID: "fixture-model"}).Key(), NativeModel: "fixture-model", Prompt: "First input",
		Startup: &ExecutionStartupSelection{Harness: Codex, ExplicitPath: "/native/codex", ExecutableSHA256: strings.Repeat("a", 64)},
		Managed: &ManagedTitleAuthority{SubscriptionService: SubscriptionChatGPT, Generation: NewID(), IdentityCommitment: strings.Repeat("c", 64)},
	}
}

func apiTitleInput(version uint32) AuxiliaryTitleInput {
	provider := NewID()
	input := AuxiliaryTitleInput{
		Version: version, SessionID: NewID(), OperationID: NewID(), NameGeneration: 1,
		OriginalJobID: NewID(), OriginalExecutionID: NewID(), MachineID: NewID(),
		OriginalDeviceID: NewID(), OriginalInstanceID: NewID(), AgentID: NewID(),
		Harness: Codex, NativeVersion: CodexProtocolVersion, Executable: "/private/codex", AccountID: NewID(), ConnectionID: NewID(),
		ProviderID: provider, ProviderProtocol: OpenAIResponses, NativeModel: "fixture-model", Prompt: "First input",
	}
	input.ModelID = (ModelIdentity{ProviderID: provider, NativeID: input.NativeModel}).Key()
	if version == 2 {
		input.Startup = &ExecutionStartupSelection{Harness: Codex, ExplicitPath: "/native/codex", ExecutableSHA256: strings.Repeat("a", 64)}
	}
	return input
}

func TestManagedTitleAssignmentFreezesIdentityWithoutAPIAuthority(t *testing.T) {
	input := managedTitleInput()
	if err := input.Validate(); err != nil || !input.IsManaged() {
		t.Fatalf("valid managed assignment rejected: %v", err)
	}
	for name, change := range map[string]func(*AuxiliaryTitleInput){
		"provider":           func(i *AuxiliaryTitleInput) { i.ProviderID = NewID() },
		"provider-protocol":  func(i *AuxiliaryTitleInput) { i.ProviderProtocol = OpenAIResponses },
		"executable":         func(i *AuxiliaryTitleInput) { i.Executable = "/other/codex" },
		"missing-managed":    func(i *AuxiliaryTitleInput) { i.Managed = nil },
		"missing-startup":    func(i *AuxiliaryTitleInput) { i.Startup = nil },
		"missing-hash":       func(i *AuxiliaryTitleInput) { i.Startup.ExecutableSHA256 = "" },
		"wrong-service":      func(i *AuxiliaryTitleInput) { i.Managed.SubscriptionService = SubscriptionOpenCodeGo },
		"missing-generation": func(i *AuxiliaryTitleInput) { i.Managed.Generation = "" },
		"missing-identity":   func(i *AuxiliaryTitleInput) { i.Managed.IdentityCommitment = "" },
		"api-model-key":      func(i *AuxiliaryTitleInput) { i.ModelID = (ModelIdentity{ProviderID: NewID(), NativeID: "m"}).Key() },
		"bad-actor":          func(i *AuxiliaryTitleInput) { i.Managed.ActorDeviceID = "not-an-id" },
	} {
		changed := managedTitleInput()
		change(&changed)
		if changed.Validate() == nil {
			t.Fatalf("%s: unsafe managed assignment was accepted", name)
		}
	}
	// Historical API versions keep their exact authority and cannot carry the branch.
	for _, version := range []uint32{1, 2} {
		api := apiTitleInput(version)
		if err := api.Validate(); err != nil || api.IsManaged() {
			t.Fatalf("historical version %d changed: %v", version, err)
		}
		api.Managed = managedTitleInput().Managed
		if api.Validate() == nil {
			t.Fatalf("version %d accepted a managed branch", version)
		}
	}
	absent := apiTitleInput(2)
	absent.NativeVersion = ""
	if absent.Validate() != nil {
		t.Fatal("absent optional native version metadata must not change version-2 admission")
	}
	unknown := apiTitleInput(1)
	unknown.Version = 4
	if unknown.Validate() == nil {
		t.Fatal("unknown assignment version was accepted")
	}
}

func TestManagedTitleResultRequiresProtectedFinishAndSubscriptionUsage(t *testing.T) {
	input := managedTitleInput()
	finish := NewID()
	result := AuxiliaryTitleResult{Version: 2, OperationID: input.OperationID, NameGeneration: 1, Title: "A title", CleanupVerified: true, ManagedFinish: finish}
	if err := result.Validate(input); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*AuxiliaryTitleResult){
		"api-version":   func(r *AuxiliaryTitleResult) { r.Version = 1 },
		"no-finish":     func(r *AuxiliaryTitleResult) { r.ManagedFinish = "" },
		"no-cleanup":    func(r *AuxiliaryTitleResult) { r.CleanupVerified = false },
		"multiline":     func(r *AuxiliaryTitleResult) { r.Title = "a\nb" },
		"oversized":     func(r *AuxiliaryTitleResult) { r.Title = strings.Repeat("x", 257) },
		"foreign-usage": func(r *AuxiliaryTitleResult) { r.UsageRecord = &ResponseUsageRecord{Purpose: SessionTitleUsage} },
	} {
		changed := result
		change(&changed)
		if changed.Validate(input) == nil {
			t.Fatalf("%s: invalid managed result was accepted", name)
		}
	}
	api := apiTitleInput(1)
	legacy := AuxiliaryTitleResult{Version: 1, OperationID: api.OperationID, NameGeneration: 1, Title: "A title", CleanupVerified: true}
	if legacy.Validate(api) != nil {
		t.Fatal("historical API result changed")
	}
	legacy.ManagedFinish = finish
	if legacy.Validate(api) == nil {
		t.Fatal("API result accepted a managed Finish reference")
	}
}

func TestManagedTitlesRequireEveryNegotiatedCapability(t *testing.T) {
	all := []WorkerCapability{ManagedCodexTitlesV1, AutomaticTitlesCodexV1, ManagedCodexSubscriptionsV1, ExecutionStartupV1}
	if !ManagedTitlesSupported(all) {
		t.Fatal("complete capability set rejected")
	}
	for index := range all {
		partial := append([]WorkerCapability{}, all[:index]...)
		partial = append(partial, all[index+1:]...)
		if ManagedTitlesSupported(partial) {
			t.Fatalf("managed titles were admitted without %s", all[index])
		}
	}
}

func TestResponseUsageRecordAllowsSubscriptionTitlePurpose(t *testing.T) {
	record := ResponseUsageRecord{
		Purpose: SessionTitleUsage, SessionID: NewID(), ExecutionID: NewID(), AccountID: NewID(), ConnectionID: NewID(),
		SubscriptionService: SubscriptionChatGPT, ModelID: (ModelIdentity{SubscriptionService: SubscriptionChatGPT, NativeID: "m"}).Key(),
		Harness: Codex, Version: "0.159.2", ThreadID: string(NewID()), TurnID: string(NewID()), Sequence: 1,
		Usage: NativeResponseUsage{ResponseDigest: strings.Repeat("e", 64), CostEvidence: UsageCostMissing},
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("subscription title usage rejected: %v", err)
	}
	record.ProviderID = NewID()
	if record.Validate() == nil {
		t.Fatal("subscription usage fabricated provider evidence")
	}
}
