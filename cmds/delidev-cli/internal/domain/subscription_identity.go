// SPDX-License-Identifier: Apache-2.0
package domain

// SubscriptionService is independent of API provider identity. It identifies
// the native authentication service, never an API endpoint or credential.
type SubscriptionService string

const (
	SubscriptionChatGPT    SubscriptionService = "chatgpt"
	SubscriptionClaude     SubscriptionService = "claude"
	SubscriptionGrok       SubscriptionService = "grok"
	SubscriptionOpenCodeGo SubscriptionService = "opencode_go"
)

func (s SubscriptionService) Harness() Harness {
	switch s {
	case SubscriptionChatGPT:
		return Codex
	case SubscriptionClaude:
		return ClaudeCode
	case SubscriptionGrok:
		return GrokBuild
	case SubscriptionOpenCodeGo:
		return OpenCode
	default:
		return ""
	}
}

func (s SubscriptionService) Valid() bool { return s.Harness() != "" }

type ModelSourceKind string

const SubscriptionModel ModelSourceKind = "subscription"

// MatchesAccount checks both identity families without treating an empty
// provider ID as shared authority between unrelated subscription services.
func (m Model) MatchesAccount(a Account, h Harness) bool {
	if m.SourceKind == SubscriptionModel {
		return a.Type == SubscriptionAccount && m.SubscriptionService.Valid() &&
			m.SubscriptionService == a.SubscriptionService && m.SubscriptionService.Harness() == h &&
			m.ProviderID == "" && a.ProviderID == ""
	}
	return m.SourceKind == "" && a.Type == APIAccount && m.ProviderID != "" &&
		m.ProviderID == a.ProviderID && m.SubscriptionService == "" && a.SubscriptionService == ""
}

func (m Model) SameIdentity(other Model) bool {
	return m.ProviderID == other.ProviderID && m.SourceKind == other.SourceKind &&
		m.SubscriptionService == other.SubscriptionService && m.NativeID == other.NativeID
}

func SubscriptionReconfigurationRequired() *Error {
	return Fail(RecoveryRequired, "Legacy subscription configuration was retired.",
		"Create a service account and native model, then explicitly reconfigure the affected Agent and Schedule. Historical sessions remain read-only.")
}
