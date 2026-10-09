// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

// Original disconnected accounts remain disconnected: inline fixture configuration
// supplies source identity, never credentials, readiness or native acceptance.
func cliInlineFixtureRoute(provider domain.ID, native string, accounts []domain.WeightedAccount) domain.AgentSourceRoute {
	return domain.AgentSourceRoute{Model: &domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: provider, NativeID: native}, MetadataSource: domain.UserDeclared, InputModalities: []string{"text"}}, Accounts: accounts}
}

func TestCurrentInlineCLIFixtureUsesOriginalAccountSource(t *testing.T) {
	provider, account := domain.NewID(), domain.NewID()
	route := cliInlineFixtureRoute(provider, "original/native", []domain.WeightedAccount{{ID: account, Weight: 1}})
	agent := domain.Agent{Name: "Fixture", Harness: domain.Codex, Routes: []domain.AgentSourceRoute{route}, Options: domain.AgentOptions{Permission: domain.PermissionDefault}}
	if err := agent.Validate(); err != nil {
		t.Fatal(err)
	}
	disconnected := domain.Account{Alias: "Disconnected", Type: domain.APIAccount, ProviderID: provider, Enabled: true, Health: domain.AccountDisconnected}
	if !route.Model.MatchesAccount(disconnected) || disconnected.Connection != nil || disconnected.Health != domain.AccountDisconnected {
		t.Fatal("fixture granted credentials/readiness or lost original source")
	}
}
