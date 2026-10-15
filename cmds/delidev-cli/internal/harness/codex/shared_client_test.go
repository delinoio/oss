// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSharedRuntimeBindsIndependentNativeThreads(t *testing.T) {
	for _, count := range []int{2, 8} {
		t.Run(fmt.Sprintf("members-%d", count), func(t *testing.T) {
			testSharedRuntimeBindsIndependentNativeThreads(t, count)
		})
	}
}

func testSharedRuntimeBindsIndependentNativeThreads(t *testing.T, count int) {
	config := fixtureConfig(t, "thread-managed-shared")
	config.Mode = ThreadProtocol
	config.ManagedAuthentication = true
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	owner, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := owner.StartSharedRuntime()
	if err != nil {
		_ = owner.Close()
		t.Fatal(err)
	}
	defer func() {
		if nativeErr, helperErr := shared.Close(); nativeErr != nil || helperErr != nil {
			t.Errorf("account closure: %v, %v", nativeErr, helperErr)
		}
	}()
	members := make([]*Client, count)
	for i := range members {
		memberConfig := config
		memberConfig.Process.OwnerID = domain.NewID()
		members[i], err = shared.NewMember(memberConfig)
		if err != nil {
			t.Fatal(err)
		}
	}
	settings := threadSettings(t)
	settings.Provider = "openai"
	results := make([]ThreadResult, count)
	failures := make([]error, count)
	var wg sync.WaitGroup
	for i, member := range members {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], failures[i] = member.StartThread(ctx, domain.NewID(), settings)
		}()
	}
	wg.Wait()
	// Every concurrent member isolated the shared account home before binding.
	if isolationProjectLevel(t, config.Home, settings.Cwd) != "untrusted" {
		t.Fatal("shared account home did not record the project as untrusted")
	}
	identities := make(map[domain.ID]bool)
	for i, result := range results {
		if failures[i] != nil || result.Thread == nil || identities[result.Thread.ID] {
			t.Fatalf("member %d has no distinct thread: %#v, %v", i, result.Thread, failures[i])
		}
		identities[result.Thread.ID] = true
	}
	for _, member := range members {
		event, err := member.NextEvent(ctx)
		if err != nil || event.ThreadID != member.thread || !event.Correlated {
			t.Fatalf("wrong member event: %#v, %v", event, err)
		}
	}
	if nativeErr, _ := members[0].CloseWithExecutionHelpers(); domain.SafeError(nativeErr).Code != domain.RecoveryRequired {
		t.Fatalf("member closed account process: %v", nativeErr)
	}
	for i, member := range members {
		if err := member.RetireSharedMember(ctx, domain.NewID()); err != nil {
			t.Fatalf("member %d retirement: %v", i, err)
		}
		if i+1 < len(members) {
			remaining := members[i+1]
			if err := remaining.verifyManagedConfig(ctx, settings.Cwd); err != nil {
				t.Fatalf("sibling lost account connection: %v", err)
			}
		}
	}
}
