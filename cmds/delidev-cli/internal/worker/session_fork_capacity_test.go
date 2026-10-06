// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/json"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"testing"
)

func TestOpenCodeForkCompleteResultCapacityBeforePreparation(t *testing.T) {
	input := domain.ForkJobInput{ChildSessionID: domain.NewID(), RuntimeID: domain.NewID()}
	for _, c := range []struct {
		messages  int
		exhausted bool
	}{{2, false}, {1000, false}, {3000, true}, {4096, true}} {
		t.Run(fmt.Sprint(c.messages), func(t *testing.T) {
			history := make([]opencode.HistoryMessage, c.messages)
			for index := range history {
				history[index] = opencode.HistoryMessage{ID: fmt.Sprintf("msg_%012xabcdefghijklmn", index), Parts: []opencode.HistoryPart{}}
				for part := 0; part < 4; part++ {
					history[index].Parts = append(history[index].Parts, opencode.HistoryPart{ID: fmt.Sprintf("prt_%012xabcdefghijklmn", index*4+part)})
				}
			}
			err := checkOpenCodeForkResultCapacity(input, json.RawMessage(`{"version":1}`), history)
			if c.exhausted && domain.SafeError(err).Code != domain.ResourceExhausted || !c.exhausted && err != nil {
				t.Fatal("complete identity map capacity", err)
			}
		})
	}
}
