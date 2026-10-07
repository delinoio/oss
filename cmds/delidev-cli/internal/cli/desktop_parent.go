// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"crypto/rand"
	"encoding/base64"
)

func randomDesktopKey() (string, error) {
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw), err
}
