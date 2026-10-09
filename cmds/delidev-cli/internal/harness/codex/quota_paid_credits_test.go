// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
	"time"
)

func TestPaidCreditNativeProjectionAndReflection(t *testing.T) {
	for _, balance := range []string{`"1250.50"`, `"0"`, `null`} {
		raw := `{"rateLimits":{"limitId":"codex","credits":{"hasCredits":true,"unlimited":false,"balance":` + balance + `}}}`
		var native nativeQuotaRead
		if json.Unmarshal([]byte(raw), &native) != nil {
			t.Fatal("fixture")
		}
		v, err := projectQuota(native, domain.NewID(), time.Now().UTC())
		if err != nil || len(v.PaidCredits) != 1 {
			t.Fatal("projection", err)
		}
		if balance == `null` && v.PaidCredits[0].Balance != nil {
			t.Fatal("null fabricated")
		}
		if balance == `"1250.50"` && (v.PaidCredits[0].Balance == nil || *v.PaidCredits[0].Balance != "1250.50") {
			t.Fatal("precision changed")
		}
		if balance == `"1250.50"` && ValidateQuotaSecrets(v, "1250.50") == nil {
			t.Fatal("short reflected credential permitted")
		}
	}
	for _, raw := range []string{`{"hasCredits":true,"unlimited":false,"balance":"-1"}`, `{"hasCredits":true,"unlimited":false,"balance":"1e4"}`, `{"unlimited":true,"balance":null}`} {
		var native nativeQuotaRead
		json.Unmarshal([]byte(`{"rateLimits":{"limitId":"codex","credits":`+raw+`}}`), &native)
		if _, err := projectQuota(native, domain.NewID(), time.Now().UTC()); err == nil {
			t.Fatal("invalid paid projection accepted")
		}
	}
}

func TestPaidCreditOmittedBalanceDoesNotRefreshEvidence(t *testing.T) {
	var native nativeQuotaRead
	if json.Unmarshal([]byte(`{"rateLimits":{"limitId":"codex","credits":{"hasCredits":true,"unlimited":false}}}`), &native) != nil {
		t.Fatal("fixture")
	}
	v, err := projectQuota(native, domain.NewID(), time.Now().UTC())
	if err != nil || len(v.PaidCredits) != 0 {
		t.Fatal("sparse omission fabricated fresh null", err)
	}
}
