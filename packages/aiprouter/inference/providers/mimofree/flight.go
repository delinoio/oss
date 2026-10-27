// SPDX-License-Identifier: Apache-2.0
package mimofree

import (
	"context"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

// Flights contain only synchronization handles. The encrypted store remains the
// token authority. A canceled owner releases its flight so another caller can retry
// under its own authorization and send budget, without detached provider I/O.
type flight struct {
	gate  chan struct{}
	users int
}

func (a *Adapter) acquire(ctx context.Context, key string) (func(), error) {
	a.mu.Lock()
	if a.flights == nil {
		a.flights = map[string]*flight{}
	}
	f := a.flights[key]
	if f == nil {
		f = &flight{gate: make(chan struct{}, 1)}
		f.gate <- struct{}{}
		a.flights[key] = f
	}
	f.users++
	a.mu.Unlock()
	drop := func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		f.users--
		if f.users == 0 {
			delete(a.flights, key)
		}
	}
	select {
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case <-f.gate:
		return func() { f.gate <- struct{}{}; drop() }, nil
	}
}
func (a *Adapter) token(ctx context.Context, exec inference.AdapterExecutor, store inference.AdapterStateStore, s session, rejected string) (session, error) {
	release, err := a.acquire(ctx, s.Client+"/"+s.Affinity)
	if err != nil {
		return s, err
	}
	defer release()
	current, err := loadSession(ctx, store)
	if err != nil {
		return s, err
	}
	if current.Client != s.Client || current.Affinity != s.Affinity {
		return s, inference.ErrContinuation
	}
	if current.JWT != "" && current.JWT != rejected && time.Now().Before(current.Expires.Add(-5*time.Minute)) {
		return current, nil
	}
	// An older failed request cannot erase a newer token issued by another replica.
	// Clearing uses a compare-and-swap against the token actually observed here.
	current, err = saveToken(ctx, store, current, "", time.Time{})
	if err != nil {
		return s, err
	}
	if current.JWT != "" && current.JWT != rejected && time.Now().Before(current.Expires.Add(-5*time.Minute)) {
		return current, nil
	}
	return bootstrap(ctx, exec, store, current)
}
