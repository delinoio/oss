// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestThreadUnsubscribeRequiresExactClosed(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		closed bool
	}{
		{"closed", true},
		{"missing", false},
		{"foreign", false},
		{"status", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			client, _ := openThreadFixture(t, "thread-unsubscribe-"+scenario.name)
			limit := 250 * time.Millisecond
			if scenario.closed {
				limit = 3 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), limit)
			defer cancel()
			if _, err := client.StartThread(ctx, domain.NewID(), threadSettings(t)); err != nil {
				t.Fatal(err)
			}
			err := client.UnsubscribeAndWait(ctx, domain.NewID())
			if (err == nil) != scenario.closed || client.threadClosed != scenario.closed {
				t.Fatalf("closed=%v, error=%v", client.threadClosed, err)
			}
		})
	}
}
