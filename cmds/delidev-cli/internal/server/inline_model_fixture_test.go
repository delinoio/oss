// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// Fixture source changes precede execution admission. They modify the inline
// Worker route rather than creating an independent persistent Model registry.
func fixtureInlineSource(tx *store.Tx, agentID domain.ID, provider domain.ID, service domain.SubscriptionService) (domain.ID, error) {
	row, err := tx.Get(domain.AgentKind, agentID)
	if err != nil {
		return "", err
	}
	agent, err := store.Decode[domain.Agent](row)
	if err != nil {
		return "", err
	}
	m := *agent.Routes[0].Model
	m.ProviderID, m.SubscriptionService = provider, service
	agent.Routes[0].Model = &m
	_, err = tx.Put(domain.AgentKind, row.ID, row.Revision, "", "", agent)
	return m.ModelIdentity.Key(), err
}
