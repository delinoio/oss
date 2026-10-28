// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"time"
)

type NativeRecoveryClass string

const (
	NativeRecoveryInitial NativeRecoveryClass = "initial"
	NativeRecoveryRetry   NativeRecoveryClass = "retry"
	NativeRecoveryProbe   NativeRecoveryClass = "probe"
)

type NativeRecoveryLimitError struct{ RetryAt time.Time }

func (e *NativeRecoveryLimitError) Error() string { return "native recovery budget exhausted" }
func (e *NativeRecoveryLimitError) Unwrap() error { return ErrBusy }
func (s *Store) chargeNativeRecovery(ctx context.Context, tx *sql.Tx, l Lease, pool string, class NativeRecoveryClass) (retryAt time.Time, e error) {
	if pool == "" || len(pool) > 512 || (class != NativeRecoveryInitial && class != NativeRecoveryRetry && class != NativeRecoveryProbe) {
		return retryAt, ErrConflict
	}
	sc := l.Principal.Scope
	var now time.Time
	if e = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); e != nil {
		return
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM native_recovery_buckets WHERE org_id=$1 AND user_id=$2 AND pool_key=$3 AND bucket_start<=$4::timestamptz-interval '10 seconds'", sc.OrganizationID, sc.UserID, pool, now); e != nil {
		return
	}
	var initials, recoveries int64
	var oldest sql.NullTime
	e = tx.QueryRowContext(ctx, "SELECT COALESCE(sum(initials),0),COALESCE(sum(recoveries),0),min(bucket_start) FROM native_recovery_buckets WHERE org_id=$1 AND user_id=$2 AND pool_key=$3 AND bucket_start>$4::timestamptz-interval '10 seconds'", sc.OrganizationID, sc.UserID, pool, now).Scan(&initials, &recoveries, &oldest)
	if e != nil {
		return
	}
	if class != NativeRecoveryInitial {
		limit := initials / 5
		if limit < 3 {
			limit = 3
		}
		if recoveries >= limit {
			retryAt = oldest.Time.Add(10 * time.Second)
			return retryAt, &NativeRecoveryLimitError{retryAt}
		}
	}
	initial, recovery := 0, 0
	if class == NativeRecoveryInitial {
		initial = 1
	} else {
		recovery = 1
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO native_recovery_buckets(org_id,user_id,pool_key,bucket_start,initials,recoveries) VALUES($1,$2,$3,date_trunc('second',$4::timestamptz),$5,$6) ON CONFLICT(org_id,user_id,pool_key,bucket_start) DO UPDATE SET initials=native_recovery_buckets.initials+excluded.initials,recoveries=native_recovery_buckets.recoveries+excluded.recoveries", sc.OrganizationID, sc.UserID, pool, now, initial, recovery)
	return
}
func (s *Store) ChargeNativeRecovery(ctx context.Context, l Lease, poolKey string, class NativeRecoveryClass) (retryAt time.Time, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkDispatchLease(ctx, tx, l); e != nil {
			return e
		}
		var e error
		retryAt, e = s.chargeNativeRecovery(ctx, tx, l, poolKey, class)
		return e
	})
	return
}
