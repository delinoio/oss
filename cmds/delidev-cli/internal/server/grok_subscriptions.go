// SPDX-License-Identifier: Apache-2.0
package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// grokIdentityCommitment derives the identity commitment persisted on a Grok
// account's SubscriptionState. The bundle's unkeyed identity key is guessable
// from a user and principal ID, so the stored value is a server-keyed,
// domain-separated HMAC over it, matching the Codex commitment strategy. The
// result is 64 hex characters, accepted by the existing commitment validation
// and by uniqueSubscriptionIdentity. It returns "" for a bundle with no identity.
func (s *Service) grokIdentityCommitment(bundle subscription.GrokBundle) string {
	key := bundle.IdentityKey()
	if key == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(s.Identity.Token))
	mac.Write([]byte("delidev/subscription/grok-identity/v1\x00" + string(s.Identity.ServerID) + "\x00" + key))
	return hex.EncodeToString(mac.Sum(nil))
}
