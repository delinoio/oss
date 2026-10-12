// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCodexSafetyRequiresPrivateOriginalProof(t *testing.T) {
	for _, state := range []string{"missing", "empty", "foreign", "unowned", "uncorrelated", "late", "blocked", "finished"} {
		c, rpc := codexTerminalStatusFixture(t)
		before := len(rpc.events)
		event := codex.Event{Kind: codex.ModelSafetyEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true}
		switch state {
		case "empty":
			event.Safety = &codex.ModelSafetyObservation{}
		case "foreign":
			event.ThreadID = domain.NewID()
		case "unowned":
			event.TurnID = domain.NewID()
		case "uncorrelated":
			event.Correlated = false
		case "late":
			event.Late = true
		case "blocked":
			c.blocked = true
		case "finished":
			c.finished = true
		}
		finished := c.finished
		handled, err := c.PublishCore(context.Background(), event)
		if err == nil || handled || !c.blocked || c.finished != finished || len(rpc.events) != before {
			t.Fatal("unproved safety relaxed execution ownership", state, err)
		}
	}
}

func TestCodexSafetyPrivatePayloadCannotBeDecodedFromGenericJSON(t *testing.T) {
	var observation codex.ModelSafetyObservation
	raw := []byte(`{"kind":"reroute","fromModel":"private-safety-sentinel","toModel":"private-safety-sentinel","canonical":"private-safety-sentinel"}`)
	if err := json.Unmarshal(raw, &observation); err != nil {
		t.Fatal(err)
	}
	thread, turn := domain.NewID(), domain.NewID()
	if observation.PassiveFor(thread, turn) {
		t.Fatal("generic JSON manufactured private authority")
	}
	encoded, err := json.Marshal(observation)
	if err != nil || strings.Contains(string(encoded), "private-safety-sentinel") {
		t.Fatal("opaque private observation serialized native content", err)
	}
}
