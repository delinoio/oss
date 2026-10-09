// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestReviewerScopeRetainsClosedAttributionAndOriginalIdentities(t *testing.T) {
	original := Scope{
		ContextRevision: 7,
		ExecutionID:     domain.NewID(), SessionID: domain.NewID(),
		AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(),
		Harness:             domain.Codex,
		NativeModel:         domain.CodexReviewerNativeModel,
		ReviewerNativeModel: domain.CodexReviewerNativeModel,
		Attribution:         domain.BuiltinReviewerAttribution,
		Provider:            domain.Provider{Name: "Fixture", Endpoint: "http://127.0.0.1:12345/v1", Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth},
		Operations:          []Operation{ResponseCreate},
	}
	if err := original.Validate(); err != nil {
		t.Fatal("proved builtin reviewer with no catalog Model ID rejected", err)
	}
	if original.ContextRevision != 7 {
		t.Fatal("original context generation changed")
	}
	for _, test := range []struct {
		name   string
		change func(*Scope)
	}{
		{"missing execution", func(s *Scope) { s.ExecutionID = "" }},
		{"missing session", func(s *Scope) { s.SessionID = "" }},
		{"missing account", func(s *Scope) { s.AccountID = "" }},
		{"missing connection", func(s *Scope) { s.ConnectionID = "" }},
		{"missing provider", func(s *Scope) { s.ProviderID = "" }},
		{"unattributed empty model", func(s *Scope) { s.Attribution = "" }},
		{"unknown attribution", func(s *Scope) { s.Attribution = domain.UnknownReviewAttribution }},
		{"borrowed catalog model", func(s *Scope) { s.ModelID = domain.NewID() }},
		{"foreign native model", func(s *Scope) { s.NativeModel = "gpt-5.6-luna-extra" }},
		{"missing reviewer allowance", func(s *Scope) { s.ReviewerNativeModel = "" }},
		{"reviewer compaction", func(s *Scope) { s.Operations = []Operation{ResponseCompact} }},
		{"additional operation", func(s *Scope) { s.Operations = []Operation{ResponseCreate, ResponseCompact} }},
		{"title purpose", func(s *Scope) { s.Purpose = domain.SessionTitleUsage }},
		{"native subscription", func(s *Scope) { s.SubscriptionService = domain.SubscriptionChatGPT }},
	} {
		t.Run(test.name, func(t *testing.T) {
			scope := original
			test.change(&scope)
			if err := scope.Validate(); err == nil {
				t.Fatal("unproved reviewer scope accepted")
			}
		})
	}
}
