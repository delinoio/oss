// SPDX-License-Identifier: Apache-2.0
package server

import (
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

const (
	prFixDispatchRetryInitial = 30 * time.Second
	prFixDispatchRetryMaximum = 5 * time.Minute
)

type prFixDispatchRetry struct {
	revision uint64
	delay    time.Duration
	after    time.Time
}

// This scheduler-local backoff covers unaccepted dispatch attempts only. It
// prevents unchanged remote failures from spending GitHub quota on every tick;
// it never repeats a claimed native job or grants execution authority. A fresh
// server epoch rechecks the original candidate rather than persisting a timer.
type prFixDispatchRetries map[domain.ID]prFixDispatchRetry

func (retries prFixDispatchRetries) ready(record store.Record, now time.Time) bool {
	retry, found := retries[record.ID]
	if !found {
		return true
	}
	if record.Revision != retry.revision {
		session, err := store.Decode[domain.Session](record)
		if err == nil && session.Dispatch == domain.DispatchReady && session.Problem == nil {
			// Explicit Stop/Resume or a freshly accepted input can clear the
			// block. Our own failed-dispatch revision retains its problem and
			// must not reset the deadline on the next scan.
			delete(retries, record.ID)
			return true
		}
	}
	return !now.Before(retry.after)
}

func (retries prFixDispatchRetries) failed(record store.Record, now time.Time) time.Duration {
	delay := retries[record.ID].delay * 2
	if delay == 0 {
		delay = prFixDispatchRetryInitial
	}
	if delay > prFixDispatchRetryMaximum {
		delay = prFixDispatchRetryMaximum
	}
	retries[record.ID] = prFixDispatchRetry{revision: record.Revision, delay: delay, after: now.Add(delay)}
	return delay
}

func (retries prFixDispatchRetries) expire(now time.Time) {
	for id, retry := range retries {
		// Keep the failure count through an eligible retry, but discard
		// abandoned sessions after a further maximum interval. Admission is
		// bounded to four attempts per one-second scan, so this also bounds
		// retained scheduler state without evicting a live cooldown.
		if !now.Before(retry.after.Add(prFixDispatchRetryMaximum)) {
			delete(retries, id)
		}
	}
}
