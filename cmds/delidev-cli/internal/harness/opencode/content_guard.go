// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func (s *sessionAPI) contentGuard() security.ProtectedJSON {
	values := append(slices.Clone(s.protectedValues), s.password)
	if s.apiProfile != nil {
		values = append(values, s.apiProfile.Token)
	}
	return security.NewProtectedJSON(values)
}

func protectedContentRefused() *domain.Error {
	return domain.Fail(domain.Unsupported, "OpenCode native content contains protected runtime data.", "Preserve the original runtime and cleanup evidence; do not resend input or replace native content.")
}
