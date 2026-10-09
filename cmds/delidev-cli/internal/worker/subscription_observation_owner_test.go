// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
	"time"
)

func TestAutomaticCreditObserverRemovalJoinsOriginalOwner(t *testing.T) {
	registry := &managedObservationRegistry{}
	account := domain.NewID()
	remove := registry.register(account, nil, nil)
	owner := registry.owners[account]
	owner.mu.Lock()
	done := make(chan struct{})
	go func() { remove(); close(done) }()
	deadline := time.After(time.Second)
	for {
		registry.mu.Lock()
		_, present := registry.owners[account]
		registry.mu.Unlock()
		if !present {
			break
		}
		select {
		case <-deadline:
			t.Fatal("owner not fenced")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case <-done:
		t.Fatal("cleanup overtook retained observation")
	default:
	}
	if handled, _ := registry.run(context.Background(), account, domain.SubscriptionObservationOperation{}); handled {
		t.Fatal("fenced owner acquired new observation")
	}
	owner.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("original observer not joined")
	}
	remove()
}
func TestAutomaticCreditLateRemovalPreservesReplacementOwner(t *testing.T) {
	registry := &managedObservationRegistry{}
	account := domain.NewID()
	old := registry.register(account, nil, nil)
	registry.register(account, nil, nil)
	replacement := registry.owners[account]
	old()
	if registry.owners[account] != replacement || replacement.closed {
		t.Fatal("old cleanup removed replacement owner")
	}
}
