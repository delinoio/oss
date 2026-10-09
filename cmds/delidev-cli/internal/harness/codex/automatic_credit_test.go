// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestAutomaticCreditClosedNativeQuotaMarker(t *testing.T) {
	for _, tag := range []string{`"usageLimitExceeded"`, `"rateLimitExceeded"`, `"contextWindowExceeded"`, `"sessionBudgetExceeded"`, `"serverOverloaded"`, `{"usageLimitExceeded":true}`, `null`, `"USAGE_LIMIT_EXCEEDED"`} {
		t.Run(tag, func(t *testing.T) {
			raw := json.RawMessage(`{"id":"` + string(domain.NewID()) + `","items":[],"status":"failed","error":{"message":"usageLimitExceeded rateLimitExceeded","codexErrorInfo":` + tag + `}}`)
			turn, err := decodeTurn(raw)
			if err != nil {
				t.Fatal(err)
			}
			expected := tag == `"usageLimitExceeded"` || tag == `"rateLimitExceeded"`
			if turn.QuotaBlock.Valid() != expected {
				t.Fatal("unrelated error gained quota authority", turn.QuotaBlock)
			}
		})
	}
}
