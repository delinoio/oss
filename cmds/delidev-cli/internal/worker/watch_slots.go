// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// watchSlotClass names the independent capacity pool of a watch-lane
// operation. Recovery work never competes with ordinary rows for a slot, so a
// continuous ordinary backlog cannot starve a fenced account's reconciliation,
// and recovery cannot starve ordinary rows either.
type watchSlotClass string

const (
	watchSlotOrdinary watchSlotClass = "ordinary"
	watchSlotRecovery watchSlotClass = "recovery"
)

// watchOrdinaryLaneBound is the number of ordinary account operations the watch
// lane serves at once.
const watchOrdinaryLaneBound = 4

// watchSlots admits at most one in-flight operation per key and bounds each
// class separately.
type watchSlots struct {
	mu     sync.Mutex
	held   map[domain.ID]watchSlotClass
	counts map[watchSlotClass]int
	bounds map[watchSlotClass]int
}

func newWatchSlots() *watchSlots {
	return &watchSlots{
		held:   map[domain.ID]watchSlotClass{},
		counts: map[watchSlotClass]int{},
		bounds: map[watchSlotClass]int{watchSlotOrdinary: watchOrdinaryLaneBound, watchSlotRecovery: codexRecoveryLaneBound},
	}
}

// acquire reports whether key may start now. A key already in flight, or a
// class at its bound, is skipped; the stream offers the row again.
func (s *watchSlots) acquire(key domain.ID, class watchSlotClass) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, held := s.held[key]; held || s.counts[class] >= s.bounds[class] {
		return false
	}
	s.held[key] = class
	s.counts[class]++
	return true
}

// release frees the slot acquire granted; releasing an unknown key is a no-op.
func (s *watchSlots) release(key domain.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	class, held := s.held[key]
	if !held {
		return
	}
	delete(s.held, key)
	s.counts[class]--
}
