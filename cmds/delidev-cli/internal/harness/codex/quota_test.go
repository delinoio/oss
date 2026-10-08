// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPinnedNativeQuotaProjectionPreservesSparseAndCreditInventory(t *testing.T) {
	raw := `{"rateLimits":{"limitId":"codex","limitName":"private display","primary":{"usedPercent":80,"windowDurationMins":300,"resetsAt":1900000000},"secondary":{"usedPercent":20,"windowDurationMins":10080,"resetsAt":1900000000},"credits":{"hasCredits":true,"unlimited":false,"balance":"private balance"},"individualLimit":null,"spendControlReached":null,"planType":"pro","rateLimitReachedType":null},"rateLimitsByLimitId":null,"rateLimitResetCredits":{"availableCount":2,"credits":[{"id":"credit_1","resetType":"codexRateLimits","status":"available","grantedAt":1700000000,"expiresAt":null,"title":"private title","description":"private description"}]}}`
	var native nativeQuotaRead
	raw = strings.Replace(raw, `"limitName":"private display"`, `"limitName":"private display","normalModelSlug":"private model"`, 1)
	if err := domain.Decode([]byte(raw), &native); err != nil {
		t.Fatal(err)
	}
	v, err := projectQuota(native, domain.NewID(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Windows) != 2 || *v.Windows[0].Remaining != 0.2 || *v.Windows[1].Remaining != 0.8 || v.Credits.AvailableCount != 2 || len(*v.Credits.Credits) != 1 {
		t.Fatal("native values lost", v)
	}
	encoded, _ := json.Marshal(v)
	if strings.Contains(string(encoded), "private") {
		t.Fatal("native display/billing content escaped projection")
	}
	native.Legacy.Primary = nil
	native.Legacy.Secondary = nil
	native.ResetCredits.Credits = nil
	sparse, err := projectQuota(native, domain.NewID(), time.Now().UTC())
	if err != nil || len(sparse.Windows) != 0 || sparse.Credits.Credits != nil || sparse.Credits.AvailableCount != 2 {
		t.Fatal("sparse or count-only fields were fabricated")
	}
	native.ResetCredits.Count = new(int64)
	*native.ResetCredits.Count = -1
	if _, err := projectQuota(native, domain.NewID(), time.Now().UTC()); err == nil {
		t.Fatal("negative count accepted")
	}
}
func TestPinnedNativeQuotaRefusesAmbiguousBucketsAndMissingUsedPercent(t *testing.T) {
	now := time.Now().UTC()
	id := "codex"
	other := "other"
	percent := int32(101)
	for _, native := range []nativeQuotaRead{{}, {Legacy: &nativeQuotaSnapshot{Primary: &nativeQuotaWindow{}}}, {Legacy: &nativeQuotaSnapshot{Primary: &nativeQuotaWindow{UsedPercent: &percent}}}, {Legacy: &nativeQuotaSnapshot{LimitID: &id}, Buckets: map[string]nativeQuotaSnapshot{"codex": {LimitID: &other}}}} {
		if _, err := projectQuota(native, domain.NewID(), now); err == nil {
			t.Fatal("ambiguous native observation accepted")
		}
	}
}

func TestManagedNativeResetLostResponseReconcilesOnlyOriginalOfficialKey(t *testing.T) {
	config := fixtureConfig(t, "managed-quota-lost-response")
	config.Mode = SubscriptionProtocol
	config.ManagedAuthentication = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	native, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := native.StartManagedLogin(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := native.WaitManagedLogin(ctx, progress.LoginID); err != nil {
		t.Fatal(err)
	}
	observed, err := native.ReadManagedQuota(ctx, domain.NewID())
	if err != nil || observed.Credits.AvailableCount != 2 || len(*observed.Credits.Credits) != 1 {
		t.Fatal("native non-inference inventory failed", err)
	}
	op := domain.SubscriptionObservationOperation{ID: domain.NewID(), Action: domain.SubscriptionResetCredit, MachineID: domain.NewID(), Actor: domain.Principal{Type: domain.OwnerDevice}, ConnectionID: domain.NewID(), Generation: domain.NewID(), Phase: domain.SubscriptionObservationSending, RequestedAt: time.Now().UTC(), CreditID: "credit_1", CreditsObservationID: observed.Credits.ObservationID}
	bounded, stop := context.WithTimeout(ctx, 150*time.Millisecond)
	_, err = native.ConsumeManagedResetCredit(bounded, op)
	stop()
	if err == nil {
		t.Fatal("lost response invented successful consumption")
	}
	if err := native.Close(); err != nil {
		t.Fatal(err)
	}
	// A new original-owner fixture process reads the provider's durable operation
	// key. The server will independently admit this only after joined cleanup.
	native, err = Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	outcome, err := native.ConsumeManagedResetCredit(ctx, op)
	if err != nil || outcome != domain.SubscriptionAlreadyRedeemed {
		t.Fatal("same-key native reconciliation failed", outcome, err)
	}
	key, err := os.ReadFile(filepath.Join(config.Home, "credit-key"))
	if err != nil || string(key) != string(op.ID) {
		t.Fatal("provider attempt acquired a replacement key", err)
	}
}
