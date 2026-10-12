// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
)

// ClaudeToken is a separate protected envelope, never a Codex auth file or an
// inferred human identity. Server/account/generation are original vault fences.
type ClaudeToken struct {
	Version    uint32    `json:"version"`
	Server     domain.ID `json:"server"`
	Account    domain.ID `json:"account"`
	Generation domain.ID `json:"generation"`
	Token      string    `json:"token"`
}

func ParseClaudeToken(raw []byte, server, account, generation domain.ID) (ClaudeToken, error) {
	var value ClaudeToken
	if len(raw) == 0 || len(raw) > 16<<10 || domain.Decode(raw, &value) != nil || value.Version != 1 || value.Server != server || value.Account != account || value.Generation != generation || server.Validate() != nil || account.Validate() != nil || generation.Validate() != nil || len(value.Token) == 0 || len(value.Token) > 8192 || strings.ContainsAny(value.Token, "\x00\r\n\t ") {
		return value, Invalid()
	}
	for _, b := range []byte(value.Token) {
		if b < 33 || b > 126 {
			return value, Invalid()
		}
	}
	return value, nil
}
func SealClaudeToken(server, account, generation domain.ID, token []byte) ([]byte, error) {
	raw, err := json.Marshal(ClaudeToken{1, server, account, generation, string(token)})
	if err != nil {
		return nil, Invalid()
	}
	if _, err = ParseClaudeToken(raw, server, account, generation); err != nil {
		clear(raw)
		return nil, err
	}
	return raw, nil
}
