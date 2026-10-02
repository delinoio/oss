// SPDX-License-Identifier: Apache-2.0
package server

import (
	"testing"
	"time"
)

func TestEvictAutomaticPRCooldownKeepsActiveAndEvictsOldest(t *testing.T) {
	active := map[string]bool{"active": true}
	now := time.Now()
	next := map[string]time.Time{
		"active": now.Add(-time.Hour),
		"old":    now.Add(-time.Minute),
		"new":    now.Add(time.Minute),
	}
	delay := map[string]time.Duration{"active": time.Minute, "old": time.Minute, "new": time.Minute}

	evictAutomaticPRCooldown(active, next, delay)
	if _, ok := next["old"]; ok {
		t.Fatal("oldest inactive cooldown was not evicted")
	}
	if _, ok := delay["old"]; ok {
		t.Fatal("evicted cooldown retained retry delay")
	}
	if _, ok := next["active"]; !ok {
		t.Fatal("active cooldown was evicted")
	}
}
