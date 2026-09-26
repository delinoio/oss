package worker

import (
	"context"
	"testing"
)

func TestOpenCodeCompletionNeedsOriginalAcknowledgedTerminal(t *testing.T) {
	f, c := newOpenCodeEventsFixture(t)
	if _, err := c.Complete(context.Background()); err == nil || c.blocked {
		t.Fatal("premature completion acquired cleanup or poisoned original publication")
	}
	for _, scenario := range []string{"sequence", "closed-journal", "uncertain-terminal", "fabricated-native"} {
		t.Run(scenario, func(t *testing.T) {
			f, c := newOpenCodeEventsFixture(t)
			c.finished = true
			c.terminalSequence = 4
			switch scenario {
			case "sequence":
				c.terminalSequence++
			case "closed-journal":
				_ = f.c.binding.Close()
			case "uncertain-terminal":
				c.blocked = true
			}
			if completion, err := c.Complete(context.Background()); err == nil || completion.CleanupVerified || !c.blocked {
				t.Fatal("unproved original terminal/cleanup became completion")
			}
		})
	}
	if len(f.rpc.events) != 4 {
		t.Fatal("completion attempted another event")
	}
}
