// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"testing"
)

func TestRemoteControlObservationCannotPublishForbiddenStateAsDisabled(t *testing.T) {
	for _, policy := range []codex.RemoteControlPolicy{codex.RemoteControlPassive, codex.RemoteControlStateForbidden, codex.RemoteControlEnvironmentForbidden} {
		t.Run(string(policy), func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			before := len(rpc.events)
			observation := &codex.RemoteControlObservation{Status: codex.RemoteControlStatusDisabled, Policy: policy}
			if policy == codex.RemoteControlStateForbidden {
				observation.Status = codex.RemoteControlStatusConnected
			}
			if policy == codex.RemoteControlEnvironmentForbidden {
				observation.EnvironmentPresent = true
			}
			event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.RemoteControlDisabled, Correlated: true, ThreadID: c.thread, RemoteControl: observation}
			handled, err := c.PublishCore(context.Background(), event)
			if len(rpc.events) != before || c.finished {
				t.Fatal("remote observation gained publication authority")
			}
			if policy == codex.RemoteControlPassive {
				if err != nil || !handled || c.blocked {
					t.Fatal("passive disabled refused", handled, err)
				}
			} else if err == nil || handled || !c.blocked {
				t.Fatal("forbidden observation bypassed recovery", handled, err)
			}
		})
	}
}
