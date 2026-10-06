// SPDX-License-Identifier: Apache-2.0
package server

import (
	"fmt"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func scope(f store.Filter) string {
	base := fmt.Sprintf("page:%s:%s:%s:%s:%s", f.Kind, f.SessionID, f.ProjectID, f.AccountType, f.ProviderID)
	if f.SubscriptionService != "" {
		return base + ":subscription:" + string(f.SubscriptionService)
	}
	return base
}

func listScope(f store.Filter, providerID string) string {
	if providerID == "" {
		return scope(f)
	}
	return scope(f) + ":provider:" + providerID
}
