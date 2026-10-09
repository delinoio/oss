// SPDX-License-Identifier: Apache-2.0
package store

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// This internal value decodes an immutable source key. It never reads, saves or
// returns a Model resource through the protocol. Native/account checks remain
// separately owned; declared direct IDs imply no model availability.
func inlineModelRecord(key domain.ID) (Record, error) {
	identity, e := domain.ParseModelKey(key)
	if e != nil {
		return Record{}, e
	}
	m := domain.Model{Name: identity.NativeID, NativeID: identity.NativeID, ProviderID: identity.ProviderID, SubscriptionService: identity.SubscriptionService, Harnesses: []domain.Harness{domain.Codex, domain.ClaudeCode, domain.OpenCode, domain.GrokBuild}, MetadataSource: domain.Unknown}
	if identity.SubscriptionService != "" {
		m.SourceKind = domain.SubscriptionModel
		m.Harnesses = []domain.Harness{identity.SubscriptionService.Harness()}
	}
	raw, e := json.Marshal(m)
	return Record{ID: key, Revision: 1, Data: raw}, e
}
