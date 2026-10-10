// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func validatePublishedNativeAppCall(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, event domain.ExecutionEvent) error {
	proof := event.Interaction.NativeApps
	if proof == nil || input.NativeApps == nil || proof.Validate() != nil || input.Configuration.Harness != domain.Codex || input.Fork != nil {
		return domain.NativeAppsUnavailable()
	}
	scope := domain.NativeAppsAssignmentScope(input)
	if scope != proof.Scope {
		return domain.NativeAppsUnavailable()
	}
	original, _ := json.Marshal(input.NativeApps)
	reported, _ := json.Marshal(proof.Selection)
	if !bytes.Equal(original, reported) {
		return domain.NativeAppsUnavailable()
	}
	tool, err := tx.CodexAppsTool(session.ID, input.ExecutionID, event.NativeThreadID, event.NativeTurnID, proof.NativeItemID)
	if err != nil {
		return err
	}
	if tool.Role != domain.ToolMessage || tool.State != domain.MessageStreaming || tool.Tool == nil || tool.Tool.Completed != nil || tool.Tool.Started.Kind != domain.NativeAppsTool || tool.Tool.Started.Apps == nil || tool.Tool.Started.Apps.AppID != proof.AppID {
		return domain.NativeAppsUnavailable()
	}
	return nil
}
