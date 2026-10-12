// SPDX-License-Identifier: Apache-2.0
//go:build unix && !linux && !darwin

package security

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

func captureUnixNativeIdentity(_ string) (NativeIdentity, error) {
	return NativeIdentity{}, domain.SessionDeletionPending()
}
