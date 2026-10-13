// SPDX-License-Identifier: Apache-2.0
package store

import (
	"testing"
	"time"
)

func TestClearActivePricingRetainsClearInstantAndLegacyClearsAreUnknown(t *testing.T) {
	f := newReviewPricingFixture(t)
	price := preparePrice(t, f.s, f.record)
	f.clear()
	var markers int
	if err := f.s.db.QueryRow("SELECT count(*) FROM metadata WHERE key=?", pricingClearedKey(price.ID)).Scan(&markers); err != nil || markers != 1 {
		t.Fatal("clear did not retain its instant", err)
	}
	// Clearing again with no active pointer keeps the first instant.
	f.clear()
	now := time.Now().UTC().Truncate(time.Millisecond)
	f.setPricingTimes(now.Add(-2*time.Hour), now.Add(-time.Hour))
	at := func(instant time.Time) *PricingVersion {
		var got *PricingVersion
		if err := f.s.Read(t.Context(), func(tx *Tx) error {
			var err error
			got, err = tx.PricingAt(f.record.ModelID, instant)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if at(now.Add(-3*time.Hour)) != nil || at(now.Add(-90*time.Minute)) == nil || at(now.Add(-30*time.Minute)) != nil {
		t.Fatal("eligibility must follow the activation and clear instants")
	}
	// A clear from before clear history existed has no instant: report none.
	if _, err := f.s.db.Exec("DELETE FROM metadata WHERE key LIKE 'pricing-cleared-v1:%'"); err != nil {
		t.Fatal(err)
	}
	if at(now.Add(-90*time.Minute)) != nil {
		t.Fatal("a clear with unknown instant must not guess an eligible price")
	}
}
